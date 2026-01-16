package config

import (
	"encoding/json"
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

// DefaultColorConfig Default color configuration
func DefaultColorConfig() *ColorConfig {
	return &ColorConfig{
		Title:         "#7d3de3ff", // Purple
		Header:        "#7d3de3ff", // Purple
		Border:        "#5f87af",   // Purple
		TabActive:     "#7d3de3ff", // Pink
		TabInactive:   "#626262",   // Gray
		TabBackground: "#bdd4dfff", // Dark gray
		Help:          "#a4cf64ff", // Gray

		Patch:      "#00d700", // Green
		Minor:      "#ffd700", // Yellow
		Major:      "#00afff", // Cyan
		Prerelease: "#ff0000", // Red

		Selected: "#7d3de3ff", // Purple
		Cursor:   "#7d3de3ff", // Purple

		PopupBorder: "#ff5faf", // Pink

		Info:    "#5fffd7",   // Cyan
		Success: "#a4cf64ff", // Green
		Log:     "#cfd4b7ff", // Gray
	}
}

// LoadColorConfig loads color configuration from file
func LoadColorConfig(path string) (*ColorConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return default config if file doesn't exist
			return DefaultColorConfig(), nil
		}
		return nil, err
	}

	var config ColorConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
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

// LoadOrCreateDefault loads config or creates default if not exists
func LoadOrCreateDefault() (*ColorConfig, error) {
	configPath, err := GetDefaultConfigPath()
	if err != nil {
		return nil, err
	}

	config, err := LoadColorConfig(configPath)
	if err != nil {
		return nil, err
	}

	// If config file doesn't exist, create it with defaults
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := SaveColorConfig(config, configPath); err != nil {
			return nil, err
		}
	}

	return config, nil
}

// ToLipglossColor converts hex color string to lipgloss.Color
func (c *ColorConfig) ToLipglossColor(colorHex string) lipgloss.Color {
	return lipgloss.Color(colorHex)
}

// GetStyle creates a lipgloss style with the specified color
func (c *ColorConfig) GetStyle(colorHex string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c.ToLipglossColor(colorHex))
}
