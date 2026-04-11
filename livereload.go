package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	livereloadPath   = "/__livereload"
	livereloadJSPath = "/__livereload/client.js"
)

// livereloadClientJS is served at livereloadJSPath and injected into every
// HTML response. It opens an SSE connection and reloads the page on any
// message. EventSource reconnects automatically if the connection drops.
const livereloadClientJS = `(function(){
  try {
    var es = new EventSource("` + livereloadPath + `");
    es.onmessage = function(){ location.reload(); };
    es.onerror = function(){ /* EventSource auto-reconnects */ };
  } catch (e) { console.warn("livereload: " + e); }
})();`

// reloadHub is a fan-out of reload notifications to all connected SSE
// clients. Broadcasting is non-blocking and coalesces — a client that
// already has a pending reload doesn't get a second one queued.
type reloadHub struct {
	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	closed atomic.Bool
}

func newReloadHub() *reloadHub {
	return &reloadHub{subs: make(map[chan struct{}]struct{})}
}

func (h *reloadHub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if !h.closed.Load() {
		h.subs[ch] = struct{}{}
	} else {
		close(ch)
	}
	h.mu.Unlock()
	return ch
}

func (h *reloadHub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *reloadHub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
			// Already has a pending reload — coalesce.
		}
	}
}

func (h *reloadHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed.Store(true)
	for ch := range h.subs {
		close(ch)
		delete(h.subs, ch)
	}
}

func (h *reloadHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (s *server) handleLivereload(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	ctx := r.Context()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, "data: reload\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// htmlInjectingWriter buffers the body of HTML responses and injects the
// livereload script tag before flushing them to the real ResponseWriter.
// Non-HTML responses pass straight through without buffering.
type htmlInjectingWriter struct {
	http.ResponseWriter
	wroteHeader bool
	isHTML      bool
	status      int
	buf         bytes.Buffer
}

func (w *htmlInjectingWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.isHTML = isHTMLContentType(w.Header().Get("Content-Type"))
	if w.isHTML {
		// We'll rewrite Content-Length after injection.
		w.Header().Del("Content-Length")
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *htmlInjectingWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.isHTML {
		return w.buf.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *htmlInjectingWriter) finish() error {
	if !w.wroteHeader || !w.isHTML {
		return nil
	}
	// No body was written (HEAD, 304, range response with no match, etc.).
	// Don't inject anything — just forward the status we held back.
	if w.buf.Len() == 0 {
		w.ResponseWriter.WriteHeader(w.status)
		return nil
	}
	body := injectLivereload(w.buf.Bytes())
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.ResponseWriter.WriteHeader(w.status)
	_, err := w.ResponseWriter.Write(body)
	return err
}

func isHTMLContentType(ct string) bool {
	ct = strings.TrimSpace(strings.ToLower(ct))
	return strings.HasPrefix(ct, "text/html")
}

// injectLivereload inserts a <script> tag that loads the livereload client
// right before </body>. If there's no </body>, it appends the tag.
func injectLivereload(body []byte) []byte {
	tag := []byte(`<script src="` + livereloadJSPath + `"></script>`)
	if idx := indexFoldASCII(body, []byte("</body>")); idx >= 0 {
		out := make([]byte, 0, len(body)+len(tag))
		out = append(out, body[:idx]...)
		out = append(out, tag...)
		out = append(out, body[idx:]...)
		return out
	}
	return append(append([]byte{}, body...), tag...)
}

// indexFoldASCII finds the first case-insensitive occurrence of sub in s,
// for ASCII bytes only. Returns -1 if not found.
func indexFoldASCII(s, sub []byte) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	return bytes.Index(bytes.ToLower(s), bytes.ToLower(sub))
}
