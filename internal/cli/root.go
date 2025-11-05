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
		Use:     "ssh-portal",
		Short:   "A versatile console application",
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
			// Read flags from root command and pass to receiver
			relayHost, _ := cmd.Flags().GetString("relay")
			relayPort, _ := cmd.Flags().GetInt("relay-port")
			interactive, _ := cmd.Flags().GetBool("interactive")
			session, _ := cmd.Flags().GetBool("session")
			return receiver.Run(relayHost, relayPort, interactive, session)
		},
	}
)

func init() {

	zone.NewGlobal()

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./configs/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug|info|warn|error)")
	_ = viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	_ = viper.BindEnv("log.level", "ssh-portal_LOG_LEVEL")

	// Add receiver flags to root command (since receiver is the default)
	rootCmd.Flags().StringVar(&receiverRelayHost, "relay", "localhost", "Relay server host")
	rootCmd.Flags().IntVar(&receiverRelayPort, "relay-port", 4430, "Relay server TCP port (HTTP will be on port+1)")
	rootCmd.Flags().BoolVar(&receiverInteractive, "interactive", true, "interactive mode")
	rootCmd.Flags().BoolVar(&receiverSession, "session", false, "enable session handling (PTY/shell/exec)")

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
