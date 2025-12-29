package blocklist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"adblock/internal/config"
)

func TestManager_LoadBlocklists(t *testing.T) {
	// 1. Setup Mock HTTP Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
# This is a comment
127.0.0.1 example.com
0.0.0.0   ads.doubleclick.net
127.0.0.1 ignored
`))
	}))
	defer ts.Close()

	// 2. Setup Config
	tmpDir, _ := os.MkdirTemp("", "sinkhole_test")
	defer os.RemoveAll(tmpDir)

	cfg := config.Default()
	cfg.CacheDir = tmpDir
	cfg.Blocklists = []config.BlocklistSource{
		{Name: "TestList", URL: ts.URL, Format: "hosts", Enabled: true},
	}

	// 3. Test Load
	mgr := NewManager(cfg)
	err := mgr.LoadBlocklists(context.Background())
	if err != nil {
		t.Fatalf("LoadBlocklists failed: %v", err)
	}

	// 4. Verify Logic
	if mgr.Stats() != 3 {
		t.Errorf("Expected 3 blocked domains, got %d", mgr.Stats())
	}
	if !mgr.IsBlocked("example.com") {
		t.Error("example.com should be blocked")
	}
	if !mgr.IsBlocked("ads.doubleclick.net") {
		t.Error("ads.doubleclick.net should be blocked")
	}
	if mgr.IsBlocked("google.com") {
		t.Error("google.com should NOT be blocked")
	}

	// 5. Verify Cache Created
	files, _ := os.ReadDir(tmpDir)
	if len(files) != 1 {
		t.Error("Cache file was not created")
	}
}

func TestParseHostsLine(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"127.0.0.1 example.com", "example.com"},
		{"0.0.0.0 ad.com", "ad.com"},
		{"# 127.0.0.1 commented.com", ""},
		{"   127.0.0.1   spaced.com  ", "spaced.com"},
		{"127.0.0.1 inline.comment # comment", "inline.comment"},
		{"not.an.ip invalid.com", ""},
	}

	for _, tt := range tests {
		got := parseHostsLine(tt.input)
		if got != tt.expected {
			t.Errorf("parseHostsLine(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
