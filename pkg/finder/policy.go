package finder

import (
	"fmt"
	"slices"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Policy decides which published versions may be proposed at all, independently
// of what the target Go version can build.
type Policy struct {
	// AllowPrerelease proposes prerelease tags (v1.2.0-rc1) even for a module
	// pinned to a stable release. Without it they are skipped, unless the module
	// is already on a prerelease or has no other kind of release.
	AllowPrerelease bool

	// MaxUpdate caps how far a proposed version may be from the current one.
	// The zero value places no cap.
	MaxUpdate Limit
}

// Limit caps how far an update may move from the version in go.mod.
type Limit int

const (
	LimitMajor Limit = iota // no cap: the default
	LimitMinor              // stay within the current major version
	LimitPatch              // stay within the current major.minor
)

// ParseLimit reads the value of --max-update.
func ParseLimit(s string) (Limit, error) {
	switch s {
	case "", "major":
		return LimitMajor, nil
	case "minor":
		return LimitMinor, nil
	case "patch":
		return LimitPatch, nil
	}
	return LimitMajor, fmt.Errorf("invalid --max-update %q (want patch, minor or major)", s)
}

func (l Limit) String() string {
	switch l {
	case LimitMinor:
		return "minor"
	case LimitPatch:
		return "patch"
	}
	return "major"
}

// allows reports whether candidate is within the limit of current. current and
// candidate are canonical semver strings ("v1.2.3").
func (l Limit) allows(current, candidate string) bool {
	if current == "" {
		return true
	}
	switch l {
	case LimitMinor:
		return semver.Major(current) == semver.Major(candidate)
	case LimitPatch:
		return semver.MajorMinor(current) == semver.MajorMinor(candidate)
	}
	return true
}

// isPrereleaseTag reports whether v is a tagged prerelease such as v1.2.0-rc1. A
// pseudo-version also has a prerelease part, but it names a commit rather than a
// release, so it does not count.
func isPrereleaseTag(v string) bool {
	return semver.Prerelease(v) != "" && !module.IsPseudoVersion(v)
}

// selectCandidates applies the finder's update policy to a module's published
// versions and returns what may be proposed, newest first, along with how many
// newer versions lie beyond the --max-update limit. Excluded prereleases are not
// counted: they are not updates the user has asked about.
//
// Prereleases are proposed only when asked for (AllowPrerelease), when the module
// is already pinned to one, or when the module has no other kind of release.
// Filtering here, before any metadata is fetched, also means excluded versions
// cost no lookups.
func (f *Finder) selectCandidates(all []string, current string) (candidates []string, heldBack int) {
	sorted := slices.Clone(all)
	semver.Sort(sorted)
	slices.Reverse(sorted)

	allowPre := f.Policy.AllowPrerelease || isPrereleaseTag(current)
	if !allowPre {
		allowPre = !slices.ContainsFunc(sorted, func(v string) bool { return !isPrereleaseTag(v) })
	}

	for _, v := range sorted {
		if !allowPre && isPrereleaseTag(v) {
			continue
		}
		if !f.Policy.MaxUpdate.allows(current, v) {
			if current != "" && semver.Compare(v, current) > 0 {
				heldBack++
			}
			continue
		}
		candidates = append(candidates, v)
	}
	return candidates, heldBack
}
