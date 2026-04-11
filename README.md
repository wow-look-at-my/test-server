# test-server

A trivial static file server for local web development. Run it inside a
directory and it serves every file under that directory over
`http://127.0.0.1:<port>`, with live-reload and all the headers Chrome wants
in order to unlock `SharedArrayBuffer` and `performance.now()` at full
resolution.

## Features

- Serves the current working directory (recursively) over plain HTTP.
- **Live reload** — any file change under the cwd triggers a browser
  refresh. Implemented via Server-Sent Events + a tiny injected `<script>`
  tag, so it works in any HTML page without you touching your code.
- **Cross-origin isolation headers** set on every response:
  - `Cross-Origin-Opener-Policy: same-origin`
  - `Cross-Origin-Embedder-Policy: require-corp`
  - `Cross-Origin-Resource-Policy: cross-origin`
- **Permissive CORS** for local testing (`Access-Control-Allow-Origin: *`,
  etc.) plus `Access-Control-Allow-Private-Network: true`.
- **No caching** (`Cache-Control: no-store, must-revalidate`) so refreshes
  actually refresh.
- **Symlink containment** — by default, symlinks that resolve outside the
  cwd return `404`. Pass `--follow-symlinks` to allow them.
- **Correct MIME types** for `.wasm`, `.mjs`, and `.map`.
- Opens your default browser on startup (disable with
  `--no-open-browser`).
- Picks a free port by default (override with `--port`).
- Graceful shutdown on `Ctrl-C`.

## Install

```
go install github.com/wow-look-at-my/test-server@latest
```

Or build from source:

```
go-toolchain
./build/test-server
```

## Usage

```
cd /path/to/your/site
test-server
```

### Flags

| Flag                 | Default     | Description                                                |
|----------------------|-------------|------------------------------------------------------------|
| `--port N`           | `0` (auto)  | TCP port to bind. `0` asks the OS for a free one.          |
| `--host HOST`        | `127.0.0.1` | Host to bind to.                                           |
| `--follow-symlinks`  | `false`     | Allow serving files reached via symlinks escaping the cwd. |
| `--no-open-browser`  | `false`     | Don't open a browser window on startup.                    |

### Live reload

The server watches every directory under the cwd (minus `.git` and
`node_modules`) with `fsnotify`. On any change, after a short debounce
(~100 ms), it broadcasts a `data: reload` event to every connected SSE
client. Pages pick up the event via an injected `<script>` tag and call
`location.reload()`.

The injected tag is only added to responses with
`Content-Type: text/html`, right before `</body>` (or at the end if there
isn't one). Non-HTML responses are passed through untouched.

## Development

Build, lint, test, and coverage go through `go-toolchain`:

```
go-toolchain
```

The resulting binary lands in `./build/test-server`.

## Layout

```
main.go         flag parsing, run() entry point
server.go       top-level handler, headers, routing
livereload.go   SSE hub, HTML injection, reloadHub
safefs.go       symlink-containment http.FileSystem
watcher.go      recursive fsnotify tree + debounce
browser.go      per-OS browser launcher
```
