package cmd

import (
	"runtime/debug"
	"strings"
)

// Version is the release version. Release builds set it at link time:
//
//	go build -ldflags "-X github.com/elonnzhang/go-mod-select/cmd.Version=v1.2.3"
//
// When empty, toolVersion falls back to what the Go toolchain recorded.
var Version string

// toolVersion names this build: the linked-in version if there is one, else the
// module version `go install pkg@v1.2.3` records, else "devel" with the commit it
// was built from (marked +dirty when the tree had uncommitted changes).
func toolVersion() string {
	if Version != "" {
		return Version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if revision == "" {
		return "devel"
	}
	return "devel-" + strings.ToLower(revision[:min(len(revision), 7)]) + dirty
}
