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

	"0x53/internal/config"
)

// Manager implements core.BlocklistManager.
type Manager struct {
	cfg     *config.Config
	domains map[string]struct{}
	// allowlistMap provides O(1) exact-match lookup.
	allowlistMap map[string]struct{}
	// wildcardAllowlist stores suffix patterns like ".example.com" for "*.example.com".
	wildcardAllowlist []string
	logFunc           func(string)
	mu                sync.RWMutex
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
	mgr := &Manager{
		cfg:          cfg,
		domains:      make(map[string]struct{}),
		allowlistMap: make(map[string]struct{}),
	}
	mgr.syncAllowlistMap()
	return mgr
}

func (m *Manager) syncAllowlistMap() {
	m.allowlistMap = make(map[string]struct{})
	m.wildcardAllowlist = nil
	for _, domain := range m.cfg.Allowlist {
		domain = strings.ToLower(domain)
		if strings.HasPrefix(domain, "*.") {
			// Store suffix as ".example.com" for HasSuffix matching.
			m.wildcardAllowlist = append(m.wildcardAllowlist, domain[1:])
		} else {
			m.allowlistMap[domain] = struct{}{}
		}
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

	// Snapshot sources under lock to avoid races with ToggleSource.
	m.mu.RLock()
	sources := make([]config.BlocklistSource, len(m.cfg.Blocklists))
	copy(sources, m.cfg.Blocklists)
	m.mu.RUnlock()

	for _, source := range sources {
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

			// Auto-detect format if configured as "auto" or empty.
			format := detectFormat(content, src.Format)

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

				if format == "hosts" {
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
					domain = strings.TrimSuffix(domain, ".")
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

	// Determine TTL: per-source override > global > default 24h.
	ttl := m.cfg.CacheTTL
	if src.CacheTTL > 0 {
		ttl = src.CacheTTL
	}
	if ttl == 0 {
		ttl = 24 * time.Hour
	}

	// Check cache.
	info, err := os.Stat(filename)
	if err == nil && time.Since(info.ModTime()) < ttl {
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

// detectFormat guesses the blocklist format from the first 10 non-comment lines.
// Returns "hosts" if most lines match the hosts-file pattern, "wild" otherwise.
// If configured explicitly (not "auto" or empty), returns the configured format.
func detectFormat(content, configured string) string {
	if configured != "" && configured != "auto" {
		return configured
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	hostsCount := 0
	totalCount := 0

	for scanner.Scan() && totalCount < 10 {
		line := strings.TrimSpace(scanner.Text())
		// Skip blank lines and comments (both # and ! style).
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		totalCount++
		if parseHostsLine(line) != "" {
			hostsCount++
		}
	}

	// If more than half of sampled lines look like hosts format, treat as hosts.
	if totalCount > 0 && hostsCount > totalCount/2 {
		return "hosts"
	}
	return "wild"
}

func (m *Manager) IsBlocked(domain string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Normalize
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")

	// 0. Check Allowlist (Exact Match).
	if _, allowed := m.allowlistMap[domain]; allowed {
		return false
	}

	// 0b. Check Wildcard Allowlist.
	// "ads.example.com" matches "*.example.com" (stored as ".example.com").
	for _, suffix := range m.wildcardAllowlist {
		if strings.HasSuffix(domain, suffix) {
			return false
		}
	}

	// 1. Exact Match
	if _, ok := m.domains[domain]; ok {
		return true
	}

	// 2. Subdomain walking.
	// Example: "ads.google.com" -> check "google.com" -> check "com".
	for {
		idx := strings.Index(domain, ".")
		if idx == -1 {
			break
		}
		domain = domain[idx+1:]

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
	found := false
	for i, src := range m.cfg.Blocklists {
		if src.Name == name {
			m.cfg.Blocklists[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		m.mu.Unlock()
		return fmt.Errorf("source not found: %s", name)
	}
	savePath := filepath.Join(m.cfg.ConfigDir, "config.yaml")
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	return config.Save(m.cfg, savePath)
}

// --- Allowlist Implementation ---

func (m *Manager) AddAllowed(domain string) error {
	m.mu.Lock()
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		m.mu.Unlock()
		return fmt.Errorf("empty domain")
	}

	found := false
	for _, d := range m.cfg.Allowlist {
		if d == domain {
			found = true
			break
		}
	}
	if !found {
		m.cfg.Allowlist = append(m.cfg.Allowlist, domain)
	}
	m.syncAllowlistMap()
	savePath := filepath.Join(m.cfg.ConfigDir, "config.yaml")
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	return config.Save(m.cfg, savePath)
}

func (m *Manager) RemoveAllowed(domain string) error {
	m.mu.Lock()
	domain = strings.ToLower(strings.TrimSpace(domain))
	newSlice := make([]string, 0, len(m.cfg.Allowlist))
	for _, d := range m.cfg.Allowlist {
		if d != domain {
			newSlice = append(newSlice, d)
		}
	}
	m.cfg.Allowlist = newSlice
	m.syncAllowlistMap()
	savePath := filepath.Join(m.cfg.ConfigDir, "config.yaml")
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()
	return config.Save(m.cfg, savePath)
}

func (m *Manager) ListAllowed() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return slice from config (it is the source of truth)
	dst := make([]string, len(m.cfg.Allowlist))
	copy(dst, m.cfg.Allowlist)
	return dst
}

func (m *Manager) InvalidateCache() error {
	return os.RemoveAll(m.cfg.CacheDir)
}
