package analyzer

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindFilesUsingPackage finds all .go files that import a given package
func FindFilesUsingPackage(projectRoot, packagePath string) ([]string, error) {
	var usingFiles []string

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip vendor and .git directories
		if info.IsDir() && (info.Name() == "vendor" || info.Name() == ".git" || info.Name() == ".idea") {
			return filepath.SkipDir
		}

		// Only process .go files (excluding test files for now)
		if !info.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			if usesPackage(path, packagePath) {
				// Make path relative to project root
				relPath, err := filepath.Rel(projectRoot, path)
				if err == nil {
					usingFiles = append(usingFiles, relPath)
				}
			}
		}

		return nil
	})

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

// FindWhyPackageNeeded uses go mod why to find why a package is needed
func FindWhyPackageNeeded(projectRoot, packagePath string) []string {
	cmd := exec.Command("go", "mod", "why", "-m", packagePath)
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return []string{fmt.Sprintf("• (Unable to determine: %v)", err)}
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var reasons []string

	// Parse go mod why output
	// Format is: # packagePath
	//            chain of imports
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Show the dependency chain
		if i < 15 { // Limit to first few lines
			reasons = append(reasons, fmt.Sprintf("  • %s", line))
		}
	}

	if len(reasons) == 0 {
		return []string{"  • (No dependency chain found)"}
	}

	return reasons
}
