package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UpstreamStrategy defines how we choose the upstream DNS resolver.
type UpstreamStrategy string

const (
	// UpstreamAuto detects the current system DNS before overwriting it.
	UpstreamAuto UpstreamStrategy = "auto"
	// UpstreamCloudflare uses 1.1.1.1.
	UpstreamCloudflare UpstreamStrategy = "cloudflare"
	// UpstreamGoogle uses 8.8.8.8.
	UpstreamGoogle UpstreamStrategy = "google"
	// UpstreamCustom uses the CustomUpstream field.
	UpstreamCustom UpstreamStrategy = "custom"
)

// Config holds the runtime configuration for the application.
type Config struct {
	// Network Configuration
	BindPort int    `yaml:"bind_port"`
	BindIP   string `yaml:"bind_ip"`

	// Upstream Configuration
	Upstream       UpstreamStrategy `yaml:"upstream_strategy"`
	CustomUpstream string           `yaml:"custom_upstream"` // "IP:Port"

	// Persistence Paths
	ConfigDir string `yaml:"config_dir"`
	CacheDir  string `yaml:"cache_dir"`
	LogPath   string `yaml:"log_path"`

	// Feature Flags
	EnableIPv6    bool `yaml:"enable_ipv6"`
	RestoreOnExit bool `yaml:"restore_on_exit"`

	// Blocklists
	Blocklists []BlocklistSource `yaml:"blocklists"`
}

type BlocklistSource struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Format  string `yaml:"format"` // hosts, abp, wild
	Enabled bool   `yaml:"enabled"`
}

// Default returns a safe default configuration.
func Default() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	// Default Config Paths:
	// 1. /etc/0x53/config.yaml (Global) - Handled by Load logic if found
	// 2. ~/.config/0x53/config.yaml (User)
	
	return &Config{
		BindPort: 53,
		BindIP:   "0.0.0.0",
		Upstream: UpstreamGoogle, // Default to Google for stability

		ConfigDir: filepath.Join(home, ".config", "0x53"),
		CacheDir:  filepath.Join(home, ".cache", "0x53"),
		LogPath:   "/var/log/0x53.log", // Default for daemon

		EnableIPv6:    true,
		RestoreOnExit: true,

		Blocklists: []BlocklistSource{
			{Name: "Abuse.ch ThreatFox", URL: "https://threatfox.abuse.ch/downloads/hostfile/", Format: "hosts", Enabled: true},
			{Name: "AdAway", URL: "https://adaway.org/hosts.txt", Format: "hosts", Enabled: true},
			{Name: "AdGuard DNS", URL: "https://v.firebog.net/hosts/AdguardDNS.txt", Format: "hosts", Enabled: true},
			{Name: "OISD Ads", URL: "https://small.oisd.nl/domainswild", Format: "wild", Enabled: true},
			{Name: "EasyList", URL: "https://v.firebog.net/hosts/Easylist.txt", Format: "hosts", Enabled: true},
			{Name: "EasyPrivacy", URL: "https://v.firebog.net/hosts/Easyprivacy.txt", Format: "hosts", Enabled: true},
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
