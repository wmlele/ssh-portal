package log

import (
	"log/slog"
	"os"
	"strings"
)

var Logger *slog.Logger

func Init(level string) error {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	Logger = slog.New(h)
	slog.SetDefault(Logger)
	return nil
}
