package config

import (
	"fmt"
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
		viper.AddConfigPath(".")
		viper.AddConfigPath("./configs")
		viper.SetConfigName("config")
	}

	// Defaults
	viper.SetDefault("log.level", "info")
	viper.SetDefault("hello.message", "world")

	// Optional config file
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config:", filepath.Base(viper.ConfigFileUsed()))
	}
	return nil
}
