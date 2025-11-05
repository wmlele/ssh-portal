package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

func Load(cfgFile string) error {
	viper.SetConfigType("yaml")
	viper.SetEnvPrefix("ssh-portal")
	viper.AutomaticEnv()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Check for .ssh-portal.yml in user's home directory
		if homeDir, err := os.UserHomeDir(); err == nil {
			homeConfigPath := filepath.Join(homeDir, ".ssh-portal.yml")
			if _, err := os.Stat(homeConfigPath); err == nil {
				// .ssh-portal.yml exists in home directory, read it
				viper.SetConfigFile(homeConfigPath)
			}
		}
	}

	// Defaults
	viper.SetDefault("log.level", "info")

	// Read config file if one was specified
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config:", filepath.Base(viper.ConfigFileUsed()))
	}
	return nil
}
