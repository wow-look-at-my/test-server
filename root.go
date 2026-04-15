package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// config holds the resolved flag values for a single test-server run.
// It's populated by cobra's flag parsing and consumed by run().
type config struct {
	host           string
	port           int
	followSymlinks bool
	noOpenBrowser  bool
	reloadDebounce time.Duration
	noLivereload   bool
}

// rootCfg is the shared config populated by cobra flag parsing. It's a
// package-level var because cobra's Var*-style flag binding needs a
// pointer that outlives flag registration.
var rootCfg config

// rootCmd is the only command this binary has — the server itself.
// Having it as a top-level var lets init() register flags without the
// main file needing to know about any of this.
var rootCmd = &cobra.Command{
	Use:   "test-server",
	Short: "Trivial static file server for local web development",
	Long: `Serves the current working directory over HTTP with live-reload and
cross-origin-isolation headers set. Intended for local web development.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE:          runRootCmd,
}

func init() {
	rootCmd.Version = version
	f := rootCmd.Flags()
	f.IntVar(&rootCfg.port, "port", 0, "TCP port to bind (0 = pick a free one)")
	f.StringVar(&rootCfg.host, "host", "127.0.0.1", "host to bind")
	f.BoolVar(&rootCfg.followSymlinks, "follow-symlinks", false, "allow serving files reached via symlinks that escape the cwd")
	f.BoolVar(&rootCfg.noOpenBrowser, "no-open-browser", false, "do not open a browser window on startup")
	f.DurationVar(&rootCfg.reloadDebounce, "reload-debounce", 250*time.Millisecond, "debounce window for live-reload file-change batching (trailing-edge)")
	f.BoolVar(&rootCfg.noLivereload, "no-livereload", false, "disable live reload (no watcher, no script injection, 404 /__livereload)")
}

func runRootCmd(cmd *cobra.Command, _ []string) error {
	registerMimeTypes()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	rootAbs, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return fmt.Errorf("eval cwd: %w", err)
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return run(ctx, rootCfg, rootAbs, openBrowser)
}

func registerMimeTypes() {
	// Go's default mime table is missing a couple of extensions that matter
	// for modern web apps. Register them explicitly.
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".map", "application/json; charset=utf-8")
}

// run starts the file server, watcher, and optionally the browser. It
// blocks until ctx is cancelled or the HTTP server returns an error. The
// opener argument is injected so tests can verify (or suppress) browser
// launching without actually spawning a process.
func run(ctx context.Context, cfg config, root string, opener func(string)) error {
	hub := newReloadHub()
	srv := newServer(root, cfg.followSymlinks, hub, !cfg.noLivereload)

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.host, cfg.port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	boundAddr := ln.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://%s:%d/", cfg.host, boundAddr.Port)

	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	watcherDone := make(chan struct{})
	if cfg.noLivereload {
		// Skip the watcher entirely. Close the channel so the
		// shutdown-path <-watcherDone below doesn't deadlock.
		close(watcherDone)
	} else {
		go func() {
			defer close(watcherDone)
			if err := watchTree(ctx, root, cfg.followSymlinks, cfg.reloadDebounce, hub); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("watcher: %v", err)
			}
		}()
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- httpSrv.Serve(ln)
	}()

	log.Printf("test-server: serving %s at %s", root, url)
	log.Printf("test-server: follow-symlinks=%t", cfg.followSymlinks)
	if cfg.noLivereload {
		log.Printf("test-server: live reload disabled")
	} else {
		log.Printf("test-server: reload-debounce=%s", cfg.reloadDebounce)
	}

	if !cfg.noOpenBrowser && opener != nil {
		opener(url)
	}

	select {
	case <-ctx.Done():
		log.Printf("test-server: shutting down")
	case err := <-serverDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	hub.closeAll()
	<-watcherDone
	return nil
}
