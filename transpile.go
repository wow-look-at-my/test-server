package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// tsLoaders maps a TypeScript-family file extension to the esbuild loader that
// transpiles it to JavaScript. These are the same extensions registerMimeTypes
// labels as JavaScript for the --no-transpile (serve-raw) path.
var tsLoaders = map[string]api.Loader{
	".ts":  api.LoaderTS,
	".tsx": api.LoaderTSX,
	".mts": api.LoaderTS,
	".cts": api.LoaderTS,
}

// serveTranspiledTS transpiles the requested TypeScript source to JavaScript
// and writes it as the response, with an inline source map so the original
// .ts shows up in browser devtools.
//
// It returns true when it has handled the request (the URL names an existing,
// readable TS-family file) and false to let the caller fall through to the
// static file server -- for non-TS paths, directories, or missing files -- so
// redirects and 404s stay identical to the no-transpile behaviour.
//
// Import specifiers are left untouched: esbuild's single-file Transform does
// not rewrite them, so `import './dep.ts'` stays as-is and the browser then
// requests /dep.ts, which we transpile the same way.
func (s *server) serveTranspiledTS(w http.ResponseWriter, r *http.Request) bool {
	loader, ok := tsLoaders[strings.ToLower(path.Ext(r.URL.Path))]
	if !ok {
		return false
	}

	f, err := s.fs.Open(r.URL.Path)
	if err != nil {
		// Not found, or outside root with --follow-symlinks off. Fall through
		// so the file server produces the same 404 it always would.
		return false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return false
	}

	src, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read source: "+err.Error(), http.StatusInternalServerError)
		return true
	}

	result := api.Transform(string(src), api.TransformOptions{
		Loader:     loader,
		Sourcefile: path.Base(r.URL.Path),
		Sourcemap:  api.SourceMapInline,
	})

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")

	if len(result.Errors) > 0 {
		// Surface compile errors in the browser console (and as a thrown
		// error) instead of serving silently-broken output. The response is
		// still a valid JS module so the browser actually runs it.
		_, _ = w.Write(transpileErrorJS(r.URL.Path, result.Errors))
		return true
	}

	// For HEAD, headers are enough; net/http suppresses the body anyway, but
	// skipping the write keeps intent explicit.
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(result.Code)
	return true
}

// transpileErrorJS returns a JavaScript module body that logs the transpile
// failure to the browser console and throws, so the error is visible in the
// page's devtools rather than appearing as broken or empty output.
func transpileErrorJS(urlPath string, errs []api.Message) []byte {
	msg := fmt.Sprintf("test-server: failed to transpile %s", urlPath)
	for _, e := range errs {
		if e.Location != nil {
			msg += fmt.Sprintf("\n  %s (%s:%d:%d)", e.Text, e.Location.File, e.Location.Line, e.Location.Column)
		} else {
			msg += "\n  " + e.Text
		}
	}
	// json.Marshal yields a safely-escaped JS string literal for the message.
	enc, err := json.Marshal(msg)
	if err != nil {
		enc = []byte(`"test-server: transpile failed"`)
	}
	var b strings.Builder
	b.WriteString("console.error(")
	b.Write(enc)
	b.WriteString(");\nthrow new Error(")
	b.Write(enc)
	b.WriteString(");\n")
	return []byte(b.String())
}
