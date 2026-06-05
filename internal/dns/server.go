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

	draining atomic.Bool   // Set to true during graceful shutdown
	inflight sync.WaitGroup // Tracks in-flight requests for drain

	queryLog    []core.QueryEntry
	queryLogMu  sync.RWMutex
	queryLogMax int
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
		return nil
	case err := <-s.errChan:
		return fmt.Errorf("dns server failed to start: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
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

// Stop gracefully shuts down the server: stop accepting new queries,
// wait for in-flight requests to finish, then close the socket.
func (s *Server) Stop() error {
	s.draining.Store(true)
	s.inflight.Wait()
	if s.udpServer != nil {
		return s.udpServer.Shutdown()
	}
	return nil
}

// Reload re-reads configuration (stub).
func (s *Server) Reload() error {
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

	// Reject new requests during graceful drain.
	if s.draining.Load() {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeRefused
		w.WriteMsg(m)
		return
	}
	s.inflight.Add(1)
	defer s.inflight.Done()

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

// forward sends the query to the upstream resolver.
func (s *Server) forward(w dns.ResponseWriter, r *dns.Msg) {
	resp, _, err := s.upstreamClient.Exchange(r, s.upstreamAddr)
	if err != nil {
		// On error, return SERVFAIL
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
		return
	}

	w.WriteMsg(resp)
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
