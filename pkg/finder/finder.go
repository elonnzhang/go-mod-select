package finder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go-mod-select/pkg/logger"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// UpdateType represents the type of version update
type UpdateType int

const (
	Major UpdateType = iota
	Minor
	Patch
	Prerelease
)

func (ut UpdateType) String() string {
	switch ut {
	case Major:
		return "major"
	case Minor:
		return "minor"
	case Patch:
		return "patch"
	case Prerelease:
		return "prerelease"
	default:
		return "unknown"
	}
}

// batchSizes is how many module@version specs are resolved per `go list`
// invocation, in call order; the last entry repeats.
//
// `go list` resolves its arguments one after another, so a call costs about one
// proxy round-trip per spec (measured cold: 1 spec 0.12s, 2 specs 0.26s, 8 specs
// 0.80s). Starting small stops the common case — a compatible release near the
// top — from paying for eight lookups to use one, while the growth caps the
// subprocess count for modules whose recent releases all need a newer Go.
var batchSizes = []int{2, 4, 8}

// ModuleInfo contains information about a module and its available updates
type ModuleInfo struct {
	Path             string
	CurrentVersion   string
	LatestCompatible string
	UpdateType       UpdateType
	// Downgrade is true when LatestCompatible is older than CurrentVersion,
	// which happens when the current version requires a newer Go than the
	// target. Applying it moves the dependency backwards.
	Downgrade bool
	// BlockedNewer counts published versions newer than LatestCompatible that
	// were rejected for requiring a newer Go than the target — the reason the
	// module is not simply going to its latest release.
	BlockedNewer int
	// UnresolvedNewer counts newer versions whose metadata could not be read at
	// all (retracted tag, private module, proxy failure). Kept separate from
	// BlockedNewer: conflating the two turns a network problem into a false
	// "your pinned version needs a newer Go" claim.
	UnresolvedNewer int
}

// ModuleMetadata represents the JSON output from go list -m -json
type ModuleMetadata struct {
	Path      string `json:"Path"`
	Version   string `json:"Version"`
	GoVersion string `json:"GoVersion"`
	// GoMod is the path of the module's cached go.mod for this version.
	GoMod string `json:"GoMod,omitempty"`
	// Versions is only populated when go list is run with -versions.
	Versions []string `json:"Versions,omitempty"`
	Error    *struct {
		Err string `json:"Err"`
	} `json:"Error,omitempty"`
}

// Finder resolves module versions without touching the project under
// inspection.
//
// Queries run inside a throwaway scratch module rather than the user's module
// directory. Running them in place forces a choice between two bad options:
// -mod=mod rewrites the caller's go.mod and go.sum during what is supposed to
// be a read-only analysis, and -mod=readonly fails outright with "updates to
// go.sum needed" as soon as we ask about a version that is not already in the
// build list. A scratch module has neither problem: the proxy answers the same
// questions and nothing the user owns is written.
type Finder struct {
	// scratchDir is a minimal module used as the working directory for all
	// `go list` invocations.
	scratchDir string
	// cleanup removes scratchDir; nil when no scratch module could be created.
	cleanup func()

	// fallbackDir is used when a scratch module could not be created, so
	// analysis degrades to in-place queries instead of failing.
	fallbackDir string

	// ctx cancels in-flight subprocesses. Without it, quitting mid-analysis
	// orphans a dozen `go list` processes still talking to the module proxy.
	ctx context.Context

	// runList replaces the `go list -m` subprocess; tests use it to count and
	// script calls without a network.
	runList func(args ...string) ([]byte, error)
}

// scratchGoMod is the minimal module file for the query sandbox. The go
// directive only has to parse; it never constrains the versions we ask about.
const scratchGoMod = "module go-mod-select-probe\n\ngo 1.21\n"

// New returns a Finder for analysing the module rooted at projectDir. Call
// Close when finished to remove the scratch module.
func New(projectDir string) *Finder {
	return NewWithContext(context.Background(), projectDir)
}

// NewWithContext is New with a context that kills running subprocesses when
// cancelled.
func NewWithContext(ctx context.Context, projectDir string) *Finder {
	f := &Finder{fallbackDir: projectDir, ctx: ctx}

	dir, err := os.MkdirTemp("", "go-mod-select-probe-")
	if err != nil {
		logger.Log.Warn("scratch module unavailable, querying in place", "error", err.Error())
		return f
	}

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(scratchGoMod), 0o644); err != nil {
		logger.Log.Warn("scratch go.mod not written, querying in place", "error", err.Error())
		os.RemoveAll(dir)
		return f
	}

	f.scratchDir = dir
	f.cleanup = func() { os.RemoveAll(dir) }
	return f
}

// Close removes the scratch module. Safe to call more than once.
func (f *Finder) Close() {
	if f.cleanup != nil {
		f.cleanup()
		f.cleanup = nil
		f.scratchDir = ""
	}
}

// workDir is where go commands run: the scratch module when available.
func (f *Finder) workDir() string {
	if f.scratchDir != "" {
		return f.scratchDir
	}
	return f.fallbackDir
}

// goList runs `go list -m` with the given arguments and returns stdout. Stderr
// is captured separately so progress chatter ("go: downloading ...") cannot
// corrupt parsed output.
func (f *Finder) goList(args ...string) ([]byte, error) {
	if f.runList != nil {
		return f.runList(args...)
	}

	ctx := f.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-m"}, args...)...)
	cmd.Dir = f.workDir()
	// GOFLAGS from the user's environment could inject -mod=vendor and make
	// every proxy query fail; the flags this tool needs are passed explicitly.
	cmd.Env = append(os.Environ(), "GOFLAGS=")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ListVersions lists all published versions for a module, oldest first.
func (f *Finder) ListVersions(modulePath string) ([]string, error) {
	output, err := f.goList("-versions", modulePath)
	if err != nil {
		logger.Log.Warn("failed to list versions", "module", modulePath, "error", err.Error())
		return nil, fmt.Errorf("failed to list versions for %s: %w", modulePath, err)
	}

	// Output is a single line: "module v1.0.0 v1.1.0 v2.0.0"
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) == 0 {
		return nil, fmt.Errorf("no output from go list for %s", modulePath)
	}

	versions := fields[1:]
	logger.Log.Debug("found versions", "module", modulePath, "count", len(versions))
	return versions, nil
}

// GetModuleInfo gets metadata for a specific module version.
func (f *Finder) GetModuleInfo(modulePath, version string) (*ModuleMetadata, error) {
	spec := modulePath
	if version != "" {
		spec = modulePath + "@" + version
	}

	metas, err := f.getModuleInfoBatch([]string{spec})
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 {
		return nil, fmt.Errorf("no metadata returned for %s", spec)
	}
	if metas[0].Error != nil {
		return nil, fmt.Errorf("failed to resolve %s: %s", spec, metas[0].Error.Err)
	}
	return metas[0], nil
}

// getModuleInfoBatch resolves several module@version specs in one subprocess.
//
// -e makes go list report per-module errors inline as JSON instead of aborting
// the whole batch, so one retracted or deleted version cannot mask the others.
func (f *Finder) getModuleInfoBatch(specs []string) ([]*ModuleMetadata, error) {
	return f.getModuleInfoWith(nil, specs)
}

// getModuleInfoWith is getModuleInfoBatch with extra go list flags, e.g.
// -versions.
func (f *Finder) getModuleInfoWith(flags, specs []string) ([]*ModuleMetadata, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	args := append([]string{"-e", "-json"}, flags...)
	args = append(args, specs...)
	output, err := f.goList(args...)
	// With -e, a non-zero exit still yields usable JSON for the specs that
	// resolved, so only give up when there is nothing to decode.
	if err != nil && len(bytes.TrimSpace(output)) == 0 {
		return nil, fmt.Errorf("failed to resolve module metadata: %w", err)
	}

	metas, decodeErr := decodeModuleMetadata(bytes.NewReader(output))
	for _, meta := range metas {
		fillGoVersion(meta)
	}
	return metas, decodeErr
}

// fillGoVersion recovers a release's go directive from its cached go.mod when
// go list omitted it.
//
// go list reports GoVersion only for releases the installed toolchain is able to
// build; for one that declares a newer Go it prints no GoVersion at all (while
// still naming the .mod file). Treating the missing field as "no directive"
// would class exactly the releases that are too new as compatible.
func fillGoVersion(meta *ModuleMetadata) {
	if meta.GoVersion != "" || meta.Error != nil || meta.GoMod == "" {
		return
	}
	data, err := os.ReadFile(meta.GoMod)
	if err != nil {
		logger.Log.Debug("cannot read cached go.mod", "path", meta.GoMod, "error", err.Error())
		return
	}
	// Lax parsing: a dependency's go.mod may use syntax newer than x/mod knows.
	file, err := modfile.ParseLax(meta.GoMod, data, nil)
	if err != nil || file.Go == nil {
		return
	}
	meta.GoVersion = file.Go.Version
}

// decodeModuleMetadata reads the concatenated JSON objects that `go list -json`
// emits. It is not a JSON array, so a streaming decoder is required.
func decodeModuleMetadata(r io.Reader) ([]*ModuleMetadata, error) {
	dec := json.NewDecoder(r)
	var metas []*ModuleMetadata
	for {
		var meta ModuleMetadata
		if err := dec.Decode(&meta); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return metas, fmt.Errorf("failed to parse module metadata: %w", err)
		}
		metas = append(metas, &meta)
	}
	return metas, nil
}

// versionsWithLatest lists every published version of a module and, in the same
// subprocess, resolves the metadata of its @latest release.
//
// The newest release is the one almost every module ends up proposing, so
// fetching its go directive alongside the listing removes a whole subprocess
// (and its proxy round-trips) from the common path. If the combined query yields
// no version list — an unusual module or an older toolchain — it falls back to
// the plain listing, and latest is nil.
func (f *Finder) versionsWithLatest(modulePath string) ([]string, *ModuleMetadata, error) {
	metas, err := f.getModuleInfoWith([]string{"-versions"}, []string{modulePath + "@latest"})
	if err == nil && len(metas) == 1 && len(metas[0].Versions) > 0 {
		var latest *ModuleMetadata
		if metas[0].Error == nil && metas[0].Version != "" {
			latest = metas[0]
		}
		return metas[0].Versions, latest, nil
	}

	versions, err := f.ListVersions(modulePath)
	return versions, nil, err
}

// resolveBatch fetches metadata for up to size not-yet-seen versions from the
// front of from, recording them in metas. Every requested version is marked in
// attempted, so one that fails to resolve is not requested again.
func (f *Finder) resolveBatch(modulePath string, from []string, size int, metas map[string]*ModuleMetadata, attempted map[string]bool) {
	var specs []string
	for _, ver := range from {
		if len(specs) == size {
			break
		}
		if _, known := metas[ver]; known || attempted[ver] {
			continue
		}
		attempted[ver] = true
		specs = append(specs, modulePath+"@"+ver)
	}

	got, err := f.getModuleInfoBatch(specs)
	if err != nil {
		logger.Log.Debug("metadata batch failed", "module", modulePath, "error", err.Error())
	}
	// A truncated stream still yields the entries decoded before the break;
	// versions that never made it in are counted as unresolved when reached.
	// Index by version rather than trusting positional order.
	for _, meta := range got {
		metas[meta.Version] = meta
	}
}

// FindCompatibleVersions finds the newest published version of a module that
// can be built with the target Go version.
//
// Versions are inspected newest-first and the scan stops at the first
// compatible hit, which avoids resolving metadata for the long tail of old
// releases nobody will pick. The listing call already resolves @latest, so the
// common case (the newest release works) costs a single subprocess; otherwise
// further candidates are resolved in growing batches (see batchSizes).
//
// Note that `go list -m -versions` only reports versions under the given
// module path, and Go puts v2+ behind a /vN suffix. A v1 dependency therefore
// never surfaces a v2 update here — that is a deliberate import-path change,
// not something to apply automatically.
func (f *Finder) FindCompatibleVersions(modulePath, currentVersion, targetGoVersion string) (*ModuleInfo, error) {
	logger.Log.Info("finding compatible versions",
		"module", modulePath,
		"current", currentVersion,
		"target_go", targetGoVersion)

	if currentVersion != "" && !strings.HasPrefix(currentVersion, "v") {
		currentVersion = "v" + currentVersion
	}

	allVersions, latest, err := f.versionsWithLatest(modulePath)
	if err != nil {
		return nil, err
	}
	if len(allVersions) == 0 {
		return nil, fmt.Errorf("no published versions for %s", modulePath)
	}

	// go list -versions documents ascending order; sorting makes the
	// newest-first walk below correct regardless.
	candidates := make([]string, len(allVersions))
	copy(candidates, allVersions)
	semver.Sort(candidates)
	reverse(candidates)

	metas := make(map[string]*ModuleMetadata, len(candidates))
	if latest != nil {
		metas[latest.Version] = latest
	}
	attempted := make(map[string]bool)

	blockedNewer := 0
	unresolvedNewer := 0
	batches := 0
	for i, ver := range candidates {
		if _, known := metas[ver]; !known && !attempted[ver] {
			size := batchSizes[min(batches, len(batchSizes)-1)]
			batches++
			f.resolveBatch(modulePath, candidates[i:], size, metas, attempted)
		}

		meta, ok := metas[ver]
		if !ok || meta.Error != nil {
			// A retracted tag, a private module, or a flaky proxy. This is
			// NOT the same as "needs a newer Go" and must not be counted as
			// such: doing so let an unresolvable newest version masquerade
			// as a Go-version conflict and produced a bogus downgrade.
			unresolvedNewer++
			continue
		}
		if !GoVersionSatisfied(meta.GoVersion, targetGoVersion) {
			blockedNewer++
			continue
		}

		isDowngrade := currentVersion != "" && semver.Compare(ver, currentVersion) < 0

		// Every unresolved candidate was newer than this one (the walk is
		// newest-first). Proposing a downgrade past versions we simply
		// could not read would assert "your pinned version needs a newer
		// Go" — the documented meaning of ↓ — without having established
		// it. Report the resolution failure instead.
		if isDowngrade && unresolvedNewer > 0 {
			logger.Log.Warn("refusing to propose a downgrade past unresolved versions",
				"module", modulePath,
				"current", currentVersion,
				"candidate", ver,
				"unresolved_newer", unresolvedNewer)
			return nil, fmt.Errorf(
				"could not resolve %d version(s) of %s newer than %s; only older versions resolved, so no update is proposed",
				unresolvedNewer, modulePath, currentVersion)
		}

		info := &ModuleInfo{
			Path:             modulePath,
			CurrentVersion:   currentVersion,
			LatestCompatible: ver,
			UpdateType:       DetermineUpdateType(currentVersion, ver),
			Downgrade:        isDowngrade,
			BlockedNewer:     blockedNewer,
			UnresolvedNewer:  unresolvedNewer,
		}

		logger.Log.Info("found compatible version",
			"module", modulePath,
			"current", currentVersion,
			"latest", ver,
			"update_type", info.UpdateType.String(),
			"downgrade", info.Downgrade,
			"blocked_newer", blockedNewer,
			"unresolved_newer", unresolvedNewer)

		return info, nil
	}

	// Distinguish "the target Go rules everything out" from "we could not read
	// the metadata": the second is a proxy or network problem, and telling the
	// user it is a compatibility problem sends them after the wrong thing.
	if blockedNewer == 0 && unresolvedNewer > 0 {
		logger.Log.Warn("no versions could be resolved",
			"module", modulePath, "unresolved", unresolvedNewer)
		return nil, fmt.Errorf("could not resolve any version of %s (%d attempted); check network or GOPROXY",
			modulePath, unresolvedNewer)
	}

	logger.Log.Warn("no compatible versions found",
		"module", modulePath,
		"target_go", targetGoVersion,
		"total_versions", len(allVersions))
	return nil, fmt.Errorf("no version of %s is compatible with Go %s", modulePath, targetGoVersion)
}

func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// DetermineUpdateType determines the type of update (patch, minor, major, prerelease)
func DetermineUpdateType(current, target string) UpdateType {
	if !strings.HasPrefix(current, "v") {
		current = "v" + current
	}
	if !strings.HasPrefix(target, "v") {
		target = "v" + target
	}

	if semver.Prerelease(target) != "" {
		return Prerelease
	}

	if semver.Major(current) != semver.Major(target) {
		return Major
	}

	if semver.MajorMinor(current) != semver.MajorMinor(target) {
		return Minor
	}

	return Patch
}

// IsCompatibleVersion reports whether a module version can be built with the
// target Go version.
func (f *Finder) IsCompatibleVersion(modulePath, moduleVersion, targetGoVersion string) (bool, error) {
	metadata, err := f.GetModuleInfo(modulePath, moduleVersion)
	if err != nil {
		return false, err
	}
	return GoVersionSatisfied(metadata.GoVersion, targetGoVersion), nil
}
