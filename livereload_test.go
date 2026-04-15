package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestIsHTMLContentType(t *testing.T) {
	cases := map[string]bool{
		"text/html":			true,
		"text/html; charset=utf-8":	true,
		"TEXT/HTML":			true,
		"  text/html  ":		true,
		"application/json":		false,
		"":				false,
		"text/plain":			false,
		"text/html-ish":		true,	// has prefix; acceptable
		"application/xhtml+xml":	false,
	}
	for in, want := range cases {
		got := isHTMLContentType(in)
		assert.Equal(t, want, got)

	}
}

func TestIndexFoldASCII(t *testing.T) {
	cases := []struct {
		s, sub	string
		want	int
	}{
		{"hello WORLD", "world", 6},
		{"HELLO", "hello", 0},
		{"abcdef", "", 0},
		{"short", "longer", -1},
		{"no match here", "xyz", -1},
		{"</BODY>", "</body>", 0},
	}
	for _, c := range cases {
		got := indexFoldASCII([]byte(c.s), []byte(c.sub))
		assert.Equal(t, c.want, got)

	}
}

func TestInjectLivereload(t *testing.T) {
	tag := `<script src="` + livereloadJSPath + `"></script>`

	// With </body>: insert before it.
	in := []byte("<html><body>hi</body></html>")
	out := string(injectLivereload(in))
	want := "<html><body>hi" + tag + "</body></html>"
	assert.Equal(t, want, out)

	// Case-insensitive match.
	in = []byte("<HTML><BODY>hi</BODY></HTML>")
	out = string(injectLivereload(in))
	assert.Contains(t, out, tag+"</BODY>")

	// No </body>: append.
	in = []byte("no body tag here")
	out = string(injectLivereload(in))
	assert.True(t, strings.HasSuffix(out, tag))

}

func TestEncodeReloadPayload(t *testing.T) {
	// Empty batch — always emits an object with an empty array, never
	// null, so the browser parser doesn't need a null check.
	assert.Equal(t, `{"changes":[]}`, encodeReloadPayload(nil))
	assert.Equal(t, `{"changes":[]}`, encodeReloadPayload([]reloadEvent{}))

	// Single event, exact JSON.
	got := encodeReloadPayload([]reloadEvent{{Path: "index.html", Op: "WRITE"}})
	assert.Equal(t, `{"changes":[{"path":"index.html","op":"WRITE"}]}`, got)

	// Multiple events, preserves order.
	got = encodeReloadPayload([]reloadEvent{
		{Path: "a.html", Op: "CREATE|WRITE"},
		{Path: "sub/b.css", Op: "WRITE"},
	})
	assert.Equal(t, `{"changes":[{"path":"a.html","op":"CREATE|WRITE"},{"path":"sub/b.css","op":"WRITE"}]}`, got)
}

func TestReloadHubBroadcast(t *testing.T) {
	hub := newReloadHub()
	ch1 := hub.subscribe()
	ch2 := hub.subscribe()
	n := hub.subscriberCount()
	require.Equal(t, 2, n)

	first := []reloadEvent{{Path: "a.html", Op: "WRITE"}}
	hub.broadcast(first)

	select {
	case got := <-ch1:
		assert.Equal(t, first, got)
	case <-time.After(time.Second):
		t.Fatal("ch1 did not receive broadcast")
	}
	select {
	case got := <-ch2:
		assert.Equal(t, first, got)
	case <-time.After(time.Second):
		t.Fatal("ch2 did not receive broadcast")
	}

	// Coalescing: a second broadcast before the first is read replaces
	// the pending batch rather than stacking up or being dropped.
	hub.broadcast([]reloadEvent{{Path: "old.html", Op: "WRITE"}})
	latest := []reloadEvent{{Path: "new.html", Op: "CREATE"}}
	hub.broadcast(latest)
	select {
	case got := <-ch1:
		assert.Equal(t, latest, got)
	case <-time.After(time.Second):
		t.Fatal("ch1 did not receive coalesced broadcast")
	}
	select {
	case <-ch1:
		t.Fatal("ch1 got a second wakeup from coalesced broadcasts")
	case <-time.After(50 * time.Millisecond):
	}

	hub.unsubscribe(ch1)
	n = hub.subscriberCount()
	require.Equal(t, 1, n)

	// Drain ch2 (it picked up the coalesced broadcast above) so closeAll's
	// close is visible as a zero-value/closed receive.
	select {
	case <-ch2:
	default:
	}

	hub.closeAll()
	_, ok := <-ch2
	assert.False(t, ok)

	// Subscribing after close returns a closed channel.
	ch3 := hub.subscribe()
	_, ok = <-ch3
	assert.False(t, ok)

}

func TestHTMLInjectingWriterHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	iw := &htmlInjectingWriter{ResponseWriter: rec}
	iw.Header().Set("Content-Type", "text/html; charset=utf-8")
	iw.Header().Set("Content-Length", "999")	// should get wiped
	iw.WriteHeader(http.StatusOK)
	_, _ = iw.Write([]byte("<html><body>hi</body></html>"))
	require.NoError(t, iw.finish())

	assert.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, livereloadJSPath)

	cl := rec.Header().Get("Content-Length")
	assert.False(t, cl == "" || cl == "999")

}

func TestHTMLInjectingWriterNonHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	iw := &htmlInjectingWriter{ResponseWriter: rec}
	iw.Header().Set("Content-Type", "application/json")
	iw.WriteHeader(http.StatusOK)
	_, _ = iw.Write([]byte(`{"ok":true}`))
	require.NoError(t, iw.finish())

	assert.Equal(t, `{"ok":true}`, rec.Body.String())

}

func TestHTMLInjectingWriterHEADEmptyBody(t *testing.T) {
	// Simulate a HEAD response: WriteHeader is called, Write is never
	// called, so the buffer is empty. finish() must not inject the tag
	// (which would send a bogus body and bogus Content-Length).
	rec := httptest.NewRecorder()
	iw := &htmlInjectingWriter{ResponseWriter: rec}
	iw.Header().Set("Content-Type", "text/html; charset=utf-8")
	iw.WriteHeader(http.StatusOK)
	require.NoError(t, iw.finish())

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, rec.Body.Len())
}

func TestHTMLInjectingWriterImplicitWriteHeader(t *testing.T) {
	// Write without an explicit WriteHeader — should default to 200.
	rec := httptest.NewRecorder()
	iw := &htmlInjectingWriter{ResponseWriter: rec}
	iw.Header().Set("Content-Type", "text/plain")
	_, _ = iw.Write([]byte("plain"))
	require.NoError(t, iw.finish())

	assert.Equal(t, "plain", rec.Body.String())

}

func TestHandleLivereloadSSE(t *testing.T) {
	hub := newReloadHub()
	srv := &server{hub: hub}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleLivereload))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.Nil(t, err)

	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	assert.Equal(t, "text/event-stream", ct)

	// Wait until the client is subscribed, then broadcast.
	deadline := time.Now().Add(2 * time.Second)
	for hub.subscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.NotEqual(t, 0, hub.subscriberCount())

	var mu sync.Mutex
	var gotLine []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		var seen bytes.Buffer
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				seen.Write(buf[:n])
				if idx := bytes.Index(seen.Bytes(), []byte("data: {")); idx >= 0 {
					// Capture up to (and including) the next \n\n.
					rest := seen.Bytes()[idx:]
					if end := bytes.Index(rest, []byte("\n\n")); end >= 0 {
						mu.Lock()
						gotLine = append([]byte(nil), rest[:end]...)
						mu.Unlock()
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	hub.broadcast([]reloadEvent{{Path: "index.html", Op: "WRITE"}})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, string(gotLine), `"changes":`)
	assert.Contains(t, string(gotLine), `"path":"index.html"`)
	assert.Contains(t, string(gotLine), `"op":"WRITE"`)

}
