package main

import (
	"io"
	"log"
	"net/http"
)

// server is the top-level HTTP handler: it sets cross-origin-isolation and
// CORS headers on every response, routes the two livereload endpoints when
// enabled, and otherwise delegates to a static file server (wrapped with
// HTML injection when livereload is enabled).
type server struct {
	root              string
	followSymlinks    bool
	livereloadEnabled bool
	hub               *reloadHub
	fileServer        http.Handler
}

func newServer(root string, followSymlinks bool, hub *reloadHub, livereloadEnabled bool) *server {
	var fs http.FileSystem = http.Dir(root)
	if !followSymlinks {
		fs = &safeFS{root: root, inner: http.Dir(root)}
	}
	return &server{
		root:              root,
		followSymlinks:    followSymlinks,
		livereloadEnabled: livereloadEnabled,
		hub:               hub,
		fileServer:        http.FileServer(fs),
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setCommonHeaders(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if s.livereloadEnabled {
		switch r.URL.Path {
		case livereloadPath:
			s.handleLivereload(w, r)
			return
		case livereloadJSPath:
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, livereloadClientJS)
			return
		}

		// Wrap the ResponseWriter so HTML responses get the livereload
		// script tag injected before </body>.
		iw := &htmlInjectingWriter{ResponseWriter: w}
		s.fileServer.ServeHTTP(iw, r)
		if err := iw.finish(); err != nil {
			log.Printf("response finish: %v", err)
		}
		return
	}

	// Livereload disabled: no routing for /__livereload*, no injection.
	// Requests to those paths fall through to the file server, which
	// returns 404 (nothing on disk under those names).
	s.fileServer.ServeHTTP(w, r)
}

// setCommonHeaders installs the headers every response needs for this to be
// a useful local dev server: cross-origin isolation (so Chrome unlocks
// high-resolution timers and SharedArrayBuffer), permissive CORS, and no
// caching so page refreshes actually reload.
func setCommonHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Embedder-Policy", "require-corp")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "*")
	h.Set("Access-Control-Allow-Headers", "*")
	h.Set("Access-Control-Allow-Private-Network", "true")
	h.Set("Cache-Control", "no-store, must-revalidate")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")
}
