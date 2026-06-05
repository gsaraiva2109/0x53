package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// UpstreamStrategy defines how we choose the upstream DNS resolver.
type UpstreamStrategy string

const (
	// UpstreamAuto detects the current system DNS before overwriting it.
	UpstreamAuto UpstreamStrategy = "auto"
	// UpstreamCloudflare uses 1.1.1.1 (plain UDP).
	UpstreamCloudflare UpstreamStrategy = "cloudflare"
	// UpstreamCloudflareDoT uses 1.1.1.1:853 over TLS.
	UpstreamCloudflareDoT UpstreamStrategy = "cloudflare-dot"
	// UpstreamGoogle uses 8.8.8.8 (plain UDP).
	UpstreamGoogle UpstreamStrategy = "google"
	// UpstreamGoogleDoT uses 8.8.8.8:853 over TLS.
	UpstreamGoogleDoT UpstreamStrategy = "google-dot"
	// UpstreamCustom uses the CustomUpstream field.
	UpstreamCustom UpstreamStrategy = "custom"
)

// BlockingMode controls how blocked domains are answered.
type BlockingMode string

const (
	// BlockModeSinkhole responds with 0.0.0.0 / :: (default).
	BlockModeSinkhole BlockingMode = "sinkhole"
	// BlockModeNXDOMAIN responds with NXDOMAIN (non-existent domain).
	BlockModeNXDOMAIN BlockingMode = "nxdomain"
)

// Config holds the runtime configuration for the application.
type Config struct {
	// Network Configuration
	BindPort int    `yaml:"bind_port"`
	BindIP   string `yaml:"bind_ip"`

	// Local DNS Records
	LocalRecords map[string]string `yaml:"local_records"`

	// Allowlist
	Allowlist []string `yaml:"allowlist"`

	// Upstream Configuration
	Upstream       UpstreamStrategy `yaml:"upstream_strategy"`
	CustomUpstream string           `yaml:"custom_upstream"` // "IP:Port"

	// Blocking Configuration
	BlockingMode BlockingMode `yaml:"blocking_mode"`

	// Persistence Paths
	ConfigDir string `yaml:"config_dir"`
	CacheDir  string `yaml:"cache_dir"`
	LogPath   string `yaml:"log_path"`

	// Cache TTL (global default, overridable per-source).
	CacheTTL time.Duration `yaml:"cache_ttl"`

	// Feature Flags
	EnableIPv6    bool `yaml:"enable_ipv6"`
	RestoreOnExit bool `yaml:"restore_on_exit"`
	MetricsPort   int  `yaml:"metrics_port"` // 0 = disabled

	// Blocklists
	Blocklists []BlocklistSource `yaml:"blocklists"`
}

type BlocklistSource struct {
	Name     string        `yaml:"name"`
	URL      string        `yaml:"url"`
	Format   string        `yaml:"format"` // "hosts", "wild", or "auto"
	Enabled  bool          `yaml:"enabled"`
	CacheTTL time.Duration `yaml:"cache_ttl,omitempty"` // Per-source override, 0 = use global.
}

// Default returns a safe default configuration.
func Default() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	return &Config{
		BindPort: 53,
		BindIP:   "0.0.0.0",
		Upstream: UpstreamCloudflareDoT, // Encrypted by default.

		BlockingMode: BlockModeSinkhole, // 0.0.0.0 responses, safe default.

		CacheTTL: 24 * time.Hour, // Default: re-download blocklists daily.

		ConfigDir: filepath.Join(home, ".config", "0x53"),
		CacheDir:  filepath.Join(home, ".cache", "0x53"),
		LogPath:   "/var/log/0x53.log",

		EnableIPv6:    true,
		RestoreOnExit: true,

		Blocklists: []BlocklistSource{
			{Name: "Abuse.ch ThreatFox", URL: "https://threatfox.abuse.ch/downloads/hostfile/", Format: "auto", Enabled: true},
			{Name: "AdAway", URL: "https://adaway.org/hosts.txt", Format: "auto", Enabled: true},
			{Name: "AdGuard DNS", URL: "https://v.firebog.net/hosts/AdguardDNS.txt", Format: "auto", Enabled: true},
			{Name: "OISD Ads", URL: "https://small.oisd.nl/domainswild", Format: "auto", Enabled: true},
			{Name: "EasyList", URL: "https://v.firebog.net/hosts/Easylist.txt", Format: "auto", Enabled: true},
			{Name: "EasyPrivacy", URL: "https://v.firebog.net/hosts/Easyprivacy.txt", Format: "auto", Enabled: true},
		},
	}
}

// Load attempts to load the configuration from standard locations.
// It prioritizes:
// 1. Provided path (if not empty)
// 2. /etc/0x53/config.yaml
// 3. ~/.config/0x53/config.yaml
// If no file is found, it returns Default() and no error.
func Load(explicitPath string) (*Config, error) {
	paths := []string{}
	if explicitPath != "" {
		paths = append(paths, explicitPath)
	}

	// Add System and User defaults
	paths = append(paths, "/etc/0x53/config.yaml")

	home, err := os.UserHomeDir()
	if err == nil {
		paths = append(paths, filepath.Join(home, ".config", "0x53", "config.yaml"))
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("Loading config from: %s\n", p)
			return loadFromFile(p)
		}
	}

	fmt.Println("No config file found. Using defaults.")
	return Default(), nil
}

func loadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := Default() // Start with defaults to fill missing fields
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %s: %w", path, err)
	}

	// Override ConfigDir to match the directory of the loaded file,
	// so future saves write back to the same location.
	cfg.ConfigDir = filepath.Dir(path)

	return cfg, nil
}

// Save attempts to save the current configuration to the specified path.
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
