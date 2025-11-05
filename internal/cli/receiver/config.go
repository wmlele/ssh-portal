package receiver

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ReceiverConfig represents the receiver configuration
type ReceiverConfig struct {
	Relay       string `yaml:"relay,omitempty"`
	RelayPort   int    `yaml:"relay-port,omitempty"`
	Token       string `yaml:"token,omitempty"`
	Interactive *bool  `yaml:"interactive,omitempty"`
	Session     *bool  `yaml:"session,omitempty"`
	LogView     *bool  `yaml:"logview,omitempty"`
}

// LoadReceiverConfig loads receiver configuration from viper
func LoadReceiverConfig() *ReceiverConfig {
	if !viper.IsSet("receiver") {
		return nil
	}
	var cfg ReceiverConfig
	if err := viper.UnmarshalKey("receiver", &cfg); err != nil {
		return nil
	}
	return &cfg
}

// MergeReceiverFlags merges config with CLI flags, returning the final values
// Flags override config values when explicitly set
type ReceiverFlags struct {
	RelayHost   string
	RelayPort   int
	Token       string
	Interactive bool
	Session     bool
	LogView     bool
}

func MergeReceiverFlags(cmd *cobra.Command, cfg *ReceiverConfig, flags ReceiverFlags) ReceiverFlags {
	result := ReceiverFlags{
		RelayHost:   "localhost",
		RelayPort:   4430,
		Token:       "",
		Interactive: true,
		Session:     false,
		LogView:     true,
	}

	// Apply config values as defaults
	if cfg != nil {
		if cfg.Relay != "" {
			result.RelayHost = cfg.Relay
		}
		if cfg.RelayPort > 0 {
			result.RelayPort = cfg.RelayPort
		}
		if cfg.Token != "" {
			result.Token = cfg.Token
		}
		if cfg.Interactive != nil {
			result.Interactive = *cfg.Interactive
		}
		if cfg.Session != nil {
			result.Session = *cfg.Session
		}
		if cfg.LogView != nil {
			result.LogView = *cfg.LogView
		}
	}

	// CLI flags override config
	if cmd.Flags().Changed("relay") && flags.RelayHost != "" {
		result.RelayHost = flags.RelayHost
	}
	if cmd.Flags().Changed("relay-port") && flags.RelayPort > 0 {
		result.RelayPort = flags.RelayPort
	}
	if cmd.Flags().Changed("token") && flags.Token != "" {
		result.Token = flags.Token
	}
	if cmd.Flags().Changed("interactive") {
		result.Interactive = flags.Interactive
	}
	if cmd.Flags().Changed("session") {
		result.Session = flags.Session
	}
	if cmd.Flags().Changed("logview") {
		result.LogView = flags.LogView
	}

	return result
}
