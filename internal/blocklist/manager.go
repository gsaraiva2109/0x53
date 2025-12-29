package blocklist

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"adblock/internal/config"
)

// Manager implements core.BlocklistManager.
type Manager struct {
	cfg     *config.Config
	domains map[string]struct{}
	logFunc func(string)
	mu      sync.RWMutex
}

// SetLogger sets the logging callback.
func (m *Manager) SetLogger(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logFunc = fn
}

func (m *Manager) log(format string, args ...interface{}) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.logFunc != nil {
		m.logFunc(fmt.Sprintf(format, args...))
	}
}

// NewManager creates a new blocklist manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg:     cfg,
		domains: make(map[string]struct{}),
	}
}

// LoadBlocklists fetches and parses all enabled blocklists.
func (m *Manager) LoadBlocklists(ctx context.Context) error {
	var wg sync.WaitGroup
	var mu sync.Mutex

	newMap := make(map[string]struct{})

	// Ensure cache dir exists
	if err := os.MkdirAll(m.cfg.CacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}

	// Track globally
	var totalProcessed int64
	var duplicates int64
	var statMu sync.Mutex

	for _, source := range m.cfg.Blocklists {
		if !source.Enabled {
			continue
		}

		wg.Add(1)
		go func(src config.BlocklistSource) {
			defer wg.Done()

			// Try cache first or download
			m.log("Fetching source: %s...", src.Name)
			content, err := m.fetchEx(ctx, src)
			if err != nil {
				m.log("Failed to fetch %s: %v", src.Name, err)
				return
			}
			m.log("Fetched %s (Size: %d bytes). Parsing...", src.Name, len(content))

			// Parse into LOCAL map to avoid mutex contention on every line
			localMap := make(map[string]struct{})
			count := 0

			scanner := bufio.NewScanner(strings.NewReader(content))
			// Increase buffer for long lines
			buf := make([]byte, 0, 64*1024)
			scanner.Buffer(buf, 1024*1024)

			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				var domain string

				if src.Format == "hosts" {
					domain = parseHostsLine(line)
				} else {
					// Assume raw domain list
					// Remove comments
					if idx := strings.Index(line, "#"); idx != -1 {
						line = line[:idx]
					}
					line = strings.TrimSpace(line)
					if line != "" {
						domain = strings.ToLower(line)
					}
				}

				if domain != "" {
					// Normalize: remove trailing dot
					if strings.HasSuffix(domain, ".") {
						domain = domain[:len(domain)-1]
					}
					localMap[domain] = struct{}{}
					count++
				}
			}

			if err := scanner.Err(); err != nil {
				m.log("Error scanning %s: %v", src.Name, err)
			}

			// Merge local results into main map (Single Lock)
			if count > 0 {
				mu.Lock()
				for k := range localMap {
					if _, exists := newMap[k]; exists {
						statMu.Lock()
						duplicates++
						statMu.Unlock()
					}
					newMap[k] = struct{}{}
				}
				mu.Unlock()

				statMu.Lock()
				totalProcessed += int64(count)
				statMu.Unlock()
			}

			m.log("Loaded %d domains from %s", count, src.Name)
		}(source)
	}

	wg.Wait()

	m.mu.Lock()
	m.domains = newMap
	m.mu.Unlock()

	m.log("Blocklist Update Complete.")
	m.log("Total Rules: %d | Duplicates Removed: %d", len(newMap), duplicates)
	return nil
}

// fetchEx handles caching and downloading.
func (m *Manager) fetchEx(ctx context.Context, src config.BlocklistSource) (string, error) {
	hash := md5.Sum([]byte(src.URL))
	filename := filepath.Join(m.cfg.CacheDir, hex.EncodeToString(hash[:])+".txt")

	// Check cache (valid for 24h)
	info, err := os.Stat(filename)
	if err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		content, err := os.ReadFile(filename)
		if err == nil {
			return string(content), nil
		}
	}

	// Download
	req, err := http.NewRequestWithContext(ctx, "GET", src.URL, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: 120 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("bad status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Save to cache
	_ = os.WriteFile(filename, body, 0644)

	return string(body), nil
}

// parseHostsLine extracts domain from "0.0.0.0 domain.com" format.
func parseHostsLine(line string) string {
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	// Remove trailing comments
	if idx := strings.Index(line, "#"); idx != -1 {
		line = line[:idx]
	}
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		addr := fields[0]
		if addr == "0.0.0.0" || addr == "127.0.0.1" || addr == "::1" || addr == "0" {
			return strings.ToLower(fields[1])
		}
	}
	return ""
}

func (m *Manager) IsBlocked(domain string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Normalize
	domain = strings.ToLower(domain)
	if strings.HasSuffix(domain, ".") {
		domain = domain[:len(domain)-1]
	}

	// 1. Exact Match
	if _, ok := m.domains[domain]; ok {
		return true
	}

	// 2. Subdomain Walking (Alloc-free)
	// Example: "ads.google.com" -> check "google.com" -> check "com"
	idx := 0
	for {
		idx = strings.Index(domain, ".")
		if idx == -1 {
			break
		}
		// Slice matches the remainder string
		domain = domain[idx+1:]

		// Optimization: Don't block TLDs alone (e.g. "com") unless explicit
		if strings.Index(domain, ".") == -1 {
			// Current 'domain' is a TLD (no more dots). Allow it safe?
			// Some blocklists might block TLDs like "zip".
			// Let's allow TLD checking for robustness if user adds "zip".
		}

		if _, ok := m.domains[domain]; ok {
			return true
		}
	}

	return false
}

func (m *Manager) Stats() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.domains)
}

func (m *Manager) ListSources() []config.BlocklistSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return copy to prevent races
	dst := make([]config.BlocklistSource, len(m.cfg.Blocklists))
	copy(dst, m.cfg.Blocklists)
	return dst
}

func (m *Manager) ToggleSource(name string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, src := range m.cfg.Blocklists {
		if src.Name == name {
			m.cfg.Blocklists[i].Enabled = enabled
			return nil
		}
	}
	return fmt.Errorf("source not found: %s", name)
}

func (m *Manager) InvalidateCache() error {
	return os.RemoveAll(m.cfg.CacheDir)
}
