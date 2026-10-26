package scan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
)

func requires(n int) []parser.Require {
	reqs := make([]parser.Require, n)
	for i := range reqs {
		reqs[i] = parser.Require{Path: fmt.Sprintf("example.com/m%02d", n-i), Version: "v1.0.0"}
	}
	return reqs
}

func newer(path, current string) (*finder.ModuleInfo, error) {
	return &finder.ModuleInfo{Path: path, CurrentVersion: current, LatestCompatible: "v1.1.0"}, nil
}

func TestRunReturnsResultsSortedByPath(t *testing.T) {
	results := Run(context.Background(), newer, requires(30), nil)

	if len(results) != 30 {
		t.Fatalf("got %d results, want 30", len(results))
	}
	for i := 1; i < len(results); i++ {
		if results[i-1].Pkg >= results[i].Pkg {
			t.Fatalf("results not sorted: %s before %s", results[i-1].Pkg, results[i].Pkg)
		}
	}
}

func TestRunNeverExceedsTheConcurrencyLimit(t *testing.T) {
	var running, peak atomic.Int32
	lookup := func(path, cur string) (*finder.ModuleInfo, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		return newer(path, cur)
	}

	Run(context.Background(), lookup, requires(60), nil)

	if got := peak.Load(); got > Concurrency || got < 2 {
		t.Errorf("peak concurrency = %d, want between 2 and %d", got, Concurrency)
	}
}

func TestRunTurnsAPanicIntoAnErrorResult(t *testing.T) {
	lookup := func(path, cur string) (*finder.ModuleInfo, error) {
		if path == "example.com/m02" {
			panic("boom")
		}
		return newer(path, cur)
	}

	results := Run(context.Background(), lookup, requires(4), nil)

	if len(results) != 4 {
		t.Fatalf("a panic must not lose the other results: got %d", len(results))
	}
	for _, r := range results {
		if r.Pkg == "example.com/m02" && (r.Status != StatusError || r.Err == nil) {
			t.Errorf("panicking lookup = %+v, want an error result", r)
		}
		if r.Pkg != "example.com/m02" && r.Status != StatusFound {
			t.Errorf("%s = %s, want found", r.Pkg, r.Status)
		}
	}
}

func TestRunReportsCheckingBeforeTheOutcome(t *testing.T) {
	var mu sync.Mutex
	order := map[string][]string{}
	var lastCompleted, total int
	progress := func(ev Event) bool {
		mu.Lock()
		defer mu.Unlock()
		order[ev.Pkg] = append(order[ev.Pkg], ev.Status)
		total = ev.Total
		if ev.Status != StatusChecking {
			lastCompleted = max(lastCompleted, ev.Completed)
		}
		return true
	}

	Run(context.Background(), newer, requires(10), progress)

	for pkg, statuses := range order {
		if len(statuses) != 2 || statuses[0] != StatusChecking || statuses[1] != StatusFound {
			t.Errorf("%s events = %v, want [checking found]", pkg, statuses)
		}
	}
	if total != 10 || lastCompleted != 10 {
		t.Errorf("total %d, completed %d; want 10 / 10", total, lastCompleted)
	}
}

// A caller that is shutting down returns false from progress; Run must neither
// hang nor keep results for the abandoned lookups.
func TestRunStopsWhenProgressSaysSo(t *testing.T) {
	progress := func(ev Event) bool { return false }

	results := Run(context.Background(), newer, requires(20), progress)

	if len(results) != 0 {
		t.Errorf("got %d results after the caller abandoned the run", len(results))
	}
}

func TestRunDoesNotStartLookupsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	lookup := func(path, cur string) (*finder.ModuleInfo, error) {
		if started.Add(1) == 1 {
			cancel() // the user interrupts while the first lookup runs
		}
		time.Sleep(20 * time.Millisecond)
		return newer(path, cur)
	}

	Run(ctx, lookup, requires(200), nil)

	if got := started.Load(); got > 2*Concurrency {
		t.Errorf("%d lookups started after cancellation; at most the %d already holding a slot should run", got, Concurrency)
	}
}

func TestClassifyAndUpdatable(t *testing.T) {
	same := &finder.ModuleInfo{CurrentVersion: "v1.0.0", LatestCompatible: "v1.0.0"}
	diff := &finder.ModuleInfo{CurrentVersion: "v1.0.0", LatestCompatible: "v1.2.0"}

	for _, tc := range []struct {
		name string
		info *finder.ModuleInfo
		err  error
		want string
	}{
		{"error wins", diff, errors.New("x"), StatusError},
		{"newer exists", diff, nil, StatusFound},
		{"already newest", same, nil, StatusSkipped},
		{"nothing returned", nil, nil, StatusSkipped},
	} {
		if got := Classify(tc.info, tc.err, "v1.0.0"); got != tc.want {
			t.Errorf("%s: Classify = %s, want %s", tc.name, got, tc.want)
		}
	}

	got := Updatable([]Result{
		{Pkg: "a", Info: diff},
		{Pkg: "b", Info: same},
		{Pkg: "c", Info: diff, Err: errors.New("x")},
		{Pkg: "d"},
	})
	if len(got) != 1 || got[0] != diff {
		t.Errorf("Updatable = %v, want only the one with a newer version and no error", got)
	}
}
