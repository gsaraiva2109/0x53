package blocklist

import (
	"context"
	"strings"
	"sync"

	"0x53/internal/config"
)

// MockManager is a simple thread-safe map-based blocklist for testing.
type MockManager struct {
	blockedDomains map[string]struct{}
	mu             sync.RWMutex
}

func NewMockManager() *MockManager {
	return &MockManager{
		blockedDomains: make(map[string]struct{}),
	}
}

func (m *MockManager) LoadBlocklists(ctx context.Context) error {
	// No-op for mock
	return nil
}

func (m *MockManager) IsBlocked(domain string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Normalize (lowercase)
	domain = strings.ToLower(domain)
	_, exists := m.blockedDomains[domain]
	return exists
}

func (m *MockManager) Add(domain string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockedDomains[strings.ToLower(domain)] = struct{}{}
}

func (m *MockManager) Stats() int {
	return len(m.blockedDomains)
}

func (m *MockManager) InvalidateCache() error {
	return nil
}

func (m *MockManager) ListSources() []config.BlocklistSource {
	return []config.BlocklistSource{}
}

func (m *MockManager) ToggleSource(name string, enabled bool) error {
	return nil
}

func (m *MockManager) AddAllowed(domain string) error {
	return nil
}

func (m *MockManager) RemoveAllowed(domain string) error {
	return nil
}

func (m *MockManager) ListAllowed() []string {
	return []string{}
}
