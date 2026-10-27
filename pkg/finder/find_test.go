package finder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// fakeProxy scripts `go list -m` so FindCompatibleVersions can be exercised
// without a network, and records every invocation.
type fakeProxy struct {
	module   string
	versions []string          // ascending, as go list reports them
	goDirs   map[string]string // version -> go directive of that release
	broken   map[string]bool   // versions whose metadata cannot be resolved
	noCombo  bool              // make the -versions+@latest query fail
	hidden   map[string]string // version -> path of a go.mod; its GoVersion is omitted, as go list does for a too-new Go
	calls    [][]string
}

func newFakeProxy(versions []string, goDirs map[string]string) *fakeProxy {
	return &fakeProxy{module: "example.com/m", versions: versions, goDirs: goDirs, broken: map[string]bool{}}
}

func (p *fakeProxy) finder() *Finder {
	return &Finder{runList: p.run}
}

func (p *fakeProxy) meta(ver string) map[string]any {
	if p.broken[ver] {
		return map[string]any{"Path": p.module, "Version": ver, "Error": map[string]string{"Err": "unavailable"}}
	}
	if modFile, ok := p.hidden[ver]; ok {
		return map[string]any{"Path": p.module, "Version": ver, "GoMod": modFile}
	}
	return map[string]any{"Path": p.module, "Version": ver, "GoVersion": p.goDirs[ver]}
}

// hideGoVersion makes go list omit ver's GoVersion, leaving only the path of a
// cached go.mod that declares goDirective.
func (p *fakeProxy) hideGoVersion(t *testing.T, ver, goDirective string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ver+".mod")
	if err := os.WriteFile(path, []byte("module "+p.module+"\n\ngo "+goDirective+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p.hidden == nil {
		p.hidden = map[string]string{}
	}
	p.hidden[ver] = path
}

func (p *fakeProxy) latest() string {
	best := ""
	for _, v := range p.versions {
		if semver.Prerelease(v) == "" && (best == "" || semver.Compare(v, best) > 0) {
			best = v
		}
	}
	return best
}

func (p *fakeProxy) run(args ...string) ([]byte, error) {
	p.calls = append(p.calls, args)

	wantJSON := slices.Contains(args, "-json")
	wantVersions := slices.Contains(args, "-versions")
	var specs []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			specs = append(specs, a)
		}
	}

	if !wantJSON {
		return []byte(p.module + " " + strings.Join(p.versions, " ") + "\n"), nil
	}

	var out strings.Builder
	for _, spec := range specs {
		_, ver, _ := strings.Cut(spec, "@")
		var obj map[string]any
		switch {
		case ver == "latest" && p.noCombo:
			obj = map[string]any{"Path": p.module, "Error": map[string]string{"Err": "boom"}}
		case ver == "latest":
			obj = p.meta(p.latest())
			if wantVersions {
				obj["Versions"] = p.versions
			}
		default:
			obj = p.meta(ver)
		}
		b, _ := json.Marshal(obj)
		out.Write(b)
		out.WriteString("\n")
	}
	return []byte(out.String()), nil
}

// requested returns every module@version spec asked for across all calls.
func (p *fakeProxy) requested() []string {
	var specs []string
	for _, call := range p.calls {
		for _, a := range call {
			if strings.Contains(a, "@") && !strings.HasSuffix(a, "@latest") {
				specs = append(specs, a)
			}
		}
	}
	return specs
}

func TestFindCompatibleNewestReleaseCostsOneCall(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.18", "v1.2.0": "1.20", "v1.3.0": "1.21"})

	info, err := p.finder().FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.3.0" {
		t.Errorf("LatestCompatible = %s, want v1.3.0", info.LatestCompatible)
	}
	if len(p.calls) != 1 {
		t.Errorf("made %d go list calls, want 1 (listing and @latest metadata share a call): %v", len(p.calls), p.calls)
	}
}

func TestFindCompatibleSkipsReleasesNeedingNewerGo(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0", "v1.4.0"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.19", "v1.2.0": "1.24", "v1.3.0": "1.26", "v1.4.0": "1.26"})

	info, err := p.finder().FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.2.0" {
		t.Errorf("LatestCompatible = %s, want v1.2.0", info.LatestCompatible)
	}
	if info.BlockedNewer != 2 {
		t.Errorf("BlockedNewer = %d, want 2", info.BlockedNewer)
	}
	// 1 combined call + 1 batch of two covers v1.3.0 and v1.2.0.
	if len(p.calls) != 2 {
		t.Errorf("made %d go list calls, want 2: %v", len(p.calls), p.calls)
	}
}

// With prereleases enabled, one newer than @latest is still a candidate; seeding
// the cache from @latest must not hide it.
func TestFindCompatibleStillConsidersNewerPrerelease(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0-rc1"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.18", "v1.2.0-rc1": "1.18"})
	f := p.finder()
	f.Policy.AllowPrerelease = true

	info, err := f.FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.2.0-rc1" || info.UpdateType != Prerelease {
		t.Errorf("got %s (%s), want v1.2.0-rc1 (prerelease)", info.LatestCompatible, info.UpdateType)
	}
}

func TestFindCompatibleFallsBackWhenCombinedQueryFails(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.18", "v1.2.0": "1.18"})
	p.noCombo = true

	info, err := p.finder().FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.2.0" {
		t.Errorf("LatestCompatible = %s, want v1.2.0", info.LatestCompatible)
	}
}

func TestFindCompatibleNeverRequestsAVersionTwice(t *testing.T) {
	versions := []string{"v1.0.0"}
	goDirs := map[string]string{"v1.0.0": "1.18"}
	for i := 1; i <= 20; i++ {
		v := fmt.Sprintf("v1.%d.0", i)
		versions = append(versions, v)
		goDirs[v] = "1.26" // every release after v1.0.0 is too new
	}
	p := newFakeProxy(versions, goDirs)

	info, err := p.finder().FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.0.0" || info.BlockedNewer != 20 {
		t.Errorf("got %s with %d blocked, want v1.0.0 with 20", info.LatestCompatible, info.BlockedNewer)
	}

	seen := map[string]bool{}
	for _, spec := range p.requested() {
		if seen[spec] {
			t.Errorf("%s was requested more than once", spec)
		}
		seen[spec] = true
	}
	// 1 combined call, then batches of 2, 4, 8 and a final 5 cover 20 releases.
	if len(p.calls) != 5 {
		t.Errorf("made %d go list calls, want 5: %v", len(p.calls), p.calls)
	}
}

// Unreadable newer releases must not be reported as "needs a newer Go", nor
// excuse a downgrade past them.
func TestFindCompatibleRefusesDowngradePastUnresolvedRelease(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0-rc1"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.18", "v1.2.0": "1.26", "v1.3.0-rc1": "1.18"})
	p.broken["v1.3.0-rc1"] = true
	f := p.finder()
	f.Policy.AllowPrerelease = true // the unreadable release is a prerelease, newer than @latest

	_, err := f.FindCompatibleVersions(p.module, "v1.2.0", "1.25")
	if err == nil || !strings.Contains(err.Error(), "could not resolve") {
		t.Fatalf("want a resolution error, got %v", err)
	}
}

// go list leaves GoVersion out for a release whose go directive is newer than
// the installed toolchain — exactly the releases that must be rejected. The
// directive then has to come from the cached go.mod, or such a release is taken
// for one with no directive and proposed.
func TestFindCompatibleReadsGoDirectiveGoListOmits(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.20", "v1.3.0": "1.26"})
	p.hideGoVersion(t, "v1.2.0", "1.26") // reached through a batch, not @latest

	info, err := p.finder().FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.1.0" {
		t.Errorf("LatestCompatible = %s, want v1.1.0 (v1.2.0 needs Go 1.26)", info.LatestCompatible)
	}
	if info.BlockedNewer != 2 {
		t.Errorf("BlockedNewer = %d, want 2", info.BlockedNewer)
	}
}

func TestFillGoVersionLeavesUnreadableAndDirectivelessAlone(t *testing.T) {
	noDirective := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(noDirective, []byte("module example.com/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, meta := range map[string]*ModuleMetadata{
		"no GoMod path":       {Version: "v1.0.0"},
		"missing file":        {Version: "v1.0.0", GoMod: filepath.Join(t.TempDir(), "absent.mod")},
		"module with no go":   {Version: "v1.0.0", GoMod: noDirective},
		"already has a value": {Version: "v1.0.0", GoVersion: "1.21", GoMod: noDirective},
	} {
		before := meta.GoVersion
		fillGoVersion(meta)
		if meta.GoVersion != before {
			t.Errorf("%s: GoVersion changed from %q to %q", name, before, meta.GoVersion)
		}
	}
}

func TestFindCompatiblePrefersTheStableReleaseOverANewerPrerelease(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.0.0", "v1.1.0", "v1.2.0-rc1"},
		map[string]string{"v1.0.0": "1.18", "v1.1.0": "1.18", "v1.2.0-rc1": "1.18"})

	info, err := p.finder().FindCompatibleVersions(p.module, "v1.0.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.1.0" || info.UpdateType == Prerelease {
		t.Errorf("got %s (%s), want the stable v1.1.0", info.LatestCompatible, info.UpdateType)
	}
	for _, spec := range p.requested() {
		if spec == p.module+"@v1.2.0-rc1" {
			t.Error("a version the policy excludes should cost no lookup")
		}
	}
	if len(p.calls) != 1 {
		t.Errorf("the stable latest should be settled by the one combined call, got %d calls", len(p.calls))
	}
}

func TestFindCompatibleHonoursMaxUpdate(t *testing.T) {
	p := newFakeProxy(
		[]string{"v1.2.0", "v1.2.4", "v1.3.0", "v1.4.0"},
		map[string]string{"v1.2.0": "1.18", "v1.2.4": "1.18", "v1.3.0": "1.18", "v1.4.0": "1.18"})
	f := p.finder()
	f.Policy.MaxUpdate = LimitPatch

	info, err := f.FindCompatibleVersions(p.module, "v1.2.0", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != "v1.2.4" || info.UpdateType != Patch {
		t.Errorf("got %s (%s), want the patch release v1.2.4", info.LatestCompatible, info.UpdateType)
	}
	if info.HeldBack != 2 {
		t.Errorf("HeldBack = %d, want 2 (v1.3.0, v1.4.0)", info.HeldBack)
	}
}

// When everything newer is beyond the limit, staying put is the answer, not an
// error: the module is simply not updatable under the chosen policy.
func TestFindCompatibleWithNothingWithinTheLimitStaysPut(t *testing.T) {
	p := newFakeProxy([]string{"v1.3.0", "v1.4.0"}, map[string]string{"v1.3.0": "1.18", "v1.4.0": "1.18"})
	f := p.finder()
	f.Policy.MaxUpdate = LimitPatch

	// Pinned to a pseudo-version: no published version shares its major.minor.
	info, err := f.FindCompatibleVersions(p.module, "v1.2.1-0.20250101000000-abcdefabcdef", "1.25")
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestCompatible != info.CurrentVersion || info.HeldBack != 2 {
		t.Errorf("got %+v, want the current version kept and 2 held back", info)
	}
}
