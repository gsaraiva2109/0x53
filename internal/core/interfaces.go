package core

import (
	"time"

	"0x53/internal/config"
	"context"
)

// QueryEntry represents a single DNS query processed by the server.
type QueryEntry struct {
	Timestamp time.Time
	Domain    string
	Action    string        // "blocked", "allowed", "local", "error"
	Latency   time.Duration
}

// Engine is the main controller of the Sinkhole.
type Engine interface {
	Start(ctx context.Context) error
	Stop() error
	Reload() error
	Stats() (queries int, blocked int)

	AddLocalRecord(domain, ip string) error
	RemoveLocalRecord(domain string) error
	ListLocalRecords() map[string]string

	GetRecentQueries(count int) []QueryEntry
}

// BlocklistManager handles the lifecycle of blocklists.
type BlocklistManager interface {
	LoadBlocklists(ctx context.Context) error
	IsBlocked(domain string) bool
	Stats() int
	ListSources() []config.BlocklistSource
	ToggleSource(name string, enabled bool) error
	InvalidateCache() error

	AddAllowed(domain string) error
	RemoveAllowed(domain string) error
	ListAllowed() []string
}

// DNSConfigurator abstracts OS-specific network changes.
type DNSConfigurator interface {
	UnlockPort() error
	SetupDNS() error
	RestoreDNS() error
}

// Service defines the public API available to the TUI/CLI.
type Service interface {
	GetStats() (queries, blocked, activeRules int, err error)

	ListSources() ([]config.BlocklistSource, error)
	ToggleSource(name string, enabled bool) error
	Reload() error

	AddAllowed(domain string) error
	RemoveAllowed(domain string) error
	ListAllowed() ([]string, error)

	AddLocalRecord(domain, ip string) error
	RemoveLocalRecord(domain string) error
	ListLocalRecords() (map[string]string, error)

	GetRecentLogs(count int) ([]string, error)
	GetRecentQueries(count int) ([]QueryEntry, error)
}
