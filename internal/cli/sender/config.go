package sender

import (
	"time"
)

// PortForwardConfig represents a single port forward configuration
type PortForwardConfig struct {
	Bind   string `yaml:"bind"`
	Target string `yaml:"target"`
}

// Profile represents a sender profile configuration
type Profile struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description,omitempty"`
	Relay       string              `yaml:"relay,omitempty"`
	RelayPort   int                 `yaml:"relay-port,omitempty"`
	Interactive *bool               `yaml:"interactive,omitempty"`
	Keepalive   string              `yaml:"keepalive,omitempty"`
	Identity    string              `yaml:"identity,omitempty"`
	Token       string              `yaml:"token,omitempty"`
	Local       []PortForwardConfig `yaml:"local,omitempty"`
	Remote      []PortForwardConfig `yaml:"remote,omitempty"`
}

// SenderConfig represents the top-level sender configuration
type SenderConfig struct {
	Relay       string    `yaml:"relay,omitempty"`
	RelayPort   int       `yaml:"relay-port,omitempty"`
	Interactive *bool     `yaml:"interactive,omitempty"`
	Keepalive   string    `yaml:"keepalive,omitempty"`
	Identity    string    `yaml:"identity,omitempty"`
	Token       string    `yaml:"token,omitempty"`
	Profiles    []Profile `yaml:"profiles,omitempty"`
}

// Config represents the merged configuration (top-level + profile)
type Config struct {
	Relay       string
	RelayPort   int
	Interactive bool
	Keepalive   time.Duration
	Identity    string
	Token       string
	Local       []PortForwardConfig
	Remote      []PortForwardConfig
}

// MergeConfig merges top-level config with a profile, returning a merged Config
func MergeConfig(topLevel *SenderConfig, profile *Profile) *Config {
	cfg := &Config{
		Relay:       "localhost",
		RelayPort:   4430,
		Interactive: true,
		Keepalive:   30 * time.Second,
		Identity:    "",
		Token:       "",
		Local:       []PortForwardConfig{},
		Remote:      []PortForwardConfig{},
	}

	// Apply top-level config
	if topLevel != nil {
		if topLevel.Relay != "" {
			cfg.Relay = topLevel.Relay
		}
		if topLevel.RelayPort > 0 {
			cfg.RelayPort = topLevel.RelayPort
		}
		if topLevel.Interactive != nil {
			cfg.Interactive = *topLevel.Interactive
		}
		if topLevel.Keepalive != "" {
			if d, err := time.ParseDuration(topLevel.Keepalive); err == nil {
				cfg.Keepalive = d
			}
		}
		if topLevel.Identity != "" {
			cfg.Identity = topLevel.Identity
		}
		if topLevel.Token != "" {
			cfg.Token = topLevel.Token
		}
	}

	// Apply profile config (overrides top-level)
	if profile != nil {
		if profile.Relay != "" {
			cfg.Relay = profile.Relay
		}
		if profile.RelayPort > 0 {
			cfg.RelayPort = profile.RelayPort
		}
		if profile.Interactive != nil {
			cfg.Interactive = *profile.Interactive
		}
		if profile.Keepalive != "" {
			if d, err := time.ParseDuration(profile.Keepalive); err == nil {
				cfg.Keepalive = d
			}
		}
		if profile.Identity != "" {
			cfg.Identity = profile.Identity
		}
		if profile.Token != "" {
			cfg.Token = profile.Token
		}
		if len(profile.Local) > 0 {
			cfg.Local = profile.Local
		}
		if len(profile.Remote) > 0 {
			cfg.Remote = profile.Remote
		}
	}

	return cfg
}
