package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestShouldIgnoreEvent(t *testing.T) {
	cases := map[string]bool{
		"/tmp/foo.txt~":	true,
		"/tmp/.#foo.txt":	true,
		"/tmp/foo.swp":		true,
		"/tmp/foo.swx":		true,
		"/tmp/4913":		true,
		"/tmp/foo.txt":		false,
		"/tmp/sub/foo.txt":	false,
	}
	for p, want := range cases {
		got := shouldIgnoreEvent(p)
		assert.Equal(t, want, got)

	}
}

func TestWalkDirsSkipsSymlinksByDefault(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))

	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))

	require.NoError(t, os.Mkdir(filepath.Join(root, "node_modules"), 0o755))

	// Symlinked directory.
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))

	var got []string
	require.NoError(t, walkDirs(root, false, func(dir string,) {
		rel, _ := filepath.Rel(root, dir)
		got = append(got, rel)

	}))

	sort.Strings(got)
	want := []string{".", "sub"}
	assert.True(t, equalStringSlices(got, want))

}

func TestWalkDirsFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(outside, "inner"), 0o755))

	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))

	var got []string
	require.NoError(t, walkDirs(root, true, func(dir string,) {
		rel, _ := filepath.Rel(root, dir)
		got = append(got, rel)

	}))

	sort.Strings(got)
	// With follow on, we expect the root plus "linked". filepath.WalkDir
	// does not recurse into symlinked directories by default, so "linked"
	// itself shows up but "linked/inner" does not — that's fine, the file
	// server still serves files under linked/ because http.Dir resolves
	// the symlink on access.
	assert.GreaterOrEqual(t, len(got), 2)

}

func TestWatchTreeCtxCancel(t *testing.T) {
	root := t.TempDir()
	hub := newReloadHub()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchTree(ctx, root, false, hub) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.Equal(t, context.Canceled, err)

	case <-time.After(2 * time.Second):
		t.Fatal("watchTree did not return after cancel")
	}
}

func TestWatchTreeBroadcastsOnWrite(t *testing.T) {
	root := t.TempDir()
	hub := newReloadHub()
	sub := hub.subscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchTree(ctx, root, false, hub) }()

	// Give the watcher a moment to register root.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644))

	select {
	case <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("no reload broadcast after write")
	}

	cancel()
	<-done
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
