package config

import (
	"os"
	"path/filepath"
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

	return &Config{
		BindPort: 53,
		BindIP:   "0.0.0.0",
		Upstream: UpstreamAuto,

		ConfigDir: filepath.Join(home, ".config", "go-sinkhole"),
		CacheDir:  filepath.Join(home, ".cache", "go-sinkhole"),
		LogPath:   filepath.Join(home, ".config", "go-sinkhole", "server.log"),

		EnableIPv6:    true,
		RestoreOnExit: true,

		Blocklists: []BlocklistSource{
			{Name: "Abuse.ch ThreatFox", URL: "https://threatfox.abuse.ch/downloads/hostfile/", Format: "hosts", Enabled: true},
			{Name: "AdAway", URL: "https://adaway.org/hosts.txt", Format: "hosts", Enabled: true},
			{Name: "AdGuard DNS", URL: "https://v.firebog.net/hosts/AdguardDNS.txt", Format: "hosts", Enabled: true},
			{Name: "OISD Ads", URL: "https://small.oisd.nl/domainswild", Format: "wild", Enabled: true},
			{Name: "OISD Big", URL: "https://big.oisd.nl/domainswild", Format: "wild", Enabled: false},
			{Name: "OISD NSFW", URL: "https://nsfw.oisd.nl/domainswild", Format: "wild", Enabled: false},
			{Name: "Blocklist.site", URL: "https://github.com/blocklistproject/Lists", Format: "hosts", Enabled: false}, // Repo link, needs specific file
			{Name: "EasyList", URL: "https://v.firebog.net/hosts/Easylist.txt", Format: "hosts", Enabled: true},       // Converted to hosts by firebog logic usually
			{Name: "EasyPrivacy", URL: "https://v.firebog.net/hosts/Easyprivacy.txt", Format: "hosts", Enabled: true}, // Converted to hosts by firebog logic usually
			{Name: "YoYo List", URL: "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=hosts&showintro=0&mimetype=plaintext", Format: "hosts", Enabled: true},
			{Name: "HaGeZi Multi", URL: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/hosts/pro.txt", Format: "hosts", Enabled: false},
		},
	}
}
