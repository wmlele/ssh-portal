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
	receiverToken       string
)

var receiverCmd = &cobra.Command{
	Use:   "receiver",
	Short: "Receiver command",
	RunE: func(cmd *cobra.Command, args []string) error {
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

func init() {
	receiverCmd.Flags().StringVar(&receiverRelayHost, "relay", "", "Relay server host")
	receiverCmd.Flags().IntVar(&receiverRelayPort, "relay-port", 0, "Relay server TCP port")
	receiverCmd.Flags().BoolVar(&receiverInteractive, "interactive", true, "interactive mode")
	receiverCmd.Flags().BoolVar(&receiverSession, "session", false, "enable session handling (PTY/shell/exec)")
	receiverCmd.Flags().BoolVar(&receiverLogView, "logview", true, "show log panel in interactive mode")
	receiverCmd.Flags().StringVar(&receiverToken, "token", "", "optional token to send in hello message")
}
