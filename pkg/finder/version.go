package finder

import (
	"go/version"
	"strings"
)

// normalizeGoVersion turns a bare "1.21" into "1.21.0" so that versions of
// differing precision compare correctly. go/version treats go1.21 as the
// language version, which sorts *before* go1.21.0, so comparing a module's
// "go 1.21" directive against a "1.21" target would otherwise report the
// module as requiring a newer toolchain than the target.
//
// Prerelease forms such as "1.21rc1" are left untouched.
func normalizeGoVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "go")
	if v == "" {
		return ""
	}

	dots := strings.Count(v, ".")
	if dots != 1 {
		return v
	}
	// Only pad when the whole string is digits and a single dot (1.21),
	// never for prerelease forms (1.21rc1) or toolchain names.
	for _, r := range v {
		if r != '.' && (r < '0' || r > '9') {
			return v
		}
	}
	return v + ".0"
}

// GoVersionSatisfied reports whether a module that declares moduleGo in its
// go.mod can be built by the target Go version.
//
// An empty moduleGo means the module predates the go directive and is assumed
// compatible. An unparseable version on either side is also treated as
// compatible: refusing to offer an update is worse than offering one the user
// can reject.
func GoVersionSatisfied(moduleGo, targetGo string) bool {
	if strings.TrimSpace(moduleGo) == "" {
		return true
	}

	modVer := "go" + normalizeGoVersion(moduleGo)
	tgtVer := "go" + normalizeGoVersion(targetGo)

	if !version.IsValid(modVer) || !version.IsValid(tgtVer) {
		return true
	}

	return version.Compare(modVer, tgtVer) <= 0
}

// SameGoLanguageVersion reports whether two Go version strings name the same
// language version, ignoring patch precision: "1.24" and "1.24.0" match,
// "1.24" and "1.25" do not.
//
// Used to check that a requested go directive actually landed, since the go
// tool normalizes what it writes.
func SameGoLanguageVersion(a, b string) bool {
	na := "go" + normalizeGoVersion(a)
	nb := "go" + normalizeGoVersion(b)
	if !version.IsValid(na) || !version.IsValid(nb) {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return version.Lang(na) == version.Lang(nb)
}

// IsValidGoVersion reports whether v is a Go release version the tool can
// target, e.g. "1.24", "1.24.1" or "1.25rc1". A leading "go" is tolerated.
func IsValidGoVersion(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "go")
	if v == "" || v[0] < '0' || v[0] > '9' {
		return false
	}
	// Reject a bare major ("1"): the go directive always carries a minor.
	if !strings.Contains(v, ".") {
		return false
	}
	return version.IsValid("go" + v)
}
