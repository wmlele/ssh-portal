package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"ssh-portal/internal/cli/sender"
)

var (
	senderCode        string
	senderRelayHost   string
	senderRelayPort   int
	senderInteractive bool
)

var senderCmd = &cobra.Command{
	Use:   "sender",
	Short: "Sender command",
	RunE: func(cmd *cobra.Command, args []string) error {
		code := senderCode
		if code == "" {
			code = viper.GetString("sender.code")
		}
		if code == "" {
			return fmt.Errorf("code is required (use --code flag or config)")
		}
		return sender.Run(senderRelayHost, senderRelayPort, code, senderInteractive)
	},
}

func init() {
	senderCmd.Flags().StringVarP(&senderCode, "code", "c", "", "connection code")
	senderCmd.Flags().StringVar(&senderRelayHost, "relay", "localhost", "Relay server host")
	senderCmd.Flags().IntVar(&senderRelayPort, "relay-port", 4430, "Relay server TCP port")
	senderCmd.Flags().BoolVar(&senderInteractive, "interactive", true, "interactive mode")
	_ = viper.BindPFlag("sender.code", senderCmd.Flags().Lookup("code"))
	_ = viper.BindEnv("sender.code", "SSH_PORTAL_SENDER_CODE")
}
