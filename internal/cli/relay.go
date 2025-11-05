package cli

import (
	"github.com/spf13/cobra"

	"ssh-portal/internal/cli/relay"
)

var (
	relayPort          int
	relayInteractive   bool
	relayReceiverToken string
	relaySenderToken   string
)

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Relay command",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load relay config and merge with flags
		cfg := relay.LoadRelayConfig()
		merged := relay.MergeRelayFlags(cmd, cfg, relay.RelayFlags{
			Port:          relayPort,
			Interactive:   relayInteractive,
			ReceiverToken: relayReceiverToken,
			SenderToken:   relaySenderToken,
		})

		return relay.Run(merged.Port, merged.Interactive, merged.ReceiverToken, merged.SenderToken)
	},
}

func init() {
	relayCmd.Flags().IntVar(&relayPort, "port", 0, "TCP port for relay server")
	relayCmd.Flags().BoolVar(&relayInteractive, "interactive", true, "interactive mode")
	relayCmd.Flags().StringVar(&relayReceiverToken, "receiver-token", "", "optional token that receivers must provide in hello messages")
	relayCmd.Flags().StringVar(&relaySenderToken, "sender-token", "", "optional token that senders must provide in hello messages")
}
