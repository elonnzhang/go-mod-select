// Package e2e drives the built go-mod-select binary through a real pseudo
// terminal against a local fake module proxy.
//
// These tests exist because the bugs that matter most in a TUI — a q typed into
// the filter quitting the program, Ctrl+C leaving go.mod half-updated, quitting
// mid-analysis leaking the scratch directory — only show up with a real terminal
// and real subprocesses. Everything is hermetic: modules come from an in-process
// proxy, and HOME, TMPDIR and the module cache point into the test's temp dir.
package e2e
