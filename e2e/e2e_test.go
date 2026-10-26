//go:build !windows

package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// bin is the go-mod-select binary built once for the whole package; empty under
// -short, when the tests skip. GMS_E2E_BIN substitutes another build, which is
// how to check that a test really fails on the behaviour it guards.
var bin string

func TestMain(m *testing.M) {
	flag.Parse()
	if prebuilt := os.Getenv("GMS_E2E_BIN"); prebuilt != "" && !testing.Short() {
		bin = prebuilt
		os.Exit(m.Run())
	}
	if !testing.Short() {
		dir, err := os.MkdirTemp("", "gms-e2e-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		bin = filepath.Join(dir, "go-mod-select")
		if out, err := exec.Command("go", "build", "-o", bin, "..").CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building go-mod-select: %v\n%s", err, out)
			os.RemoveAll(dir)
			os.Exit(1)
		}
		code := m.Run()
		os.RemoveAll(dir)
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// sandbox is a module to update plus an environment in which the go tool can
// only see the fake proxy and every file the program writes lands in a temp dir.
type sandbox struct {
	t     *testing.T
	proxy *fakeProxy
	dir   string // the module under test
	tmp   string // the program's TMPDIR, where the finder's scratch module goes
	env   []string

	// target is the Go version asked for. It defaults to the module's own, so no
	// go directive change muddies what a test asserts; tests about interrupting
	// an update raise it, because the directive is the first thing written and
	// so the first thing a missing rollback would leave behind.
	target string
}

// newSandbox creates a module requiring each dep at its first published version.
func newSandbox(t *testing.T, proxy *fakeProxy, deps ...string) *sandbox {
	t.Helper()
	if bin == "" {
		t.Skip("skipped in -short mode")
	}

	root := t.TempDir()
	// Go extracts modules read-only; without this t.TempDir cannot remove them.
	// Registered after TempDir's own cleanup, so it runs first.
	t.Cleanup(func() {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil {
				os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	sb := &sandbox{t: t, proxy: proxy, dir: filepath.Join(root, "app"), tmp: filepath.Join(root, "tmp"), target: "1.21"}
	for _, d := range []string{sb.dir, sb.tmp, filepath.Join(root, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var require, imports strings.Builder
	for _, dep := range deps {
		fmt.Fprintf(&require, "\t%s %s\n", dep, proxy.releases(dep)[0].version)
		fmt.Fprintf(&imports, "\t_ %q\n", dep)
	}
	sb.write("go.mod", "module example.com/app\n\ngo 1.21\n\nrequire (\n"+require.String()+")\n")
	sb.write("main.go", "package main\n\nimport (\n"+imports.String()+")\n\nfunc main() {}\n")

	skip := regexp.MustCompile(`^(GO[A-Z]*|HOME|TMPDIR|TERM)=`)
	for _, kv := range os.Environ() {
		if !skip.MatchString(kv) {
			sb.env = append(sb.env, kv)
		}
	}
	sb.env = append(sb.env,
		"HOME="+filepath.Join(root, "home"),
		"TMPDIR="+sb.tmp,
		"GOPATH="+filepath.Join(root, "gopath"),
		"GOMODCACHE="+filepath.Join(root, "gopath", "pkg", "mod"),
		"GOCACHE="+filepath.Join(root, "gocache"),
		"GOPROXY="+proxy.url(),
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOFLAGS=",
	)

	// A real project has a go.sum. Without one `go build` fails on every
	// dependency, which would make the build baseline meaningless.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = sb.dir
	tidy.Env = sb.env
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy in the fixture: %v\n%s", err, out)
	}
	return sb
}

// files is the pair of module files an update rewrites.
type files struct{ mod, sum string }

func (sb *sandbox) snapshot() files { return files{sb.read("go.mod"), sb.read("go.sum")} }

func (sb *sandbox) write(name, content string) {
	sb.t.Helper()
	if err := os.WriteFile(filepath.Join(sb.dir, name), []byte(content), 0o644); err != nil {
		sb.t.Fatal(err)
	}
}

func (sb *sandbox) read(name string) string {
	sb.t.Helper()
	b, err := os.ReadFile(filepath.Join(sb.dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

// tui starts the interactive program in a terminal.
func (sb *sandbox) tui() *term {
	sb.t.Helper()
	return startTerm(sb.t, sb.dir, sb.env, bin, sb.target, "--dir", sb.dir)
}

// run executes a non-interactive mode and returns its combined output.
func (sb *sandbox) run(args ...string) (string, error) {
	sb.t.Helper()
	cmd := exec.Command(bin, append([]string{sb.target, "--dir", sb.dir}, args...)...)
	cmd.Env = sb.env
	cmd.Dir = sb.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (sb *sandbox) scratchDirs() []string {
	dirs, _ := filepath.Glob(filepath.Join(sb.tmp, "go-mod-select-probe-*"))
	return dirs
}

// twoReleases publishes v1.0.0 and v1.1.0 of each module, both for go 1.21.
func twoReleases(p *fakeProxy, modules ...string) {
	for _, m := range modules {
		p.publish(m, release{version: "v1.0.0", goDir: "1.21"}, release{version: "v1.1.0", goDir: "1.21"})
	}
}

var quick = 15 * time.Second
