package core

import (
	"adblock/internal/config"
	"context"
)

// Engine is the main controller of the Sinkhole.
// It coordinates the UDP Listener, Blocklist Manager, and Upstream Forwarder.
type Engine interface {
	// Start begins the DNS listener and blocking services.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the server and restores system DNS.
	Stop() error
	// Reload refreshes blocklists and configuration without dropping connections.
	Reload() error
	// Stats returns total queries and blocked queries count.
	Stats() (queries int, blocked int)
}

// BlocklistManager handles the lifecycle of blocklists.
// It downloads, parses, and provides O(1) lookups.
type BlocklistManager interface {
	// LoadBlocklists fetches and parses lists from configured sources.
	LoadBlocklists(ctx context.Context) error
	// IsBlocked checks if a domain (or subdomain) is in the blocklist.
	// Returns true if blocked.
	IsBlocked(domain string) bool
	// Stats returns the total count of blocked domains currently loaded.
	Stats() int
	// ListSources returns the current list configuration.
	ListSources() []config.BlocklistSource
	// ToggleSource enables or disables a blocklist source.
	ToggleSource(name string, enabled bool) error
    // InvalidateCache clears the local disk cache.
    InvalidateCache() error
}

// DNSConfigurator abstracts OS-specific network changes.
// Implementations exist for Linux (systemd-resolved) and Windows (netsh).
type DNSConfigurator interface {
	// UnlockPort ensures port 53 is free (stops conflicting services).
	UnlockPort() error
	// SetupDNS points the system resolver to our Listener (usually 127.0.0.1).
	// This MUST backup the previous state before applying changes.
	SetupDNS() error
	// RestoreDNS reverts the system resolver to the pre-setup state.
	// This should be called on application exit or crash recovery.
	RestoreDNS() error
}

// Service defines the public API available to the TUI/CLI.
// It can be implemented by a local struct (Monolith) or an RPC Client (Daemon mode).
type Service interface {
	// GetStats returns combined metrics.
	GetStats() (queries, blocked, activeRules int, err error)
	
	// Blocklist Management
	ListSources() ([]config.BlocklistSource, error)
	ToggleSource(name string, enabled bool) error
	Reload() error
	
	// Logs
	// GetRecentLogs returns the last 'count' lines of logs.
	GetRecentLogs(count int) ([]string, error)
}
