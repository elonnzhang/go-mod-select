// Package scan looks up, for each direct requirement of a module, the newest
// version a given Go release can build.
//
// It is the part of the analysis that does not depend on how results are shown:
// the TUI, --dry-run and --auto all run it. It used to live in the UI package,
// which made the command-line modes import the interface code, and left them
// with their own copy of the concurrency loop.
package scan

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/elonnzhang/go-mod-select/pkg/finder"
	"github.com/elonnzhang/go-mod-select/pkg/logger"
	"github.com/elonnzhang/go-mod-select/pkg/parser"
)

// Concurrency bounds how many lookups, and so how many `go list` subprocesses,
// run at once.
//
// Each lookup spawns subprocesses that do network I/O against the module proxy.
// The previous limit of 200 spawned hundreds of processes for a large go.mod,
// which the proxy rate-limits and the OS charges for in context switches; a
// modest pool is both faster in practice and far kinder to the machine.
const Concurrency = 12

// Statuses a lookup moves through; the TUI renders them in its analysis log.
const (
	StatusChecking = "checking"
	StatusFound    = "found"
	StatusSkipped  = "skipped"
	StatusError    = "error"
)

// Result is the outcome of looking up one requirement.
type Result struct {
	Pkg    string
	Info   *finder.ModuleInfo
	Err    error
	Status string // StatusFound, StatusSkipped or StatusError
}

// Event reports progress: a lookup has started (StatusChecking) or finished.
type Event struct {
	Pkg       string
	Status    string
	Completed int // lookups finished so far
	Total     int
}

// LookupFunc finds the newest compatible version of one module.
type LookupFunc func(modulePath, currentVersion string) (*finder.ModuleInfo, error)

// Run looks up every requirement, at most Concurrency at a time, and returns the
// results ordered by module path so the Update tab does not shuffle between runs.
//
// progress, if non-nil, is called from the lookup goroutines — concurrently, and
// possibly blocking. Returning false abandons that lookup, which is how a caller
// that is shutting down stops without waiting on a reader that has gone away.
// Cancelling ctx stops lookups that have not started; ones in flight end when the
// lookup function notices (the finder's go commands are tied to the same
// context). A caller that cancels should discard the results.
func Run(ctx context.Context, lookup LookupFunc, requires []parser.Require, progress func(Event) bool) []Result {
	total := len(requires)
	report := func(ev Event) bool { return progress == nil || progress(ev) }

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		completed atomic.Int32
		slots     = make(chan struct{}, Concurrency)
		results   = make([]Result, 0, total)
	)

	for _, req := range requires {
		wg.Add(1)
		go func(r parser.Require) {
			defer wg.Done()

			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}

			if !report(Event{Pkg: r.Path, Status: StatusChecking, Completed: int(completed.Load()), Total: total}) {
				return
			}

			info, err := safely(lookup, r.Path, r.Version)
			res := Result{Pkg: r.Path, Info: info, Err: err, Status: Classify(info, err, r.Version)}

			if !report(Event{Pkg: r.Path, Status: res.Status, Completed: int(completed.Add(1)), Total: total}) {
				return
			}

			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}(req)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Pkg < results[j].Pkg })
	return results
}

// safely runs a lookup with a panic turned into an error. Each lookup has its
// own goroutine, outside any recover the caller has, so an unrecovered panic
// would take the whole process down — with a TUI's terminal still in raw mode.
func safely(lookup LookupFunc, path, current string) (info *finder.ModuleInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Error("version lookup panicked", "module", path, "panic", fmt.Sprint(r))
			info, err = nil, fmt.Errorf("internal error: %v", r)
		}
	}()
	return lookup(path, current)
}

// Classify maps a lookup outcome to the status the TUI renders.
func Classify(info *finder.ModuleInfo, err error, currentVersion string) string {
	switch {
	case err != nil:
		return StatusError
	case info != nil && info.LatestCompatible != currentVersion:
		return StatusFound
	default:
		return StatusSkipped
	}
}

// Updatable keeps only the modules whose newest compatible version differs from
// the version pinned in go.mod.
func Updatable(results []Result) []*finder.ModuleInfo {
	var out []*finder.ModuleInfo
	for _, r := range results {
		if r.Err == nil && r.Info != nil && r.Info.LatestCompatible != r.Info.CurrentVersion {
			out = append(out, r.Info)
		}
	}
	return out
}
