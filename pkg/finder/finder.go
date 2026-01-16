package finder

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go-mod-select/pkg/logger"

	"golang.org/x/mod/semver"
)

// UpdateType represents the type of version update
type UpdateType int

const (
	Patch UpdateType = iota
	Minor
	Major
	Prerelease
)

func (ut UpdateType) String() string {
	switch ut {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	case Prerelease:
		return "prerelease"
	default:
		return "unknown"
	}
}

// ModuleInfo contains information about a module and its available updates
type ModuleInfo struct {
	Path             string
	CurrentVersion   string
	LatestCompatible string
	UpdateType       UpdateType
	CompatibleCount  int
	AllVersions      []string
}

// ModuleMetadata represents the JSON output from go list -m -json
type ModuleMetadata struct {
	Path      string `json:"Path"`
	Version   string `json:"Version"`
	GoVersion string `json:"GoVersion"`
}

// ListVersions lists all available versions for a module
func ListVersions(modulePath string) ([]string, error) {
	// Use -mod=mod to allow updating go.sum if needed
	cmd := exec.Command("go", "list", "-m", "-versions", "-mod=mod", modulePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Warn("failed to list versions",
			"module", modulePath,
			"error", err.Error(),
			"output", string(output))
		return nil, fmt.Errorf("failed to list versions for %s: %w\nOutput: %s", modulePath, err, output)
	}

	outputStr := strings.TrimSpace(string(output))
	logger.Log.Debug("list versions output", "module", modulePath, "output", outputStr)

	// Parse output: "module v1.0.0 v1.1.0 v2.0.0"
	parts := strings.Fields(outputStr)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty output from go list for %s", modulePath)
	}

	// If only one field (just module name, no versions), return empty list
	if len(parts) == 1 {
		logger.Log.Debug("no versions found for module", "module", modulePath)
		return []string{}, nil
	}

	// First field is the module path, rest are versions
	versions := parts[1:]
	logger.Log.Debug("found versions", "module", modulePath, "count", len(versions))
	return versions, nil
}

// GetModuleInfo gets metadata for a specific module version
func GetModuleInfo(modulePath, version string) (*ModuleMetadata, error) {
	moduleSpec := modulePath
	if version != "" {
		moduleSpec = modulePath + "@" + version
	}

	cmd := exec.Command("go", "list", "-m", "-json", "-mod=mod", moduleSpec)
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Log.Debug("failed to get module info",
			"module", moduleSpec,
			"error", err.Error(),
			"output", string(output))
		return nil, fmt.Errorf("failed to get module info for %s: %w\nOutput: %s", moduleSpec, err, output)
	}

	var metadata ModuleMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		logger.Log.Warn("failed to parse module metadata",
			"module", moduleSpec,
			"error", err.Error(),
			"output", string(output))
		return nil, fmt.Errorf("failed to parse module metadata: %w", err)
	}

	return &metadata, nil
}

// FindCompatibleVersions finds all versions compatible with the target Go version
func FindCompatibleVersions(modulePath, currentVersion, targetGoVersion string) (*ModuleInfo, error) {
	logger.Log.Info("finding compatible versions",
		"module", modulePath,
		"current", currentVersion,
		"target_go", targetGoVersion)

	// Ensure versions are in canonical form
	if !strings.HasPrefix(currentVersion, "v") {
		currentVersion = "v" + currentVersion
	}
	if !strings.HasPrefix(targetGoVersion, "v") {
		targetGoVersion = "v" + targetGoVersion
	}

	// Get all available versions
	allVersions, err := ListVersions(modulePath)
	if err != nil {
		return nil, err
	}

	// If no versions available, skip this module
	if len(allVersions) == 0 {
		logger.Log.Debug("no versions available, skipping", "module", modulePath)
		return nil, fmt.Errorf("no versions available for %s", modulePath)
	}

	// Filter compatible versions
	var compatible []string
	for i := len(allVersions) - 1; i >= 0; i-- {
		ver := allVersions[i]
		// Get module info to check GoVersion requirement
		metadata, err := GetModuleInfo(modulePath, ver)
		if err != nil {
			// Skip versions we can't get info for
			logger.Log.Debug("skipping version due to error",
				"module", modulePath,
				"version", ver,
				"error", err.Error())
			continue
		}

		// If no GoVersion specified, assume compatible
		if metadata.GoVersion == "" {
			compatible = append(compatible, ver)
			continue
		}

		// Ensure GoVersion is in canonical form
		moduleGoVersion := metadata.GoVersion
		if !strings.HasPrefix(moduleGoVersion, "v") {
			moduleGoVersion = "v" + moduleGoVersion
		}

		// Check if module's Go requirement is satisfied by target version
		if semver.Compare(moduleGoVersion, targetGoVersion) <= 0 {
			compatible = append(compatible, ver)
		}
	}

	if len(compatible) == 0 {
		logger.Log.Warn("no compatible versions found",
			"module", modulePath,
			"target_go", targetGoVersion,
			"total_versions", len(allVersions))
		return nil, fmt.Errorf("no compatible versions found for %s with Go %s", modulePath, targetGoVersion)
	}

	// Sort versions
	semver.Sort(compatible)

	// Get latest compatible version
	latest := compatible[len(compatible)-1]

	// Determine update type
	updateType := DetermineUpdateType(currentVersion, latest)

	logger.Log.Info("found compatible versions",
		"module", modulePath,
		"current", currentVersion,
		"latest", latest,
		"update_type", updateType.String(),
		"compatible_count", len(compatible))

	return &ModuleInfo{
		Path:             modulePath,
		CurrentVersion:   currentVersion,
		LatestCompatible: latest,
		UpdateType:       updateType,
		CompatibleCount:  len(compatible),
		AllVersions:      allVersions,
	}, nil
}

// DetermineUpdateType determines the type of update (patch, minor, major, prerelease)
func DetermineUpdateType(current, target string) UpdateType {
	// Ensure versions have v prefix
	if !strings.HasPrefix(current, "v") {
		current = "v" + current
	}
	if !strings.HasPrefix(target, "v") {
		target = "v" + target
	}

	// Check for prerelease
	if semver.Prerelease(target) != "" {
		return Prerelease
	}

	// Get major versions
	currentMajor := semver.Major(current)
	targetMajor := semver.Major(target)

	if currentMajor != targetMajor {
		return Major
	}

	// Compare minor versions
	// Extract minor by removing patch
	currentMinor := extractMinor(current)
	targetMinor := extractMinor(target)

	if currentMinor != targetMinor {
		return Minor
	}

	return Patch
}

// extractMinor extracts the minor version number from a semver string
func extractMinor(version string) string {
	// version format: v1.2.3
	parts := strings.Split(version, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return version
}

// IsCompatibleVersion checks if a module version is compatible with target Go version
func IsCompatibleVersion(modulePath, moduleVersion, targetGoVersion string) (bool, error) {
	metadata, err := GetModuleInfo(modulePath, moduleVersion)
	if err != nil {
		return false, err
	}

	// If no GoVersion specified, assume compatible
	if metadata.GoVersion == "" {
		return true, nil
	}

	// Ensure versions are in canonical form
	moduleGoVersion := metadata.GoVersion
	if !strings.HasPrefix(moduleGoVersion, "v") {
		moduleGoVersion = "v" + moduleGoVersion
	}
	if !strings.HasPrefix(targetGoVersion, "v") {
		targetGoVersion = "v" + targetGoVersion
	}

	return semver.Compare(moduleGoVersion, targetGoVersion) <= 0, nil
}
