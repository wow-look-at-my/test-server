package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestSetCommonHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	setCommonHeaders(rec)
	want := map[string]string{
		"Cross-Origin-Opener-Policy":		"same-origin",
		"Cross-Origin-Embedder-Policy":		"require-corp",
		"Cross-Origin-Resource-Policy":		"cross-origin",
		"Access-Control-Allow-Origin":		"*",
		"Access-Control-Allow-Methods":		"*",
		"Access-Control-Allow-Headers":		"*",
		"Access-Control-Allow-Private-Network":	"true",
		"Cache-Control":			"no-store, must-revalidate",
		"Pragma":				"no-cache",
		"Expires":				"0",
	}
	for k, v := range want {
		got := rec.Header().Get(k)
		assert.Equal(t, v, got)

	}
}

func TestServerOptionsPreflight(t *testing.T) {
	hub := newReloadHub()
	srv := newServer(t.TempDir(), false, hub)
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))

}

func TestServerServesFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>hi</body></html>"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("not html"), 0o644))

	hub := newReloadHub()
	srv := newServer(dir, false, hub)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// HTML: should have the livereload script injected.
	resp, err := http.Get(ts.URL + "/")
	require.Nil(t, err)

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Contains(t, string(body), livereloadJSPath)

	assert.Equal(t, "same-origin", resp.Header.Get("Cross-Origin-Opener-Policy"))

	// Plain text: should pass through untouched.
	resp, err = http.Get(ts.URL + "/plain.txt")
	require.Nil(t, err)

	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "not html", string(body))

	// Livereload client JS.
	resp, err = http.Get(ts.URL + livereloadJSPath)
	require.Nil(t, err)

	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Contains(t, string(body), "EventSource")

}

func TestServerFollowSymlinksOff(t *testing.T) {
	dir := t.TempDir()
	// File inside root: should be served.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("ok"), 0o644))

	// Symlink pointing outside root: should 404.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644))

	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "leak")))

	hub := newReloadHub()
	srv := newServer(mustEval(t, dir), false, hub)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/inside.txt")
	require.Nil(t, err)

	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = http.Get(ts.URL + "/leak")
	require.Nil(t, err)

	resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)

}

func TestServerFollowSymlinksOn(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("ok!"), 0o644))

	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "leak")))

	hub := newReloadHub()
	srv := newServer(mustEval(t, dir), true, hub)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/leak")
	require.Nil(t, err)

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.False(t, resp.StatusCode != 200 || string(body) != "ok!")

}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.Nil(t, err)

	return r
}
