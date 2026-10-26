//go:build !windows

package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const waitTimeout = 45 * time.Second

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// term runs a command in a pseudo terminal and lets a test type at it and wait
// for text to appear, the way a person would.
type term struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File

	mu   sync.Mutex
	buf  bytes.Buffer
	mark int

	exited chan struct{}
	status syscall.WaitStatus
}

func startTerm(t *testing.T, dir string, env []string, name string, args ...string) *term {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(env, "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start %s: %v", name, err)
	}

	tm := &term{t: t, cmd: cmd, ptmx: ptmx, exited: make(chan struct{})}
	go tm.pump()
	go func() {
		cmd.Wait()
		tm.status, _ = cmd.ProcessState.Sys().(syscall.WaitStatus)
		close(tm.exited)
	}()

	t.Cleanup(func() {
		select {
		case <-tm.exited:
		default:
			cmd.Process.Kill()
			<-tm.exited
		}
		ptmx.Close()
	})
	return tm
}

// pump copies the child's output into buf and answers the terminal queries
// lipgloss makes at startup (background colour, cursor position). Unanswered,
// they stall the program and swallow the first keys typed.
func (tm *term) pump() {
	chunk := make([]byte, 32*1024)
	for {
		n, err := tm.ptmx.Read(chunk)
		if n > 0 {
			data := chunk[:n]
			if bytes.Contains(data, []byte("\x1b]11;?")) {
				io.WriteString(tm.ptmx, "\x1b]11;rgb:0000/0000/0000\x1b\\")
			}
			if bytes.Contains(data, []byte("\x1b[6n")) {
				io.WriteString(tm.ptmx, "\x1b[1;1R")
			}
			tm.mu.Lock()
			tm.buf.Write(data)
			tm.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// text is what has been drawn since the last successful expect, without escape
// sequences.
func (tm *term) text() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return ansi.ReplaceAllString(string(tm.buf.Bytes()[tm.mark:]), "")
}

// expect waits for substr to appear in new output, then moves past it. Bubble
// Tea redraws only the lines that changed, so expect text that is drawn when the
// screen changes, not text that was already on it.
func (tm *term) expect(substr string) {
	tm.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(tm.text(), substr) {
			tm.mu.Lock()
			tm.mark = tm.buf.Len()
			tm.mu.Unlock()
			return
		}
		select {
		case <-tm.exited:
			tm.t.Fatalf("program exited while waiting for %q; output:\n%s", substr, tm.text())
		case <-time.After(20 * time.Millisecond):
		}
	}
	tm.t.Fatalf("timed out waiting for %q; output since last match:\n%s", substr, tm.text())
}

func (tm *term) send(s string) {
	tm.t.Helper()
	if _, err := io.WriteString(tm.ptmx, s); err != nil {
		tm.t.Fatalf("write to terminal: %v", err)
	}
}

func (tm *term) signal(sig os.Signal) {
	tm.t.Helper()
	if err := tm.cmd.Process.Signal(sig); err != nil {
		tm.t.Fatalf("signal %v: %v", sig, err)
	}
}

// wait blocks until the program exits and returns its exit code.
func (tm *term) wait(within time.Duration) int {
	tm.t.Helper()
	select {
	case <-tm.exited:
		return tm.status.ExitStatus()
	case <-time.After(within):
		tm.t.Fatalf("program still running after %v; output since last match:\n%s", within, tm.text())
		return -1
	}
}
