package analyzer

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// maxWhyChainLines caps how much of a `go mod why` chain is shown in the
// detail popup; deep chains are informative for the first few hops only.
const maxWhyChainLines = 15

// skipDirs are directories that never contain first-party source worth
// attributing an import to.
var skipDirs = map[string]bool{
	"vendor":       true,
	".git":         true,
	".idea":        true,
	".vscode":      true,
	"node_modules": true,
	"testdata":     true,
}

// FindFilesUsingPackage finds all .go files under projectRoot that import
// packagePath (or any package beneath it), returned as sorted relative paths.
//
// Unreadable directories are skipped rather than aborting the walk: a partial
// answer is more useful in a detail popup than an error.
func FindFilesUsingPackage(projectRoot, packagePath string) ([]string, error) {
	var usingFiles []string

	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			// Skip build/tooling dirs and hidden dirs, but never the root
			// itself (which may legitimately be a dotted path).
			if path != projectRoot && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		if usesPackage(path, packagePath) {
			if relPath, relErr := filepath.Rel(projectRoot, path); relErr == nil {
				usingFiles = append(usingFiles, relPath)
			}
		}
		return nil
	})

	sort.Strings(usingFiles)
	return usingFiles, err
}

// usesPackage checks if a Go file imports a given package
func usesPackage(filePath, packagePath string) bool {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
	if err != nil {
		return false
	}

	for _, imp := range node.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if importPath == packagePath || strings.HasPrefix(importPath, packagePath+"/") {
			return true
		}
	}

	return false
}

// FindWhyPackageNeeded uses go mod why to find why a package is needed.
//
// -mod=readonly keeps the inspection side-effect free: the user is only
// browsing a detail popup and should not have their go.mod rewritten by it.
func FindWhyPackageNeeded(projectRoot, packagePath string) []string {
	cmd := exec.Command("go", "mod", "why", "-m", "-mod=readonly", packagePath)
	cmd.Dir = projectRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return []string{fmt.Sprintf("  • (Unable to determine: %s)", detail)}
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var reasons []string

	// go mod why output is a "# module" header followed by the import chain.
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if len(reasons) >= maxWhyChainLines {
			reasons = append(reasons, "  • …")
			break
		}
		reasons = append(reasons, fmt.Sprintf("  • %s", line))
	}

	if len(reasons) == 0 {
		return []string{"  • (No dependency chain found)"}
	}

	return reasons
}
