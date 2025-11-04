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
		keepaliveTimeout, err := time.ParseDuration(senderKeepaliveTimeout)
		if err != nil {
			return fmt.Errorf("invalid keepalive timeout: %w", err)
		}
        return sender.Run(senderRelayHost, senderRelayPort, code, senderInteractive, keepaliveTimeout, senderIdentity)
	},
}

func init() {
	senderCmd.Flags().StringVarP(&senderCode, "code", "c", "", "connection code")
	senderCmd.Flags().StringVar(&senderRelayHost, "relay", "localhost", "Relay server host")
	senderCmd.Flags().IntVar(&senderRelayPort, "relay-port", 4430, "Relay server TCP port")
	senderCmd.Flags().BoolVar(&senderInteractive, "interactive", true, "interactive mode")
	senderCmd.Flags().StringVar(&senderKeepaliveTimeout, "keepalive", "30s", "keepalive timeout (e.g., 30s, 1m)")
    senderCmd.Flags().StringVar(&senderIdentity, "identity", "", "sender identity label to display at receiver")
	_ = viper.BindPFlag("sender.code", senderCmd.Flags().Lookup("code"))
	_ = viper.BindEnv("sender.code", "SSH_PORTAL_SENDER_CODE")
}
