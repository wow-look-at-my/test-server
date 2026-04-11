package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestIsWithin(t *testing.T) {
	root := t.TempDir()
	assert.True(t, isWithin(root, root))

	assert.True(t, isWithin(root, filepath.Join(root, "sub", "file")))

	assert.False(t, isWithin(root, filepath.Dir(root)))

	assert.False(t, isWithin(root, "/tmp/completely/unrelated"))

}

func TestResolveWithinExisting(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(f, nil, 0o644))

	got, err := resolveWithin(f)
	require.Nil(t, err)

	want, _ := filepath.EvalSymlinks(f)
	assert.Equal(t, want, got)

}

func TestResolveWithinNonExistingLeaf(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "does-not-exist.txt")
	got, err := resolveWithin(p)
	require.Nil(t, err)

	wantDir, _ := filepath.EvalSymlinks(dir)
	want := filepath.Join(wantDir, "does-not-exist.txt")
	assert.Equal(t, want, got)

}

func TestSafeFSOpenInside(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ok"), 0o644))

	root, _ := filepath.EvalSymlinks(dir)
	fs := &safeFS{root: root, inner: http.Dir(root)}

	f, err := fs.Open("/a.txt")
	require.Nil(t, err)

	f.Close()
}

func TestSafeFSOpenEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644))

	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "leak")))

	root, _ := filepath.EvalSymlinks(dir)
	fs := &safeFS{root: root, inner: http.Dir(root)}

	_, err := fs.Open("/leak")
	assert.NotNil(t, err)

}

func TestSafeFSOpenMissing(t *testing.T) {
	dir := t.TempDir()
	root, _ := filepath.EvalSymlinks(dir)
	fs := &safeFS{root: root, inner: http.Dir(root)}
	_, err := fs.Open("/nope.txt")
	assert.NotNil(t, err)

}
