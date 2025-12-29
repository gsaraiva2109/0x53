package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"0x53/internal/config"
	"0x53/internal/core"
)

// AppService implements core.Service.
// It wraps the core components and exposes high-level operations.
type AppService struct {
	engine   core.Engine
	manager  core.BlocklistManager
	
	// Log Storage (Ring Buffer)
	logLines []string
	logMu    sync.RWMutex
	logLimit int
}

// NewAppService creates a new service instance.
func NewAppService(eng core.Engine, mgr core.BlocklistManager) *AppService {
	svc := &AppService{
		engine:   eng,
		manager:  mgr,
		logLines: make([]string, 0, 1000),
		logLimit: 200, // Keep last 200 lines in memory for TUI
	}
	return svc
}

// Log is a callback that can be passed to Engine and Manager.
func (s *AppService) Log(msg string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	
	// Add timestamp
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s", ts, msg)
	
	s.logLines = append(s.logLines, line)
	
	// Prune
	if len(s.logLines) > s.logLimit {
		s.logLines = s.logLines[len(s.logLines)-s.logLimit:]
	}
}

// GetStats returns combined metrics.
func (s *AppService) GetStats() (int, int, int, error) {
	q, b := s.engine.Stats()
	r := s.manager.Stats()
	return q, b, r, nil
}

// Blocklist Management
func (s *AppService) ListSources() ([]config.BlocklistSource, error) {
	return s.manager.ListSources(), nil
}

func (s *AppService) ToggleSource(name string, enabled bool) error {
	s.Log(fmt.Sprintf("Toggling source %s to %v", name, enabled))
	return s.manager.ToggleSource(name, enabled)
}

func (s *AppService) AddAllowed(domain string) error {
	s.Log(fmt.Sprintf("Allowing domain: %s", domain))
	return s.manager.AddAllowed(domain)
}

func (s *AppService) RemoveAllowed(domain string) error {
	s.Log(fmt.Sprintf("Removing allowed domain: %s", domain))
	return s.manager.RemoveAllowed(domain)
}

func (s *AppService) ListAllowed() ([]string, error) {
	return s.manager.ListAllowed(), nil
}

func (s *AppService) Reload() error {
	s.Log("Reloading configuration and blocklists...")
	// TODO: Reload config from disk
	if err := s.manager.LoadBlocklists(context.Background()); err != nil {
		s.Log(fmt.Sprintf("Reload failed: %v", err))
		return err
	}
	s.Log("Reload complete.")
	return nil
}

// Logs
func (s *AppService) GetRecentLogs(count int) ([]string, error) {
	s.logMu.RLock()
	defer s.logMu.RUnlock()
	
	if count <= 0 || count > len(s.logLines) {
		count = len(s.logLines)
	}
	
	// Return a copy to avoid race conditions
	dst := make([]string, count)
	copy(dst, s.logLines[len(s.logLines)-count:])
	return dst, nil
}
