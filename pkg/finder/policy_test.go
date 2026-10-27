package finder

import (
	"slices"
	"testing"
)

func TestParseLimit(t *testing.T) {
	for in, want := range map[string]Limit{"": LimitMajor, "major": LimitMajor, "minor": LimitMinor, "patch": LimitPatch} {
		if got, err := ParseLimit(in); err != nil || got != want {
			t.Errorf("ParseLimit(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseLimit("mega"); err == nil {
		t.Error("an unknown level must be rejected")
	}
	if LimitMinor.String() != "minor" || LimitPatch.String() != "patch" || LimitMajor.String() != "major" {
		t.Error("String should round-trip the flag values")
	}
}

func TestLimitAllows(t *testing.T) {
	cases := []struct {
		limit              Limit
		current, candidate string
		want               bool
	}{
		{LimitMajor, "v1.2.3", "v3.0.0", true},
		{LimitMinor, "v1.2.3", "v1.9.0", true},
		{LimitMinor, "v1.2.3", "v2.0.0+incompatible", false},
		{LimitMinor, "v0.9.0", "v1.0.0", false},
		{LimitPatch, "v1.2.3", "v1.2.9", true},
		{LimitPatch, "v1.2.3", "v1.3.0", false},
		{LimitPatch, "", "v9.0.0", true}, // nothing to measure from
	}
	for _, c := range cases {
		if got := c.limit.allows(c.current, c.candidate); got != c.want {
			t.Errorf("%v.allows(%s, %s) = %v, want %v", c.limit, c.current, c.candidate, got, c.want)
		}
	}
}

func TestSelectCandidatesPrereleases(t *testing.T) {
	all := []string{"v1.0.0", "v1.1.0", "v1.2.0-rc1", "v1.2.0-beta"}

	cases := []struct {
		name    string
		current string
		pre     bool
		want    []string
	}{
		{"stable pin, default", "v1.0.0", false, []string{"v1.1.0", "v1.0.0"}},
		{"stable pin, --pre", "v1.0.0", true, []string{"v1.2.0-rc1", "v1.2.0-beta", "v1.1.0", "v1.0.0"}},
		{"already on a prerelease", "v1.2.0-beta", false, []string{"v1.2.0-rc1", "v1.2.0-beta", "v1.1.0", "v1.0.0"}},
		// A pseudo-version names a commit, not a prerelease line to stay on.
		{"pseudo-version pin", "v0.0.0-20250311204145-2c3ea96c31dd", false, []string{"v1.1.0", "v1.0.0"}},
	}
	for _, c := range cases {
		f := &Finder{Policy: Policy{AllowPrerelease: c.pre}}
		got, _ := f.selectCandidates(all, c.current)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: candidates = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSelectCandidatesFallsBackToPrereleasesWhenThereIsNothingElse(t *testing.T) {
	f := &Finder{}
	got, _ := f.selectCandidates([]string{"v0.1.0-alpha", "v0.1.0-beta"}, "v0.1.0-alpha")
	if len(got) != 2 {
		t.Errorf("a module with only prereleases can only be updated to one, got %v", got)
	}

	got, _ = f.selectCandidates([]string{"v0.1.0-alpha", "v0.1.0-beta"}, "v0.0.0-20250101000000-abcdefabcdef")
	if len(got) != 2 {
		t.Errorf("same for a pseudo-version pin, got %v", got)
	}
}

func TestSelectCandidatesCountsWhatTheLimitHeldBack(t *testing.T) {
	all := []string{"v1.2.0", "v1.2.5", "v1.3.0", "v1.4.0", "v2.0.0+incompatible"}

	f := &Finder{Policy: Policy{MaxUpdate: LimitPatch}}
	got, held := f.selectCandidates(all, "v1.2.0")
	if !slices.Equal(got, []string{"v1.2.5", "v1.2.0"}) || held != 3 {
		t.Errorf("patch limit: candidates %v, held back %d; want [v1.2.5 v1.2.0], 3", got, held)
	}

	f = &Finder{Policy: Policy{MaxUpdate: LimitMinor}}
	got, held = f.selectCandidates(all, "v1.2.0")
	if len(got) != 4 || held != 1 {
		t.Errorf("minor limit: candidates %v, held back %d; want 4 candidates, 1 held back", got, held)
	}

	// Older versions outside the limit are not "held back updates".
	f = &Finder{Policy: Policy{MaxUpdate: LimitPatch}}
	if _, held := f.selectCandidates([]string{"v1.0.0", "v1.2.0"}, "v1.2.0"); held != 0 {
		t.Errorf("an older version is not an update held back, got %d", held)
	}
}
