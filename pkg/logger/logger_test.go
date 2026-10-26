package logger

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func restoreLog(t *testing.T) {
	t.Helper()
	prev := Log
	t.Cleanup(func() { Log = prev; slog.SetDefault(prev) })
}

func TestInitMovesAnOversizedLogAside(t *testing.T) {
	restoreLog(t)
	path := filepath.Join(t.TempDir(), "sub", "app.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("x"), maxLogBytes)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("older generation"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	Log.Info("hello")

	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != int64(len(big)) {
		t.Errorf("the oversized log should become app.log.1 (replacing the older one): %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 || info.Size() > 4096 {
		t.Errorf("a fresh app.log should hold only the new entry: %v", err)
	}
}

func TestInitKeepsASmallLogAndAppends(t *testing.T) {
	restoreLog(t)
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("previous run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	Log.Info("hello")

	got, _ := os.ReadFile(path)
	if !bytes.HasPrefix(got, []byte("previous run\n")) {
		t.Errorf("a small log must be appended to, not replaced:\n%s", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Errorf("no rotation expected for a small log (%v)", err)
	}
}
