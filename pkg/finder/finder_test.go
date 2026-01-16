package finder

import (
	"testing"
)

func TestDetermineUpdateType(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		target   string
		expected UpdateType
	}{
		{
			name:     "Patch update",
			current:  "v1.2.3",
			target:   "v1.2.4",
			expected: Patch,
		},
		{
			name:     "Minor update",
			current:  "v1.2.3",
			target:   "v1.3.0",
			expected: Minor,
		},
		{
			name:     "Major update",
			current:  "v1.2.3",
			target:   "v2.0.0",
			expected: Major,
		},
		{
			name:     "Prerelease update",
			current:  "v1.2.3",
			target:   "v1.2.4-beta.1",
			expected: Prerelease,
		},
		{
			name:     "Without v prefix",
			current:  "1.2.3",
			target:   "1.3.0",
			expected: Minor,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetermineUpdateType(tt.current, tt.target)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestExtractMinor(t *testing.T) {
	tests := []struct {
		version  string
		expected string
	}{
		{"v1.2.3", "v1.2"},
		{"v2.0.1", "v2.0"},
		{"v1.25.0", "v1.25"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			result := extractMinor(tt.version)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestUpdateTypeString(t *testing.T) {
	tests := []struct {
		updateType UpdateType
		expected   string
	}{
		{Patch, "patch"},
		{Minor, "minor"},
		{Major, "major"},
		{Prerelease, "prerelease"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.updateType.String()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// Integration tests - these require network access and actual Go modules
// Uncomment to run with: go test -tags=integration

// func TestListVersions(t *testing.T) {
// 	versions, err := ListVersions("github.com/fatih/color")
// 	if err != nil {
// 		t.Fatalf("ListVersions failed: %v", err)
// 	}
//
// 	if len(versions) == 0 {
// 		t.Error("Expected at least one version")
// 	}
//
// 	// Verify versions have v prefix
// 	for _, v := range versions {
// 		if !strings.HasPrefix(v, "v") {
// 			t.Errorf("Version %s should have v prefix", v)
// 		}
// 	}
// }

// func TestGetModuleInfo(t *testing.T) {
// 	metadata, err := GetModuleInfo("github.com/fatih/color", "v1.16.0")
// 	if err != nil {
// 		t.Fatalf("GetModuleInfo failed: %v", err)
// 	}
//
// 	if metadata.Path != "github.com/fatih/color" {
// 		t.Errorf("Expected path github.com/fatih/color, got %s", metadata.Path)
// 	}
//
// 	if metadata.Version != "v1.16.0" {
// 		t.Errorf("Expected version v1.16.0, got %s", metadata.Version)
// 	}
// }
