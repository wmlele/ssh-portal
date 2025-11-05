package cli

import (
	"github.com/spf13/cobra"

	"ssh-portal/internal/cli/receiver"
)

var (
	receiverRelayHost   string
	receiverRelayPort   int
	receiverInteractive bool
	receiverSession     bool
	receiverLogView     bool
)

var receiverCmd = &cobra.Command{
	Use:   "receiver",
	Short: "Receiver command",
	RunE: func(cmd *cobra.Command, args []string) error {
		return receiver.Run(receiverRelayHost, receiverRelayPort, receiverInteractive, receiverSession, receiverLogView)
	},
}

func init() {
	receiverCmd.Flags().StringVar(&receiverRelayHost, "relay", "localhost", "Relay server host")
	receiverCmd.Flags().IntVar(&receiverRelayPort, "relay-port", 4430, "Relay server TCP port (HTTP will be on port+1)")
	receiverCmd.Flags().BoolVar(&receiverInteractive, "interactive", true, "interactive mode")
	receiverCmd.Flags().BoolVar(&receiverSession, "session", false, "enable session handling (PTY/shell/exec)")
	receiverCmd.Flags().BoolVar(&receiverLogView, "logview", true, "show log panel in interactive mode (default: true)")
}
