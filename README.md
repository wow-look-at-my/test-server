# test-server

A trivial static file server for local web development. Run it inside a
directory and it serves every file under that directory over
`http://127.0.0.1:<port>`, with live-reload and all the headers Chrome wants
in order to unlock `SharedArrayBuffer` and `performance.now()` at full
resolution.

## Features

- Serves the current working directory (recursively) over plain HTTP.
- **TypeScript transpilation** — requests for `.ts`, `.tsx`, `.mts`, and
  `.cts` are transpiled to JavaScript on the fly (via [esbuild]), served as
  `text/javascript` with an inline source map. So
  `<script type="module" src="app.ts">` just works — no build step, no
  `tsc`, no `node_modules`. Disable with `--no-transpile` to serve the raw
  `.ts` bytes instead. Import specifiers are left untouched, so
  `import './dep.ts'` resolves to `/dep.ts`, which is transpiled the same
  way. Compile errors are surfaced in the browser console (the response is a
  JS module that logs and throws the error).
- **Live reload** — any file change under the cwd triggers a browser
  refresh. Implemented via Server-Sent Events + a tiny injected `<script>`
  tag, so it works in any HTML page without you touching your code.
- **Cross-origin isolation headers** set on every response:
  - `Cross-Origin-Opener-Policy: same-origin`
  - `Cross-Origin-Embedder-Policy: credentialless` (unlocks
    `SharedArrayBuffer` while still loading cross-origin assets such as CDN
    stylesheets and scripts; `require-corp` would block them)
  - `Cross-Origin-Resource-Policy: cross-origin`
- **Permissive CORS** for local testing (`Access-Control-Allow-Origin: *`,
  etc.) plus `Access-Control-Allow-Private-Network: true`.
- **No caching** (`Cache-Control: no-store, must-revalidate`) so refreshes
  actually refresh.
- **Symlink containment** — by default, symlinks that resolve outside the
  cwd return `404`. Pass `--follow-symlinks` to allow them.
- **Correct MIME types** for `.wasm`, `.mjs`, and `.map` (and `.ts` family
  when served raw under `--no-transpile`).
- Opens your default browser on startup (disable with
  `--no-open-browser`).
- Picks a free port by default (override with `--port`).
- Graceful shutdown on `Ctrl-C`.

## Install

Releases are published by CI (the `wow-look-at-my/go-toolchain` GitHub
Action), which also keeps the Homebrew tap current:

```
brew install pazer/build/test-server
```

## Usage

```
cd /path/to/your/site
test-server
```

### Flags

| Flag                   | Default     | Description                                                              |
|------------------------|-------------|--------------------------------------------------------------------------|
| `--port N`             | `0` (auto)  | TCP port to bind. `0` asks the OS for a free one.                        |
| `--host HOST`          | `127.0.0.1` | Host to bind to.                                                         |
| `--follow-symlinks`    | `false`     | Allow serving files reached via symlinks escaping the cwd.               |
| `--no-open-browser`    | `false`     | Don't open a browser window on startup.                                  |
| `--reload-debounce D`  | `250ms`     | Trailing-edge debounce window for live-reload file-change batching.      |
| `--no-livereload`      | `false`     | Disable live reload (no watcher, no script injection, 404 `/__livereload`). |
| `--no-transpile`       | `false`     | Disable on-the-fly TypeScript transpilation (serve `.ts/.tsx/.mts/.cts` raw). |
| `--version`            |             | Print the version and exit.                                              |

### Live reload

The server watches every directory under the cwd (minus `.git` and
`node_modules`) with `fsnotify`. Events are batched over a trailing-edge
debounce window (default `250ms`, configurable with `--reload-debounce`):
the window resets on every new event, so the reload fires after writes
stop, not on the first event. The batched change list is broadcast as a
Server-Sent Event, for example:

```
data: {"changes":[{"path":"index.html","op":"WRITE"}]}
```

Paths are relative to the serve root (forward-slash separated). The op
string is a `|`-joined list of the fsnotify bits that triggered the
event (`CREATE`, `WRITE`, `REMOVE`, `RENAME`, `CHMOD`). Pages pick up
the event via an injected `<script>` tag and call `location.reload()`.
Both the server console and the browser console log each changed file so
you can see exactly what caused a reload.

The injected tag is only added to responses with
`Content-Type: text/html`, right before `</body>` (or at the end if there
isn't one). Non-HTML responses are passed through untouched.

Pass `--no-livereload` to disable the watcher, skip script injection
entirely, and return `404` for the two `/__livereload` endpoints.

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
transpile.go    on-the-fly TypeScript -> JavaScript (esbuild)
livereload.go   SSE hub, HTML injection, reloadHub
safefs.go       symlink-containment http.FileSystem
watcher.go      recursive fsnotify tree + debounce
browser.go      per-OS browser launcher
```

[esbuild]: https://github.com/evanw/esbuild
