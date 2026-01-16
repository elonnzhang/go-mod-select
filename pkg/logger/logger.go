package logger

import (
	"log/slog"
	"os"
	"path/filepath"
)

var Log *slog.Logger

// Init initializes the logger with file output
func Init(logPath string) error {
	// Create log directory if it doesn't exist
	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	// Open log file
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}

	// Create JSON handler for structured logging
	handler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	// Set global logger
	Log = slog.New(handler)
	slog.SetDefault(Log)

	return nil
}

// InitDefault initializes with default log path
func InitDefault() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(homeDir, ".go-mod-select", "app.log")
	return Init(logPath)
}

// Close closes the log file (if needed)
func Close() {
	// Currently no cleanup needed with slog
}
