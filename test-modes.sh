#!/bin/bash
# Test script for go-mod-select working directory modes

echo "Testing go-mod-select working directory modes"
echo "=============================================="
echo ""

# Test 1: Current directory (should find go.mod in current dir)
echo "Test 1: Default mode (current directory)"
./go-mod-select 1.25 --dry-run 2>&1 | head -5
echo ""

# Test 2: --dir with current directory
echo "Test 2: Using --dir with current directory"
./go-mod-select 1.25 --dir . --dry-run 2>&1 | head -5
echo ""

# Test 3: --gomod with explicit path
echo "Test 3: Using --gomod with explicit path"
./go-mod-select 1.25 --gomod ./go.mod --dry-run 2>&1 | head -5
echo ""

# Test 4: Non-existent directory (should error)
echo "Test 4: Non-existent directory (should error)"
./go-mod-select 1.25 --dir /nonexistent/path 2>&1 | grep -i "error\|not found"
echo ""

# Test 5: --dir and --gomod are mutually exclusive (should error)
echo "Test 5: Both --dir and --gomod (should be rejected)"
./go-mod-select 1.25 --dir /tmp --gomod ./go.mod --dry-run 2>&1 | head -3
echo ""

echo "=============================================="
echo "Tests completed"
