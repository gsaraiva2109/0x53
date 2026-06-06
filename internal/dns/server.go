package dns

import (
	"context"
	"crypto/tls"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"0x53/internal/config"
	"0x53/internal/core"

	"github.com/miekg/dns"
)

const maxQueryLog = 1000

// Server implements the core.Engine interface for DNS handling.
type Server struct {
	cfg        *config.Config
	blocklists core.BlocklistManager

	udpServer *dns.Server

	upstreamClient *dns.Client
	upstreamAddr   string

	statsQueries uint64
	statsBlocked uint64

	logFunc func(string) // Optional logger callback

	mu sync.RWMutex

	Ready   chan struct{} // Closed when server is listening
	errChan chan error    // Receives ListenAndServe errors

	draining  atomic.Bool    // Set to true during graceful shutdown
	inflight  sync.WaitGroup // Tracks in-flight requests for drain
	stopOnce  sync.Once      // Ensures Stop is idempotent

	queryLog    []core.QueryEntry
	queryLogMu  sync.RWMutex
	queryLogMax int

	cache     *responseCache
	dotConn   *dns.Conn // persistent connection for DoT upstream
	dotConnMu sync.Mutex
}

// SetLogger sets the callback for logging events.
func (s *Server) SetLogger(fn func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logFunc = fn
}

// Stats returns atomic snapshots of counters.
func (s *Server) Stats() (int, int) {
	return int(atomic.LoadUint64(&s.statsQueries)), int(atomic.LoadUint64(&s.statsBlocked))
}

// NewServer creates a new DNS server instance.
func NewServer(cfg *config.Config, bl core.BlocklistManager) *Server {
	return &Server{
		cfg:        cfg,
		blocklists: bl,
		upstreamClient: &dns.Client{
			Timeout:        2 * time.Second,
			Net:            "udp",
			SingleInflight: true,
		},
		upstreamAddr: "8.8.8.8:53", // Default, overridden by configureUpstream.
		Ready:        make(chan struct{}),
		errChan:      make(chan error, 1),
		queryLogMax:  maxQueryLog,
		cache:        newResponseCache(5000),
	}
}

// Start begins listening on the configured port.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.BindIP, s.cfg.BindPort)

	s.udpServer = &dns.Server{
		Addr: addr,
		Net:  "udp",
		NotifyStartedFunc: func() {
			close(s.Ready)
		},
	}
	s.udpServer.Handler = dns.HandlerFunc(s.handleRequest)

	s.configureUpstream()

	fmt.Printf("Starting DNS Server on %s (Upstream: %s [%s])\n", addr, s.upstreamAddr, s.upstreamClient.Net)

	// Run in goroutine to allow non-blocking start.
	// Errors are captured in errChan so callers can detect startup failures.
	go func() {
		if err := s.udpServer.ListenAndServe(); err != nil {
			s.errChan <- err
		}
	}()

	// Wait briefly for either Ready or an immediate error.
	select {
	case <-s.Ready:
	case err := <-s.errChan:
		return fmt.Errorf("dns server failed to start: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	// Wire context cancellation to server shutdown.
	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	return nil
}

// configureUpstream sets the upstream resolver and transport based on config.
func (s *Server) configureUpstream() {
	// Reset transport to safe default before switching.
	s.upstreamClient.Net = "udp"
	s.upstreamClient.TLSConfig = nil

	switch s.cfg.Upstream {
	case config.UpstreamCloudflareDoT:
		s.upstreamAddr = "1.1.1.1:853"
		s.upstreamClient.Net = "tcp-tls"
		s.upstreamClient.TLSConfig = &tls.Config{
			ServerName: "cloudflare-dns.com",
			MinVersion: tls.VersionTLS12,
		}
	case config.UpstreamGoogleDoT:
		s.upstreamAddr = "8.8.8.8:853"
		s.upstreamClient.Net = "tcp-tls"
		s.upstreamClient.TLSConfig = &tls.Config{
			ServerName: "dns.google",
			MinVersion: tls.VersionTLS12,
		}
	case config.UpstreamCloudflare:
		s.upstreamAddr = "1.1.1.1:53"
	case config.UpstreamGoogle:
		s.upstreamAddr = "8.8.8.8:53"
	case config.UpstreamCustom:
		s.upstreamAddr = s.cfg.CustomUpstream
	case config.UpstreamAuto:
		s.upstreamAddr = "8.8.8.8:53"
	}
}

// Stop gracefully shuts down the server. Safe to call multiple times.
func (s *Server) Stop() error {
	var err error
	s.stopOnce.Do(func() {
		s.draining.Store(true)
		s.inflight.Wait()
		s.dotConnMu.Lock()
		if s.dotConn != nil {
			s.dotConn.Close()
			s.dotConn = nil
		}
		s.dotConnMu.Unlock()
		if s.udpServer != nil {
			err = s.udpServer.Shutdown()
		}
	})
	return err
}

// Reload re-reads config from disk and applies hot-reloadable fields.
func (s *Server) Reload() error {
	cfgPath := filepath.Join(s.cfg.ConfigDir, "config.yaml")
	newCfg, err := config.LoadFromFile(cfgPath)
	if err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	s.mu.Lock()
	s.cfg.Upstream = newCfg.Upstream
	s.cfg.CustomUpstream = newCfg.CustomUpstream
	s.cfg.BlockingMode = newCfg.BlockingMode
	s.cfg.EnableIPv6 = newCfg.EnableIPv6
	s.configureUpstream()
	s.mu.Unlock()
	return nil
}

// recordQuery appends an entry to the query log ring buffer.
func (s *Server) recordQuery(entry core.QueryEntry) {
	s.queryLogMu.Lock()
	defer s.queryLogMu.Unlock()
	s.queryLog = append(s.queryLog, entry)
	if len(s.queryLog) > s.queryLogMax {
		s.queryLog = s.queryLog[len(s.queryLog)-s.queryLogMax:]
	}
}

// GetRecentQueries returns the last N query log entries.
func (s *Server) GetRecentQueries(count int) []core.QueryEntry {
	s.queryLogMu.RLock()
	defer s.queryLogMu.RUnlock()
	if count <= 0 || count > len(s.queryLog) {
		count = len(s.queryLog)
	}
	dst := make([]core.QueryEntry, count)
	copy(dst, s.queryLog[len(s.queryLog)-count:])
	return dst
}

// handleRequest is the main DNS query entry point.
func (s *Server) handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()

	// Add to inflight BEFORE checking drain to close the TOCTOU window:
	// Stop() sets draining=true then waits on inflight; by incrementing first
	// we guarantee Stop() must wait even if we ultimately reject the request.
	s.inflight.Add(1)
	defer s.inflight.Done()

	if s.draining.Load() {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeRefused
		w.WriteMsg(m)
		return
	}

	// Only handle standard queries (OpcodeQuery).
	if r.Opcode != dns.OpcodeQuery {
		s.forward(w, r)
		return
	}

	atomic.AddUint64(&s.statsQueries, 1)

	for _, q := range r.Question {
		name := q.Name
		lookupName := name
		if len(name) > 0 && name[len(name)-1] == '.' {
			lookupName = name[:len(name)-1]
		}

		// 1. Check local records (custom DNS overrides).
		s.mu.RLock()
		_, hasLocal := s.cfg.LocalRecords[lookupName]
		var localIP string
		if hasLocal {
			localIP = s.cfg.LocalRecords[lookupName]
		}
		s.mu.RUnlock()

		if hasLocal {
			s.recordQuery(core.QueryEntry{
				Timestamp: start,
				Domain:    lookupName,
				Action:    "local",
				Latency:   time.Since(start),
			})
			if s.logFunc != nil {
				s.logFunc(fmt.Sprintf("[LOCAL] %s -> %s", lookupName, localIP))
			}
			s.respondA(w, r, localIP)
			return
		}

		// 2. Check blocklist.
		if s.blocklists != nil && s.blocklists.IsBlocked(lookupName) {
			atomic.AddUint64(&s.statsBlocked, 1)
			s.recordQuery(core.QueryEntry{
				Timestamp: start,
				Domain:    lookupName,
				Action:    "blocked",
				Latency:   time.Since(start),
			})
			if s.logFunc != nil {
				s.logFunc(fmt.Sprintf("[BLOCKED] %s", lookupName))
			}
			s.blockResponse(w, r)
			return
		}

		// 3. Allowed — log, record, and forward.
		if s.logFunc != nil {
			s.logFunc(fmt.Sprintf("[ALLOWED] %s", lookupName))
		}
	}

	// Use the first question's domain for the query log entry.
	qDomain := ""
	if len(r.Question) > 0 {
		qDomain = r.Question[0].Name
		qDomain = strings.TrimSuffix(qDomain, ".")
	}
	s.forward(w, r)
	s.recordQuery(core.QueryEntry{
		Timestamp: start,
		Domain:    qDomain,
		Action:    "allowed",
		Latency:   time.Since(start),
	})
}

// blockResponse answers a blocked query according to the configured blocking mode.
func (s *Server) blockResponse(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)

	if s.cfg.BlockingMode == config.BlockModeNXDOMAIN {
		m.Rcode = dns.RcodeNameError
		w.WriteMsg(m)
		return
	}

	// Default: sinkhole with 0.0.0.0 / ::.
	for _, q := range r.Question {
		switch q.Qtype {
		case dns.TypeA:
			rr, _ := dns.NewRR(fmt.Sprintf("%s 3600 IN A 0.0.0.0", q.Name))
			m.Answer = append(m.Answer, rr)
		case dns.TypeAAAA:
			rr, _ := dns.NewRR(fmt.Sprintf("%s 3600 IN AAAA ::", q.Name))
			m.Answer = append(m.Answer, rr)
		}
	}

	w.WriteMsg(m)
}

// forward sends the query to the upstream resolver, using the response cache.
func (s *Server) forward(w dns.ResponseWriter, r *dns.Msg) {
	// Cache hit: skip upstream entirely.
	if cached := s.cache.get(r); cached != nil {
		w.WriteMsg(cached)
		return
	}

	resp, err := s.exchangeUpstream(r)
	if err != nil {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
		return
	}

	s.cache.set(r, resp)
	w.WriteMsg(resp)
}

// exchangeUpstream sends r to the configured upstream. For DoT, it reuses a
// persistent TCP-TLS connection and reconnects transparently on error.
func (s *Server) exchangeUpstream(r *dns.Msg) (*dns.Msg, error) {
	if s.upstreamClient.Net != "tcp-tls" {
		resp, _, err := s.upstreamClient.Exchange(r, s.upstreamAddr)
		return resp, err
	}

	// DoT path: try persistent connection first.
	s.dotConnMu.Lock()
	conn := s.dotConn
	s.dotConnMu.Unlock()

	if conn != nil {
		resp, _, err := s.upstreamClient.ExchangeWithConn(r, conn)
		if err == nil {
			return resp, nil
		}
		// Stale connection — close and fall through to reconnect.
		conn.Close()
		s.dotConnMu.Lock()
		s.dotConn = nil
		s.dotConnMu.Unlock()
	}

	// Establish a new persistent connection.
	newConn, err := s.upstreamClient.Dial(s.upstreamAddr)
	if err != nil {
		// Fall back to ephemeral exchange.
		resp, _, err := s.upstreamClient.Exchange(r, s.upstreamAddr)
		return resp, err
	}

	resp, _, err := s.upstreamClient.ExchangeWithConn(r, newConn)
	if err != nil {
		newConn.Close()
		// One retry with ephemeral connection.
		resp, _, err = s.upstreamClient.Exchange(r, s.upstreamAddr)
		return resp, err
	}

	s.dotConnMu.Lock()
	s.dotConn = newConn
	s.dotConnMu.Unlock()

	return resp, nil
}

// respondA sends a specific A record response.
func (s *Server) respondA(w dns.ResponseWriter, r *dns.Msg, ip string) {
	m := new(dns.Msg)
	m.SetReply(r)

	for _, q := range r.Question {
		if q.Qtype == dns.TypeA {
			rr, err := dns.NewRR(fmt.Sprintf("%s 3600 IN A %s", q.Name, ip))
			if err == nil {
				m.Answer = append(m.Answer, rr)
			}
		}
	}
	w.WriteMsg(m)
}

// --- Local Records Implementation ---

func (s *Server) AddLocalRecord(domain, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cfg.LocalRecords == nil {
		s.cfg.LocalRecords = make(map[string]string)
	}

	domain = s.normalizeDomain(domain)
	s.cfg.LocalRecords[domain] = ip
	return config.Save(s.cfg, filepath.Join(s.cfg.ConfigDir, "config.yaml"))
}

func (s *Server) RemoveLocalRecord(domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cfg.LocalRecords == nil {
		return nil
	}

	domain = s.normalizeDomain(domain)
	delete(s.cfg.LocalRecords, domain)
	return config.Save(s.cfg, filepath.Join(s.cfg.ConfigDir, "config.yaml"))
}

func (s *Server) ListLocalRecords() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dst := make(map[string]string)
	for k, v := range s.cfg.LocalRecords {
		dst[k] = v
	}
	return dst
}

func (s *Server) normalizeDomain(d string) string {
	if len(d) > 0 && d[len(d)-1] == '.' {
		return d[:len(d)-1]
	}
	return d
}
