package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
)

// ColorConfig holds all color configurations for the TUI
type ColorConfig struct {
	// General UI colors
	Title         string `json:"title"`
	Header        string `json:"header"`
	Border        string `json:"border"`
	TabActive     string `json:"tab_active"`
	TabInactive   string `json:"tab_inactive"`
	TabBackground string `json:"tab_background"`
	Help          string `json:"help"`

	// Version update type colors
	Patch      string `json:"patch"`
	Minor      string `json:"minor"`
	Major      string `json:"major"`
	Prerelease string `json:"prerelease"`

	// Status colors
	Selected string `json:"selected"`
	Cursor   string `json:"cursor"`

	// Popup colors
	PopupBorder string `json:"popup_border"`

	// Other UI colors
	Info    string `json:"info"`
	Success string `json:"success"`
	Log     string `json:"log"`
}

// DefaultColorConfig returns the built-in theme.
//
// Values are hex; an 8-digit form is accepted and its alpha byte ignored, since
// terminals have no alpha channel.
func DefaultColorConfig() *ColorConfig {
	return &ColorConfig{
		Title:         "#7d3de3ff", // violet
		Header:        "#7d3de3ff", // violet
		Border:        "#5f87af",   // steel blue
		TabActive:     "#7d3de3ff", // violet, on a light background
		TabInactive:   "#626262",   // mid gray
		TabBackground: "#bdd4dfff", // pale blue
		Help:          "#a4cf64ff", // olive green

		Patch:      "#00d700", // green
		Minor:      "#ffd700", // yellow
		Major:      "#00afff", // cyan
		Prerelease: "#ff0000", // red

		Selected: "#7d3de3ff", // violet
		Cursor:   "#7d3de3ff", // violet

		PopupBorder: "#ff5faf", // pink

		Info:    "#5fffd7",   // aqua
		Success: "#a4cf64ff", // olive green
		Log:     "#cfd4b7ff", // pale khaki
	}
}

// LoadColorConfig loads color configuration from file.
//
// Missing keys keep their default value: unmarshalling onto a zero struct would
// turn every unspecified field into "", which lipgloss renders as no styling at
// all. That makes a partial theme file — the common case, since most people
// only want to change a few colors — silently blank most of the UI.
func LoadColorConfig(path string) (*ColorConfig, error) {
	config := DefaultColorConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, err
	}

	return config, nil
}

// SaveColorConfig saves color configuration to file
func SaveColorConfig(config *ColorConfig, path string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// GetDefaultConfigPath returns the default config file path
func GetDefaultConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".go-mod-select", "colors.json"), nil
}

// LoadOrCreateDefault loads the color config, writing a default file the first
// time so users have something to edit.
//
// A write failure is not fatal: a read-only home directory should not stop the
// TUI from running with the defaults it already has in memory.
func LoadOrCreateDefault() (*ColorConfig, error) {
	configPath, err := GetDefaultConfigPath()
	if err != nil {
		return nil, err
	}

	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		config := DefaultColorConfig()
		if err := SaveColorConfig(config, configPath); err != nil {
			return config, fmt.Errorf("could not write default color config: %w", err)
		}
		return config, nil
	}

	return LoadColorConfig(configPath)
}

// ToLipglossColor converts hex color string to lipgloss.Color
func (c *ColorConfig) ToLipglossColor(colorHex string) lipgloss.Color {
	return lipgloss.Color(colorHex)
}

// GetStyle creates a lipgloss style with the specified color
func (c *ColorConfig) GetStyle(colorHex string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c.ToLipglossColor(colorHex))
}
