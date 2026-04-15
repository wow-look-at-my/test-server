package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
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
	go func() { done <- watchTree(ctx, root, false, 50*time.Millisecond, hub) }()

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
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	hub := newReloadHub()
	sub := hub.subscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchTree(ctx, root, false, 50*time.Millisecond, hub) }()

	// Give the watcher a moment to register root.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644))

	select {
	case got := <-sub:
		require.NotEmpty(t, got)
		// Path should be relative (no leading slash or drive) and
		// contain the file we wrote.
		var found bool
		for _, ev := range got {
			if ev.Path == "a.txt" {
				found = true
				assert.NotEmpty(t, ev.Op)
			}
		}
		assert.True(t, found, "expected a.txt in received events, got %v", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no reload broadcast after write")
	}

	cancel()
	<-done
}

func TestWatchTreeTrailingEdgeDebounce(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	hub := newReloadHub()
	sub := hub.subscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	// Use a debounce long enough that two writes spaced < debounce apart
	// coalesce into one broadcast.
	debounce := 200 * time.Millisecond
	go func() { done <- watchTree(ctx, root, false, debounce, hub) }()

	// Let the watcher register.
	time.Sleep(100 * time.Millisecond)

	// Two writes well within the debounce window — should produce
	// exactly one broadcast containing both files.
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("1"), 0o644))
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("2"), 0o644))

	var batch []reloadEvent
	select {
	case batch = <-sub:
	case <-time.After(2 * time.Second):
		t.Fatal("no broadcast after writes")
	}

	// Gather filenames from the batch so we can assert on them regardless
	// of event order/duplicates.
	names := map[string]bool{}
	for _, ev := range batch {
		names[ev.Path] = true
	}
	assert.True(t, names["a.txt"], "expected a.txt in batch, got %v", batch)
	assert.True(t, names["b.txt"], "expected b.txt in batch, got %v", batch)

	// And no second broadcast should land within another full debounce
	// — because the two writes were coalesced.
	select {
	case extra := <-sub:
		t.Fatalf("got unexpected second broadcast: %v", extra)
	case <-time.After(debounce + 100*time.Millisecond):
	}

	cancel()
	<-done
}

func TestOpString(t *testing.T) {
	cases := map[fsnotify.Op]string{
		fsnotify.Create:                 "CREATE",
		fsnotify.Write:                  "WRITE",
		fsnotify.Remove:                 "REMOVE",
		fsnotify.Rename:                 "RENAME",
		fsnotify.Chmod:                  "CHMOD",
		fsnotify.Create | fsnotify.Write: "CREATE|WRITE",
		0:                               "UNKNOWN",
	}
	for op, want := range cases {
		assert.Equal(t, want, opString(op))
	}
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
