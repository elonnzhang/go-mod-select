//go:build !windows

package e2e

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// release is one published version of a fake module.
type release struct {
	version string
	goDir   string // the go directive in that release's go.mod
}

// fakeProxy serves just enough of the GOPROXY protocol for go list and go get.
type fakeProxy struct {
	mu      sync.Mutex
	modules map[string][]release // ascending, as go reports them
	delays  map[string]time.Duration
	server  *httptest.Server
}

func newFakeProxy(t *testing.T) *fakeProxy {
	t.Helper()
	p := &fakeProxy{modules: map[string][]release{}, delays: map[string]time.Duration{}}
	p.server = httptest.NewServer(p)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeProxy) url() string { return p.server.URL }

func (p *fakeProxy) publish(module string, releases ...release) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modules[module] = append(p.modules[module], releases...)
}

// delay slows responses of one kind ("list", "info", "mod", "zip") or, with
// "*", every response, so a test can hold go in the middle of a command.
func (p *fakeProxy) delay(kind string, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delays[kind] = d
}

func (p *fakeProxy) delayFor(kind string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.delays[kind], p.delays["*"])
}

// sleep applies the configured delay, giving up as soon as the client hangs up
// so a test that interrupts go does not wait out the delay at teardown.
func (p *fakeProxy) sleep(r *http.Request, kind string) {
	select {
	case <-time.After(p.delayFor(kind)):
	case <-r.Context().Done():
	}
}

func (p *fakeProxy) releases(module string) []release {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]release(nil), p.modules[module]...)
}

func (p *fakeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	module, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/@")
	releases := p.releases(module)
	if !ok || len(releases) == 0 {
		http.NotFound(w, r)
		return
	}

	if rest == "latest" {
		latest := releases[len(releases)-1]
		for _, rel := range releases {
			if !strings.Contains(rel.version, "-") {
				latest = rel
			}
		}
		p.sleep(r, "info")
		fmt.Fprintf(w, `{"Version":%q,"Time":"2024-01-01T00:00:00Z"}`, latest.version)
		return
	}

	name, found := strings.CutPrefix(rest, "v/")
	if !found {
		http.NotFound(w, r)
		return
	}
	if name == "list" {
		p.sleep(r, "list")
		for _, rel := range releases {
			fmt.Fprintln(w, rel.version)
		}
		return
	}

	dot := strings.LastIndex(name, ".")
	version, kind := name[:dot], name[dot+1:]
	var rel *release
	for i := range releases {
		if releases[i].version == version {
			rel = &releases[i]
		}
	}
	if rel == nil {
		http.NotFound(w, r)
		return
	}

	p.sleep(r, kind)
	switch kind {
	case "info":
		fmt.Fprintf(w, `{"Version":%q,"Time":"2024-01-01T00:00:00Z"}`, version)
	case "mod":
		fmt.Fprint(w, goMod(module, rel.goDir))
	case "zip":
		w.Write(moduleZip(module, *rel))
	default:
		http.NotFound(w, r)
	}
}

func goMod(module, goDir string) string {
	return fmt.Sprintf("module %s\n\ngo %s\n", module, goDir)
}

// moduleZip builds the zip go get unpacks: every entry lives under module@version/.
func moduleZip(module string, rel release) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	prefix := module + "@" + rel.version + "/"
	pkg := module[strings.LastIndex(module, "/")+1:]
	for name, body := range map[string]string{
		"go.mod":    goMod(module, rel.goDir),
		pkg + ".go": "package " + pkg + "\n",
	} {
		f, _ := zw.Create(prefix + name)
		f.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}
