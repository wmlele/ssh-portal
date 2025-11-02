package cli

import (
	"github.com/spf13/cobra"

	"ssh-portal/internal/cli/relay"
)

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Relay command",
	RunE: func(cmd *cobra.Command, args []string) error {
		return relay.Run()
	},
}

