package dns

import (
	"context"
	"net"
	"testing"
	"time"

	"adblock/internal/blocklist"
	"adblock/internal/config"
	"github.com/miekg/dns"
)

func TestServer_Blocking(t *testing.T) {
	// Setup
	cfg := config.Default()
	cfg.BindPort = 5354 // Use high port for test
	
	bl := blocklist.NewMockManager()
	bl.Add("example.com")
	
	srv := NewServer(cfg, bl)
	
	// Start Server
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer srv.Stop()
	
	// Wait for startup
	time.Sleep(100 * time.Millisecond)
	
	// Client Setup
	c := new(dns.Client)
	c.Timeout = 1 * time.Second
	addr := "127.0.0.1:5354"
	
	// Test Case 1: Blocked Domain
	m := new(dns.Msg)
	m.SetQuestion("example.com.", dns.TypeA)
	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}
	
	if len(r.Answer) == 0 {
		t.Fatal("Expected answer for blocked domain")
	}
	aRecord, ok := r.Answer[0].(*dns.A)
	if !ok {
		t.Fatal("Expected A record")
	}
	if !aRecord.A.Equal(net.IPv4(0, 0, 0, 0)) {
		t.Errorf("Expected 0.0.0.0, got %v", aRecord.A)
	}
	
	// Test Case 2: Allowed Domain (Forwarding)
	// Note: This relies on 8.8.8.8 being reachable. In a pure unit test we should mock the upstream client too.
	// For "Infra-First", we might want to skip this if network is restricted, but usually fine for dev.
	m2 := new(dns.Msg)
	m2.SetQuestion("google.com.", dns.TypeA)
	r2, _, err := c.Exchange(m2, addr)
	if err != nil {
		t.Logf("Forwarding failed (expected if no network): %v", err)
	} else {
		if len(r2.Answer) == 0 {
			t.Errorf("Expected answer for google.com")
		}
	}
}
