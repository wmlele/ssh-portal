package relay

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// RelayConfig represents the relay configuration
type RelayConfig struct {
	Port          int    `yaml:"port,omitempty" mapstructure:"port,omitempty"`
	Interactive   *bool  `yaml:"interactive,omitempty" mapstructure:"interactive,omitempty"`
	ReceiverToken string `yaml:"receiver-token,omitempty" mapstructure:"receiver-token,omitempty"`
	SenderToken   string `yaml:"sender-token,omitempty" mapstructure:"sender-token,omitempty"`
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
	Port          int
	Interactive   bool
	ReceiverToken string
	SenderToken   string
}

func MergeRelayFlags(cmd *cobra.Command, cfg *RelayConfig, flags RelayFlags) RelayFlags {
	result := RelayFlags{
		Port:          4430,
		Interactive:   true,
		ReceiverToken: "",
		SenderToken:   "",
	}

	// Apply config values as defaults
	if cfg != nil {
		if cfg.Port > 0 {
			result.Port = cfg.Port
		}
		if cfg.Interactive != nil {
			result.Interactive = *cfg.Interactive
		}
		if cfg.ReceiverToken != "" {
			result.ReceiverToken = cfg.ReceiverToken
		}
		if cfg.SenderToken != "" {
			result.SenderToken = cfg.SenderToken
		}
	}

	// CLI flags override config
	if cmd.Flags().Changed("port") && flags.Port > 0 {
		result.Port = flags.Port
	}
	if cmd.Flags().Changed("interactive") {
		result.Interactive = flags.Interactive
	}
	if cmd.Flags().Changed("receiver-token") {
		result.ReceiverToken = flags.ReceiverToken
	}
	if cmd.Flags().Changed("sender-token") {
		result.SenderToken = flags.SenderToken
	}

	return result
}
