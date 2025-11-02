package cli

import (
	"github.com/spf13/cobra"

	"ssh-portal/internal/cli/receiver"
)

var receiverCmd = &cobra.Command{
	Use:   "receiver",
	Short: "Receiver command",
	RunE: func(cmd *cobra.Command, args []string) error {
		return receiver.Run()
	},
}
