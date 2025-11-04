package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"ssh-portal/internal/cli/sender"
)

var (
	senderCode             string
	senderRelayHost        string
	senderRelayPort        int
	senderInteractive      bool
	senderKeepaliveTimeout string
	senderIdentity         string
	senderProfile          string
	senderMenu             bool
)

var senderCmd = &cobra.Command{
	Use:   "sender",
	Short: "Sender command",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load top-level sender config from viper
		var topLevel *sender.SenderConfig
		if viper.IsSet("sender") {
			var cfg sender.SenderConfig
			if err := viper.UnmarshalKey("sender", &cfg); err == nil {
				topLevel = &cfg
			}
		}

		// Show menu if enabled and profiles exist
		if senderMenu && topLevel != nil && len(topLevel.Profiles) > 0 && senderProfile == "" {
			needsCode := senderCode == ""
			result, err := sender.SelectProfile(topLevel.Profiles, needsCode)
			if err != nil {
				return fmt.Errorf("profile selection failed: %w", err)
			}
			if result != nil {
				if result.Profile != "" {
					senderProfile = result.Profile
				}
				if result.Code != "" {
					senderCode = result.Code
				}
			}
		}

		// Find and merge profile if specified
		var profile *sender.Profile
		if senderProfile != "" {
			if topLevel == nil {
				return fmt.Errorf("no sender configuration found in config file")
			}
			// Find profile by name
			found := false
			for i := range topLevel.Profiles {
				if topLevel.Profiles[i].Name == senderProfile {
					profile = &topLevel.Profiles[i]
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("profile '%s' not found in configuration", senderProfile)
			}
		}

		// Merge top-level + profile
		mergedCfg := sender.MergeConfig(topLevel, profile)

		// Apply CLI flags (they override config)
		relayHost := mergedCfg.Relay
		if cmd.Flags().Changed("relay") && senderRelayHost != "" {
			relayHost = senderRelayHost
		}

		relayPort := mergedCfg.RelayPort
		if cmd.Flags().Changed("relay-port") && senderRelayPort > 0 {
			relayPort = senderRelayPort
		}

		interactive := mergedCfg.Interactive
		if cmd.Flags().Changed("interactive") {
			interactive = senderInteractive
		}

		keepaliveTimeout := mergedCfg.Keepalive
		if cmd.Flags().Changed("keepalive") && senderKeepaliveTimeout != "" {
			var err error
			keepaliveTimeout, err = time.ParseDuration(senderKeepaliveTimeout)
			if err != nil {
				return fmt.Errorf("invalid keepalive timeout: %w", err)
			}
		}

		identity := mergedCfg.Identity
		if cmd.Flags().Changed("identity") && senderIdentity != "" {
			identity = senderIdentity
		}

		// Get code (required)
		code := senderCode
		if code == "" {
			code = viper.GetString("sender.code")
		}
		if code == "" {
			return fmt.Errorf("code is required (use --code flag or config)")
		}

		// Run sender with merged configuration
		return sender.RunWithConfig(relayHost, relayPort, code, interactive, keepaliveTimeout, identity, mergedCfg)
	},
}

func init() {
	senderCmd.Flags().StringVarP(&senderCode, "code", "c", "", "connection code")
	senderCmd.Flags().StringVar(&senderRelayHost, "relay", "", "Relay server host (overrides config)")
	senderCmd.Flags().IntVar(&senderRelayPort, "relay-port", 0, "Relay server TCP port (overrides config)")
	senderCmd.Flags().BoolVar(&senderInteractive, "interactive", false, "interactive mode (overrides config)")
	senderCmd.Flags().StringVar(&senderKeepaliveTimeout, "keepalive", "", "keepalive timeout (e.g., 30s, 1m) (overrides config)")
	senderCmd.Flags().StringVar(&senderIdentity, "identity", "", "sender identity label to display at receiver (overrides config)")
	senderCmd.Flags().StringVar(&senderProfile, "profile", "", "profile name to use from config file")
	senderCmd.Flags().BoolVar(&senderMenu, "menu", true, "show profile selection menu if profiles exist")
	_ = viper.BindPFlag("sender.code", senderCmd.Flags().Lookup("code"))
	_ = viper.BindEnv("sender.code", "SSH_PORTAL_SENDER_CODE")
}
