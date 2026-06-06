package dns

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"0x53/internal/blocklist"
	"0x53/internal/config"
	"github.com/miekg/dns"
)

// freeUDPPort finds an available UDP port by binding to :0.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeUDPPort: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}

// startTestServer starts a DNS server on a free port and registers cleanup via t.Cleanup.
// Returns the server and its bound address.
func startTestServer(t *testing.T, cfg *config.Config) (*Server, string) {
	t.Helper()
	bl := blocklist.NewMockManager()
	bl.Add("blocked.example.com")

	cfg.BindPort = freeUDPPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.BindPort)
	cfg.BindIP = "127.0.0.1"

	srv := NewServer(cfg, bl)
	ctx, cancel := context.WithCancel(context.Background())

	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("failed to start server: %v", err)
	}

	select {
	case <-srv.Ready:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("server did not become ready in time")
	}

	t.Cleanup(func() {
		cancel()
		srv.Stop()
	})

	return srv, addr
}

func TestServer_BlockingSinkhole(t *testing.T) {
	cfg := config.Default()
	cfg.BlockingMode = config.BlockModeSinkhole

	_, addr := startTestServer(t, cfg)

	c := &dns.Client{Timeout: 1 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion("blocked.example.com.", dns.TypeA)

	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if len(r.Answer) == 0 {
		t.Fatal("expected sinkhole A record in answer")
	}
	a, ok := r.Answer[0].(*dns.A)
	if !ok {
		t.Fatal("expected A record type")
	}
	if !a.A.Equal(net.IPv4(0, 0, 0, 0)) {
		t.Errorf("sinkhole: expected 0.0.0.0, got %v", a.A)
	}
}

func TestServer_BlockingNXDOMAIN(t *testing.T) {
	cfg := config.Default()
	cfg.BlockingMode = config.BlockModeNXDOMAIN

	_, addr := startTestServer(t, cfg)

	c := &dns.Client{Timeout: 1 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion("blocked.example.com.", dns.TypeA)

	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("NXDOMAIN mode: expected NXDOMAIN rcode, got %d", r.Rcode)
	}
	if len(r.Answer) != 0 {
		t.Error("NXDOMAIN mode: expected no answer records")
	}
}

func TestServer_QueryLog(t *testing.T) {
	cfg := config.Default()
	cfg.BlockingMode = config.BlockModeSinkhole

	srv, addr := startTestServer(t, cfg)

	c := &dns.Client{Timeout: 1 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion("blocked.example.com.", dns.TypeA)
	if _, _, err := c.Exchange(m, addr); err != nil {
		t.Fatalf("exchange failed: %v", err)
	}

	entries := srv.GetRecentQueries(10)
	if len(entries) == 0 {
		t.Fatal("expected at least one query log entry")
	}

	var found bool
	for _, e := range entries {
		if e.Domain == "blocked.example.com" && e.Action == "blocked" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected blocked entry for blocked.example.com, got: %+v", entries)
	}
}

func TestServer_GracefulDrain(t *testing.T) {
	cfg := config.Default()

	srv, addr := startTestServer(t, cfg)

	// Signal drain then verify new requests get REFUSED.
	srv.draining.Store(true)

	c := &dns.Client{Timeout: 1 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion("blocked.example.com.", dns.TypeA)

	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if r.Rcode != dns.RcodeRefused {
		t.Errorf("expected REFUSED during drain, got rcode %d", r.Rcode)
	}
}

func TestServer_StopIdempotent(t *testing.T) {
	cfg := config.Default()

	srv, _ := startTestServer(t, cfg)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.Stop()
		}()
	}
	wg.Wait()
}

func TestServer_QueryLogRingBuffer(t *testing.T) {
	cfg := config.Default()

	bl := blocklist.NewMockManager()
	cfg.BindPort = freeUDPPort(t)
	cfg.BindIP = "127.0.0.1"
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.BindPort)

	srv := NewServer(cfg, bl)
	srv.queryLogMax = 3

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); srv.Stop() })

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	<-srv.Ready

	c := &dns.Client{Timeout: 1 * time.Second}
	for i := 0; i < 5; i++ {
		m := new(dns.Msg)
		m.SetQuestion("blocked.example.com.", dns.TypeA)
		c.Exchange(m, addr) //nolint:errcheck
	}

	entries := srv.GetRecentQueries(100)
	if len(entries) > 3 {
		t.Errorf("ring buffer exceeded max: got %d entries, want ≤3", len(entries))
	}
}
