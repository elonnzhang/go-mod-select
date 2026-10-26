package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Log is safe to use before Init: it starts as a discard logger so library
// code (and tests that never call Init) cannot nil-panic on a log call.
var Log = slog.New(slog.NewJSONHandler(io.Discard, nil))

// maxLogBytes is how large the log may be when the program starts before it is
// moved aside. The file is opened for append at DEBUG level on every run and
// holds each go command's stderr, so without a cap it only ever grows.
const maxLogBytes = 5 << 20

// rotate keeps one previous generation (path + ".1"), replacing any older one.
// Best effort: if the rename fails the log simply keeps growing.
func rotate(path string) {
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogBytes {
		os.Rename(path, path+".1")
	}
}

// Init initializes the logger with file output
func Init(logPath string) error {
	// Create log directory if it doesn't exist
	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	rotate(logPath)

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
