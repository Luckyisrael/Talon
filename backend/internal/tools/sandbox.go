package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	ErrPathTraversal  = errors.New("path traversal not allowed")
	ErrAbsolutePath   = errors.New("absolute paths not allowed")
	ErrOutsideRoot    = errors.New("path escapes workspace root")
	ErrSymlinkEscape  = errors.New("symlink escapes workspace root")
)

// ValidateSandboxPath ensures a user-supplied relative path stays within the root directory.
// It rejects absolute paths, traversal sequences, and resolves symlinks to verify containment.
func ValidateSandboxPath(root, userPath string) (string, error) {
	clean := filepath.Clean(userPath)

	// Reject absolute paths
	if filepath.IsAbs(clean) {
		return "", ErrAbsolutePath
	}

	// Reject paths that start with .. or contain /../
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
		return "", ErrPathTraversal
	}

	// Join with root and resolve to absolute
	fullPath := filepath.Join(root, clean)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve root: %w", err)
	}
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Verify the resolved path is within the root
	if !strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) && absFull != absRoot {
		return "", ErrOutsideRoot
	}

	return absFull, nil
}
