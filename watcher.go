package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceWindow = 100 * time.Millisecond

// watchTree walks the tree rooted at root, registers every directory with
// fsnotify, and broadcasts a reload to hub whenever anything changes. New
// directories discovered at runtime are automatically added to the watch
// set. Events are debounced so a batch of saves produces one reload.
func watchTree(ctx context.Context, root string, followSymlinks bool, hub *reloadHub) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new watcher: %w", err)
	}
	defer w.Close()

	addDir := func(dir string) {
		if err := w.Add(dir); err != nil {
			log.Printf("watch add %s: %v", dir, err)
		}
	}

	if err := walkDirs(root, followSymlinks, addDir); err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	var pending bool
	timer := time.NewTimer(debounceWindow)
	timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if shouldIgnoreEvent(ev.Name) {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = walkDirs(ev.Name, followSymlinks, addDir)
				}
			}
			if !pending {
				pending = true
				timer.Reset(debounceWindow)
			}

		case <-timer.C:
			if pending {
				pending = false
				hub.broadcast()
			}

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

// walkDirs walks the tree rooted at root and invokes add(dir) for every
// directory it finds. When followSymlinks is true, symlinked directories are
// traversed too (a cycle-detection map keeps us from looping forever).
// When false, symlinked directories are skipped entirely to match safeFS.
// .git and node_modules are always skipped to avoid pathological watch
// counts — those directories are still served, just not watched.
func walkDirs(root string, followSymlinks bool, add func(string)) error {
	visited := make(map[string]bool)
	return walkDirsRec(root, followSymlinks, add, visited)
}

func walkDirsRec(dir string, followSymlinks bool, add func(string), visited map[string]bool) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		log.Printf("walk eval %s: %v", dir, err)
		return nil
	}
	if visited[real] {
		return nil
	}
	visited[real] = true

	add(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("walk read %s: %v", dir, err)
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == "node_modules" {
			continue
		}
		path := filepath.Join(dir, name)
		isSymlink := e.Type()&os.ModeSymlink != 0
		if isSymlink {
			if !followSymlinks {
				continue
			}
			// Only traverse symlinks that point to directories.
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
		} else if !e.IsDir() {
			continue
		}
		if err := walkDirsRec(path, followSymlinks, add, visited); err != nil {
			return err
		}
	}
	return nil
}

// shouldIgnoreEvent filters out churn from common editor save strategies
// so that editing one file doesn't trigger a storm of reloads. Editors
// typically touch several files around every save (backups, lock files,
// swap files, atomic-rename dance, etc.); we want to ignore those and
// only react to the real content change.
func shouldIgnoreEvent(path string) bool {
	base := filepath.Base(path)
	switch {
	// Trailing "~": emacs, nano, and many other editors write backup
	// files alongside the real file, e.g. "index.html~".
	case strings.HasSuffix(base, "~"):
		return true

	// Leading ".#": emacs lock files, e.g. ".#index.html". These are
	// symlinks emacs creates to mark a buffer as being edited.
	case strings.HasPrefix(base, ".#"):
		return true

	// ".swp" / ".swx": vim swap files. Vim writes these while a buffer
	// is open so it can recover from a crash.
	case strings.HasSuffix(base, ".swp"), strings.HasSuffix(base, ".swx"):
		return true

	// "4913": vim's atomic-save probe. When `:w`ing a file, vim first
	// creates a file literally named "4913" (or 4914, 4915, ... if
	// 4913 already exists) in the target directory to test whether it
	// can write there. If the probe succeeds, vim deletes it and
	// performs the real atomic rename. See vim source: src/fileio.c,
	// function vim_create_and_check_writeable(). We ignore the probe
	// so saves don't produce a spurious extra reload.
	case base == "4913":
		return true
	}
	return false
}
