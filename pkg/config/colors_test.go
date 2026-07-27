package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadColorConfigKeepsDefaultsForMissingKeys guards the bug where a partial
// theme file unmarshalled onto a zero struct, leaving every unspecified color as
// "" — which lipgloss renders as no styling, blanking most of the UI.
func TestLoadColorConfigKeepsDefaultsForMissingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "colors.json")

	// A realistic partial theme: the user only wanted a different cursor.
	if err := os.WriteFile(path, []byte(`{"cursor": "#123456"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadColorConfig(path)
	if err != nil {
		t.Fatalf("LoadColorConfig: %v", err)
	}

	if got.Cursor != "#123456" {
		t.Errorf("Cursor = %q, want the override #123456", got.Cursor)
	}

	def := DefaultColorConfig()
	if got.Title != def.Title {
		t.Errorf("Title = %q, want default %q", got.Title, def.Title)
	}
	if got.Patch != def.Patch {
		t.Errorf("Patch = %q, want default %q", got.Patch, def.Patch)
	}
	if got.Log == "" {
		t.Error("Log is empty; an unspecified color must not blank out")
	}
}

func TestLoadColorConfigMissingFileReturnsDefaults(t *testing.T) {
	got, err := LoadColorConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing file should not error, got %v", err)
	}
	if got.Title != DefaultColorConfig().Title {
		t.Errorf("Title = %q, want the default", got.Title)
	}
}

func TestLoadColorConfigRejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "colors.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadColorConfig(path); err == nil {
		t.Error("malformed JSON should surface an error, not be silently ignored")
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "colors.json")

	want := DefaultColorConfig()
	want.Major = "#abcdef"
	if err := SaveColorConfig(want, path); err != nil {
		t.Fatalf("SaveColorConfig: %v", err)
	}

	got, err := LoadColorConfig(path)
	if err != nil {
		t.Fatalf("LoadColorConfig: %v", err)
	}
	if got.Major != "#abcdef" {
		t.Errorf("Major = %q, want #abcdef", got.Major)
	}
}

// TestDefaultConfigIsFullyPopulated catches a new field being added to
// ColorConfig without a default, which would render as unstyled text.
func TestDefaultConfigIsFullyPopulated(t *testing.T) {
	data, err := json.Marshal(DefaultColorConfig())
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}

	for name, value := range fields {
		if value == "" {
			t.Errorf("default color %q is empty", name)
		}
	}
}
