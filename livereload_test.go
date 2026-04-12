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

func TestReloadHubBroadcast(t *testing.T) {
	hub := newReloadHub()
	ch1 := hub.subscribe()
	ch2 := hub.subscribe()
	n := hub.subscriberCount()
	require.Equal(t, 2, n)

	hub.broadcast()

	select {
	case <-ch1:
	case <-time.After(time.Second):
		t.Fatal("ch1 did not receive broadcast")
	}
	select {
	case <-ch2:
	case <-time.After(time.Second):
		t.Fatal("ch2 did not receive broadcast")
	}

	// Coalescing: two broadcasts without a receive should still only
	// deliver one wakeup.
	hub.broadcast()
	hub.broadcast()
	select {
	case <-ch1:
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
	var gotReload bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		var seen bytes.Buffer
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				seen.Write(buf[:n])
				if bytes.Contains(seen.Bytes(), []byte("data: reload")) {
					mu.Lock()
					gotReload = true
					mu.Unlock()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	hub.broadcast()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
	}

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, gotReload)

}
