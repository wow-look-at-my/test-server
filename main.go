// Command test-server is a trivial static file server for local web
// development. It serves the current working directory over
// http://127.0.0.1:<port>, auto-reloads the browser when files change on
// disk, and sets the headers needed for cross-origin isolation (so Chrome
// unlocks high-resolution timers and SharedArrayBuffer).
package main

import (
	"context"
	"errors"
	"flag"
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
)

type config struct {
	host           string
	port           int
	followSymlinks bool
	noOpenBrowser  bool
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("test-server", flag.ContinueOnError)
	var cfg config
	fs.IntVar(&cfg.port, "port", 0, "TCP port to bind (0 = pick a free one)")
	fs.StringVar(&cfg.host, "host", "127.0.0.1", "host to bind")
	fs.BoolVar(&cfg.followSymlinks, "follow-symlinks", false, "allow serving files reached via symlinks that escape the cwd")
	fs.BoolVar(&cfg.noOpenBrowser, "no-open-browser", false, "do not open a browser window on startup")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(fs.Output(), "Serves the current working directory over HTTP with live-reload and\n")
		fmt.Fprintf(fs.Output(), "cross-origin-isolation headers set. Intended for local web development.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func registerMimeTypes() {
	// Go's default mime table is missing a couple of extensions that matter
	// for modern web apps. Register them explicitly.
	_ = mime.AddExtensionType(".wasm", "application/wasm")
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".map", "application/json; charset=utf-8")
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	registerMimeTypes()

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	rootAbs, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		log.Fatalf("eval cwd: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, cfg, rootAbs, openBrowser); err != nil {
		log.Fatalf("run: %v", err)
	}
}

// run starts the file server, watcher, and optionally the browser. It
// blocks until ctx is cancelled or the HTTP server returns an error. The
// opener argument is injected so tests can verify (or suppress) browser
// launching without actually spawning a process.
func run(ctx context.Context, cfg config, root string, opener func(string)) error {
	hub := newReloadHub()
	srv := newServer(root, cfg.followSymlinks, hub)

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
	go func() {
		defer close(watcherDone)
		if err := watchTree(ctx, root, cfg.followSymlinks, hub); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("watcher: %v", err)
		}
	}()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- httpSrv.Serve(ln)
	}()

	log.Printf("test-server: serving %s at %s", root, url)
	log.Printf("test-server: follow-symlinks=%t", cfg.followSymlinks)

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
