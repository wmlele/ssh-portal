package relay

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// RelayConfig represents the relay configuration
type RelayConfig struct {
	Port        int   `yaml:"port,omitempty"`
	Interactive *bool `yaml:"interactive,omitempty"`
}

// LoadRelayConfig loads relay configuration from viper
func LoadRelayConfig() *RelayConfig {
	if !viper.IsSet("relay") {
		return nil
	}
	var cfg RelayConfig
	if err := viper.UnmarshalKey("relay", &cfg); err != nil {
		return nil
	}
	return &cfg
}

// MergeRelayFlags merges config with CLI flags, returning the final values
// Flags override config values when explicitly set
type RelayFlags struct {
	Port        int
	Interactive bool
}

func MergeRelayFlags(cmd *cobra.Command, cfg *RelayConfig, flags RelayFlags) RelayFlags {
	result := RelayFlags{
		Port:        4430,
		Interactive: true,
	}

	// Apply config values as defaults
	if cfg != nil {
		if cfg.Port > 0 {
			result.Port = cfg.Port
		}
		if cfg.Interactive != nil {
			result.Interactive = *cfg.Interactive
		}
	}

	// CLI flags override config
	if cmd.Flags().Changed("port") && flags.Port > 0 {
		result.Port = flags.Port
	}
	if cmd.Flags().Changed("interactive") {
		result.Interactive = flags.Interactive
	}

	return result
}
