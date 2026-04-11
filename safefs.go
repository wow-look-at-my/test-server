package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// safeFS is an http.FileSystem that refuses to serve any file whose real
// on-disk path (after resolving symlinks) is not inside root. This prevents
// `ln -s /etc/passwd foo` from leaking secrets when --follow-symlinks is
// off.
type safeFS struct {
	root  string
	inner http.Dir
}

func (s *safeFS) Open(name string) (http.File, error) {
	// http.FileServer always passes slash-separated, cleaned paths starting
	// with "/". Convert to a real filesystem path and verify it resolves
	// inside root.
	rel := strings.TrimPrefix(filepath.FromSlash(name), string(filepath.Separator))
	full := filepath.Join(s.root, rel)

	resolved, err := resolveWithin(full)
	if err != nil {
		return nil, err
	}
	if !isWithin(s.root, resolved) {
		return nil, os.ErrNotExist
	}
	return s.inner.Open(name)
}

// resolveWithin returns the fully symlink-resolved absolute path of p.
// Non-existent leaves are okay: we resolve the deepest existing ancestor
// and reattach the unresolved tail, so 404s still work for paths that
// don't exist yet.
func resolveWithin(p string) (string, error) {
	p = filepath.Clean(p)
	tail := ""
	for {
		if _, err := os.Lstat(p); err == nil {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", err
			}
			if tail != "" {
				resolved = filepath.Join(resolved, tail)
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", os.ErrNotExist
		}
		tail = filepath.Join(filepath.Base(p), tail)
		p = parent
	}
}

// isWithin reports whether p is equal to root or a descendant of it, after
// resolving the root's symlinks. p is assumed to already be resolved.
func isWithin(root, p string) bool {
	rootClean, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootClean = filepath.Clean(root)
	}
	pClean := filepath.Clean(p)
	rel, err := filepath.Rel(rootClean, pClean)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}
