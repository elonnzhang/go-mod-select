package finder

import (
	"strings"
	"testing"
)

func TestDetermineUpdateType(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		target   string
		expected UpdateType
	}{
		{"patch", "v1.2.3", "v1.2.4", Patch},
		{"minor", "v1.2.3", "v1.3.0", Minor},
		{"major", "v1.2.3", "v2.0.0", Major},
		{"prerelease", "v1.2.3", "v1.2.4-beta.1", Prerelease},
		{"missing v prefix", "1.2.3", "1.3.0", Minor},
		{"same version is a patch no-op", "v1.2.3", "v1.2.3", Patch},
		// Two-digit minors must not compare as strings: "v1.9" < "v1.10"
		// numerically, but the reverse lexically.
		{"double digit minor", "v1.9.0", "v1.10.0", Minor},
		{"double digit patch", "v1.2.9", "v1.2.10", Patch},
		{"major suffix path", "v2.1.0", "v2.2.0", Minor},
		{"downgrade still classified by distance", "v1.3.0", "v1.2.0", Minor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetermineUpdateType(tt.current, tt.target); got != tt.expected {
				t.Errorf("DetermineUpdateType(%q, %q) = %s, want %s",
					tt.current, tt.target, got, tt.expected)
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
		{UpdateType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.updateType.String(); got != tt.expected {
				t.Errorf("String() = %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestGoVersionSatisfied(t *testing.T) {
	tests := []struct {
		name     string
		moduleGo string
		targetGo string
		want     bool
	}{
		{"older module runs on newer target", "1.21", "1.25", true},
		{"newer module rejected by older target", "1.26", "1.25", false},
		// The bug this replaced: semver.Compare("v1.21", "v1.21.0") == 0 only
		// by luck, while go/version ranks a bare "go1.21" below "go1.21.0",
		// so an unnormalized comparison misjudged equal versions.
		{"equal, differing precision", "1.21", "1.21.0", true},
		{"equal, differing precision reversed", "1.21.0", "1.21", true},
		{"exact match", "1.25", "1.25", true},
		{"patch above target minor", "1.25.3", "1.25", false},
		{"patch below target", "1.24.1", "1.25", true},
		// semver.Compare treats "rc1" as a prerelease ranking *below* 1.21,
		// which is also what we want here — but only go/version parses the
		// Go-specific spelling at all.
		{"module prerelease toolchain", "1.21rc1", "1.22", true},
		{"single digit minor vs double", "1.9", "1.25", true},
		{"double digit minor not compared lexically", "1.21", "1.9", false},
		// No go directive: the module predates the field, assume it builds.
		{"empty module version", "", "1.25", true},
		{"whitespace module version", "   ", "1.25", true},
		// Unparseable input must not silently exclude a module from updates.
		{"garbage module version", "not-a-version", "1.25", true},
		{"garbage target version", "1.21", "garbage", true},
		{"go prefix tolerated", "go1.21", "go1.25", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GoVersionSatisfied(tt.moduleGo, tt.targetGo); got != tt.want {
				t.Errorf("GoVersionSatisfied(%q, %q) = %v, want %v",
					tt.moduleGo, tt.targetGo, got, tt.want)
			}
		})
	}
}

func TestNormalizeGoVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1.21", "1.21.0"},
		{"1.21.0", "1.21.0"},
		{"1.21.4", "1.21.4"},
		{"go1.21", "1.21.0"},
		{"1.21rc1", "1.21rc1"}, // prerelease: never padded
		{"1.21beta2", "1.21beta2"},
		{"1", "1"}, // no minor: leave alone, IsValid rejects it
		{"", ""},
		{"  1.24  ", "1.24.0"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeGoVersion(tt.in); got != tt.want {
				t.Errorf("normalizeGoVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsValidGoVersion(t *testing.T) {
	valid := []string{"1.21", "1.24.1", "1.25", "1.25rc1", "go1.24"}
	for _, v := range valid {
		if !IsValidGoVersion(v) {
			t.Errorf("IsValidGoVersion(%q) = false, want true", v)
		}
	}

	invalid := []string{"", "1", "x1.21", "1.21.", "latest", "v1.21", "1..2"}
	for _, v := range invalid {
		if IsValidGoVersion(v) {
			t.Errorf("IsValidGoVersion(%q) = true, want false", v)
		}
	}
}

func TestSameGoLanguageVersion(t *testing.T) {
	same := [][2]string{
		{"1.24", "1.24.0"},
		{"1.24.0", "1.24"},
		{"1.24.3", "1.24"},
		{"go1.25", "1.25"},
		{"1.25", "1.25"},
	}
	for _, p := range same {
		if !SameGoLanguageVersion(p[0], p[1]) {
			t.Errorf("SameGoLanguageVersion(%q, %q) = false, want true", p[0], p[1])
		}
	}

	differ := [][2]string{
		{"1.24", "1.25"},
		{"1.9", "1.19"},
		{"1.25", "1.24.9"},
	}
	for _, p := range differ {
		if SameGoLanguageVersion(p[0], p[1]) {
			t.Errorf("SameGoLanguageVersion(%q, %q) = true, want false", p[0], p[1])
		}
	}

	// Unparseable input falls back to string equality rather than claiming a
	// match it cannot verify.
	if !SameGoLanguageVersion("garbage", "garbage") {
		t.Error("identical unparseable versions should compare equal")
	}
	if SameGoLanguageVersion("garbage", "1.25") {
		t.Error("unparseable vs valid should not match")
	}
}

func TestGetModuleInfoBatchParsesStream(t *testing.T) {
	// go list -json emits concatenated objects, not a JSON array, and with -e
	// a failed spec appears as an Error field rather than aborting the batch.
	stream := `{
	"Path": "example.com/a",
	"Version": "v1.0.0",
	"GoVersion": "1.21"
}
{
	"Path": "example.com/b",
	"Version": "v9.9.9",
	"Error": {"Err": "not found"}
}
{
	"Path": "example.com/c",
	"Version": "v2.0.0",
	"GoVersion": "1.24"
}
`

	metas, err := decodeModuleMetadata(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("decodeModuleMetadata: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("got %d metadata entries, want 3", len(metas))
	}
	if metas[0].GoVersion != "1.21" {
		t.Errorf("entry 0 GoVersion = %q, want 1.21", metas[0].GoVersion)
	}
	if metas[1].Error == nil || metas[1].Error.Err != "not found" {
		t.Errorf("entry 1 should carry an inline error, got %+v", metas[1].Error)
	}
	if metas[2].Version != "v2.0.0" {
		t.Errorf("entry 2 Version = %q, want v2.0.0", metas[2].Version)
	}
}

func TestDecodeModuleMetadataEmpty(t *testing.T) {
	metas, err := decodeModuleMetadata(strings.NewReader(""))
	if err != nil {
		t.Fatalf("empty input should not error, got %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("got %d entries from empty input, want 0", len(metas))
	}
}
