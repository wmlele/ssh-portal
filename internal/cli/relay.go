package cli

import (
	"github.com/spf13/cobra"

	"ssh-portal/internal/cli/relay"
)

var (
	relayPort         int
	relayInteractive  bool
)

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Relay command",
	RunE: func(cmd *cobra.Command, args []string) error {
		return relay.Run(relayPort, relayInteractive)
	},
}

func init() {
	relayCmd.Flags().IntVar(&relayPort, "port", 4430, "TCP port for relay (HTTP will be on port+1)")
	relayCmd.Flags().BoolVar(&relayInteractive, "interactive", true, "interactive mode")
}

