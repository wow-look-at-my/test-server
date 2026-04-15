package main

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRootCmdFlagsDefaults(t *testing.T) {
	// DefValue is the default string cobra/pflag recorded at registration
	// time, independent of whatever the bound pointer currently holds. We
	// want to verify the advertised defaults, not the live values.
	f := rootCmd.Flags()
	assert.Equal(t, "0", f.Lookup("port").DefValue)
	assert.Equal(t, "127.0.0.1", f.Lookup("host").DefValue)
	assert.Equal(t, "false", f.Lookup("follow-symlinks").DefValue)
	assert.Equal(t, "false", f.Lookup("no-open-browser").DefValue)
	assert.Equal(t, "250ms", f.Lookup("reload-debounce").DefValue)
	assert.Equal(t, "false", f.Lookup("no-livereload").DefValue)
}

func TestRootCmdBadFlag(t *testing.T) {
	// Run the command with a bogus flag and verify cobra reports an error.
	cmd := *rootCmd // shallow copy so we don't mutate the package var
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--nope"})
	err := cmd.Execute()
	assert.NotNil(t, err)
}

func TestRegisterMimeTypes(t *testing.T) {
	registerMimeTypes()
	assert.Equal(t, "application/wasm", mime.TypeByExtension(".wasm"))
	assert.Contains(t, mime.TypeByExtension(".mjs"), "javascript")
}

func TestRunServesAndShutsDown(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>hi</body></html>"), 0o644))

	root, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	cfg := config{
		host:          "127.0.0.1",
		port:          0,
		noOpenBrowser: false, // we inject an opener below
	}

	var openedURL string
	opener := func(u string) { openedURL = u }

	// Listen on our own so we can learn the port before run() starts.
	// We'll work around this by just polling for the listening socket.
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- run(ctx, cfg, root, opener) }()

	// Poll until the opener gets called — this tells us run() is up.
	deadline := time.Now().Add(3 * time.Second)
	for openedURL == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.NotEqual(t, "", openedURL, "opener was never called")

	resp, err := http.Get(openedURL)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "hi")
	assert.Equal(t, "same-origin", resp.Header.Get("Cross-Origin-Opener-Policy"))

	cancel()
	select {
	case err := <-runDone:
		assert.Nil(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestRunSkipsOpenerWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	root, _ := filepath.EvalSymlinks(dir)

	cfg := config{host: "127.0.0.1", port: 0, noOpenBrowser: true}

	var opened bool
	opener := func(string) { opened = true }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, root, opener) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	assert.False(t, opened)
}

func TestRunListenFailure(t *testing.T) {
	// Bind to an invalid host to force a listen error.
	cfg := config{host: "256.256.256.256", port: 1, noOpenBrowser: true}
	err := run(context.Background(), cfg, t.TempDir(), nil)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "listen")
}

func TestOpenBrowserUsesStarter(t *testing.T) {
	oldStarter := browserStarter
	defer func() { browserStarter = oldStarter }()

	var gotArgv []string
	browserStarter = func(argv []string) error {
		gotArgv = argv
		return nil
	}

	openBrowser("http://127.0.0.1:9999/")
	require.NotEqual(t, 0, len(gotArgv))
	assert.Equal(t, "http://127.0.0.1:9999/", gotArgv[len(gotArgv)-1])
}

func TestOpenBrowserStarterError(t *testing.T) {
	oldStarter := browserStarter
	defer func() { browserStarter = oldStarter }()

	browserStarter = func([]string) error { return assertFailErr("boom") }
	// Should log and return without panicking.
	openBrowser("http://127.0.0.1:9999/")
}

type assertFailErr string

func (e assertFailErr) Error() string { return string(e) }
