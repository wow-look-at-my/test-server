package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// get is a tiny helper: GET path from ts and return status, content-type, body.
func get(t *testing.T, base, p string) (int, string, string) {
	t.Helper()
	resp, err := http.Get(base + p)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

func TestTranspileStripsTypesTSX(t *testing.T) {
	dir := t.TempDir()
	// A .tsx file with a type annotation and JSX. esbuild lowers the JSX to
	// React.createElement and strips the type, so neither survives verbatim.
	src := "export const n: number = 2\n" +
		"export const el = <div className=\"x\">{n}</div>\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ui.tsx"), []byte(src), 0o644))

	hub := newReloadHub()
	srv := newServer(dir, false, hub, true, true)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	status, ct, body := get(t, ts.URL, "/ui.tsx")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, ct, "javascript")
	assert.NotContains(t, body, ": number")
	assert.NotContains(t, body, "<div")
	assert.Contains(t, body, "createElement")
}

func TestTranspileDisabledServesRaw(t *testing.T) {
	dir := t.TempDir()
	src := "export const greet = (s: string): string => s\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.ts"), []byte(src), 0o644))

	hub := newReloadHub()
	// transpileEnabled = false: the file server serves the bytes verbatim,
	// with the registered .ts MIME type.
	registerMimeTypes()
	srv := newServer(dir, false, hub, true, false)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	status, ct, body := get(t, ts.URL, "/app.ts")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, ct, "javascript") // MIME relabel still applies
	assert.Equal(t, src, body)           // but the TypeScript is untouched
}

func TestTranspileErrorIsVisibleInBrowser(t *testing.T) {
	dir := t.TempDir()
	// Syntactically invalid: esbuild reports an error.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.ts"), []byte("const = = =\n"), 0o644))

	hub := newReloadHub()
	srv := newServer(dir, false, hub, true, true)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	status, ct, body := get(t, ts.URL, "/bad.ts")
	// Still a 200 JS module so the browser runs it and surfaces the error,
	// rather than a silent 500 or broken script.
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, ct, "javascript")
	assert.Contains(t, body, "throw new Error(")
	assert.Contains(t, body, "failed to transpile")
}

func TestTranspileMissingFileFallsThroughTo404(t *testing.T) {
	dir := t.TempDir()
	hub := newReloadHub()
	srv := newServer(dir, false, hub, true, true)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	status, _, _ := get(t, ts.URL, "/nope.ts")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestTranspileRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	hub := newReloadHub()
	srv := newServer(dir, false, hub, true, true)

	// A raw request can carry a ".." path that an http.Client would otherwise
	// collapse. It must be canonicalized to within root (here: a nonexistent
	// file) and never read outside it.
	req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
	req.URL.Path = "/../../etc/passwd.ts"
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTranspileRespectsSymlinkContainment(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	// A real .ts outside the served root, reachable only via a symlink.
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.ts"), []byte("export const x = 1\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.ts"), filepath.Join(dir, "leak.ts")))

	hub := newReloadHub()
	// follow-symlinks off: the transpiler must not read the escaping file.
	srv := newServer(mustEval(t, dir), false, hub, true, true)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	status, _, _ := get(t, ts.URL, "/leak.ts")
	assert.Equal(t, http.StatusNotFound, status)
}
