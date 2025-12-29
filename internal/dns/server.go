package dns

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"0x53/internal/config"
	"0x53/internal/core"

	"github.com/miekg/dns"
)

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
	
	Ready chan struct{} // Closed when server is listening
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
			Timeout: 2 * time.Second,
			Net:     "udp",
			SingleInflight: true,
		},
		upstreamAddr: "8.8.8.8:53", // Default, will be overriden by config
		Ready:        make(chan struct{}),
	}
}

// Start begins listening on the configured port.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.BindIP, s.cfg.BindPort)
	
	s.udpServer = &dns.Server{
		Addr: addr, 
		Net: "udp",
		NotifyStartedFunc: func() {
			close(s.Ready)
		},
	}
	s.udpServer.Handler = dns.HandlerFunc(s.handleRequest)
	
	// Handle Upstream Configuration
	s.configureUpstream()

	fmt.Printf("Starting DNS Server on %s (Upstream: %s)\n", addr, s.upstreamAddr)

	// Run in goroutine to allow non-blocking start
	go func() {
		if err := s.udpServer.ListenAndServe(); err != nil {
			fmt.Printf("Failed to start UDP server: %v\n", err)
		}
	}()
	
	return nil
}

// configureUpstream sets the upstream resolver based on config.
func (s *Server) configureUpstream() {
	switch s.cfg.Upstream {
	case config.UpstreamCloudflare:
		s.upstreamAddr = "1.1.1.1:53"
	case config.UpstreamGoogle:
		s.upstreamAddr = "8.8.8.8:53"
	case config.UpstreamCustom:
		s.upstreamAddr = s.cfg.CustomUpstream
	case config.UpstreamAuto:
		// TODO: Implement autodetection from /etc/resolv.conf
		s.upstreamAddr = "8.8.8.8:53" 
	}
}

// Stop shuts down the server.
func (s *Server) Stop() error {
	if s.udpServer != nil {
		return s.udpServer.Shutdown()
	}
	return nil
}

// Reload re-reads configuration (stub).
func (s *Server) Reload() error {
	return nil
}

// handleRequest is the main DNS query entry point.
func (s *Server) handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Compress = true
	m.Authoritative = true

	// We only handle standard queries (OpcodeQuery)
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

		if s.blocklists != nil && s.blocklists.IsBlocked(lookupName) {
			atomic.AddUint64(&s.statsBlocked, 1)
			
			s.mu.RLock()
			if s.logFunc != nil {
				s.logFunc(fmt.Sprintf("[BLOCKED] %s", lookupName))
			}
			s.mu.RUnlock()
			
			s.sinkhole(w, r)
			return
		}
		
		// Log Allowed
		s.mu.RLock()
		if s.logFunc != nil {
			s.logFunc(fmt.Sprintf("[ALLOWED] %s", lookupName))
		}
		s.mu.RUnlock()
	}

	s.forward(w, r)
}

// sinkhole responds with 0.0.0.0 (A) or :: (AAAA).
func (s *Server) sinkhole(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	
	// Create NXDOMAIN or 0.0.0.0 response
	// Adblockers usually prefer 0.0.0.0 for speed, some use NXDOMAIN.
	// We'll use 0.0.0.0 A Record.
	
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
