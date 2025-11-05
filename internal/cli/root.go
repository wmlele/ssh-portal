package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"ssh-portal/internal/cli/receiver"
	"ssh-portal/internal/config"
	"ssh-portal/internal/log"
	"ssh-portal/internal/version"

	zone "github.com/lrstanley/bubblezone"
)

var (
	cfgFile  string
	logLevel string
	rootCmd  = &cobra.Command{
		Use:   "ssh-portal",
		Short: "A wormhole portal for SSH",
		Long: "A relay-based SSH connection system designed for scenarios where both the sender and receiver\n" +
			"are behind NAT or firewalls, making direct connections impossible. It enables secure SSH connections\n" +
			"through an intermediary relay server, allowing temporary remote access for remote support scenarios.\n" +
			"Supports TCP/IP port forwarding (local and remote) for development and troubleshooting use cases.\n\n" +
			"All command-line flags override values from configuration files.",
		Version: version.String(),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Load config (flags/env/config file/defaults)
			if err := config.Load(cfgFile); err != nil {
				return err
			}
			// Override with --log-level if set
			if cmd.Flags().Changed("log-level") {
				viper.Set("log.level", logLevel)
			}
			return log.Init(viper.GetString("log.level"))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no subcommand specified, default to receiver
			// Load receiver config and merge with flags
			cfg := receiver.LoadReceiverConfig()
			merged := receiver.MergeReceiverFlags(cmd, cfg, receiver.ReceiverFlags{
				RelayHost:   receiverRelayHost,
				RelayPort:   receiverRelayPort,
				Token:       receiverToken,
				Interactive: receiverInteractive,
				Session:     receiverSession,
				LogView:     receiverLogView,
			})

			return receiver.Run(merged.RelayHost, merged.RelayPort, merged.Interactive, merged.Session, merged.LogView, merged.Token)
		},
	}
)

func init() {

	zone.NewGlobal()

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.ssh-portal.yml)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug|info|warn|error)")
	_ = viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindEnv("log.level", "ssh-portal_LOG_LEVEL")

	// Add receiver flags to root command (since receiver is the default)
	rootCmd.Flags().StringVar(&receiverRelayHost, "relay", "", "Relay server host")
	rootCmd.Flags().IntVar(&receiverRelayPort, "relay-port", 0, "Relay server TCP port")
	rootCmd.Flags().BoolVar(&receiverInteractive, "interactive", true, "interactive mode")
	rootCmd.Flags().BoolVar(&receiverSession, "session", false, "enable session handling (PTY/shell/exec)")
	rootCmd.Flags().BoolVar(&receiverLogView, "logview", true, "show log panel in interactive mode")
	rootCmd.Flags().StringVar(&receiverToken, "token", "", "optional access token")

	// Add subcommands
	rootCmd.AddCommand(senderCmd)
	rootCmd.AddCommand(receiverCmd)
	rootCmd.AddCommand(relayCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
