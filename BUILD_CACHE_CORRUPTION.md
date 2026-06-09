# Incident: go-toolchain distributed build cache served corrupted `runtime` package data

## TL;DR

CI for `wow-look-at-my/test-server` failed at the `go vet` stage with a bogus
error claiming the standard-library `runtime` package is undefined and was
"imported as reflectlite". The repository code is correct and builds/tests
green both locally and on `master`. The failure is a **poisoned/cross-
contaminated entry in the go-toolchain distributed build cache** (the
`cacheprog` + S3 layer), not a defect in this repo.

This is an infrastructure bug in `wow-look-at-my/go-toolchain` (its build-cache
layer), **not** in `test-server`.

## Symptom

- Workflow: `.github/workflows/ci.yml` -> `wow-look-at-my/go-toolchain@v1`
- Failing run: `27175109628`
- Commit: `deda737` (branch `claude/zealous-cerf-c6jxt5`)
- Stage: `go vet ./...` (run by go-toolchain before the build matrix)
- Exit code: 1, run duration ~36s

Exact error from the CI log:

```
vet: loaded 225 packages (1007 files parsed) in 6.565s
Error: vet failed: package load errors:
browser.go:35:25: undefined: runtime
browser.go:6:2: "runtime" imported as reflectlite and not used
browser.go:35:25: undefined: runtime
browser.go:6:2: "runtime" imported as reflectlite and not used
```

`browser.go` is trivially correct and unchanged in this branch:

```go
import (
    "log"
    "os/exec"
    "runtime"          // line 6
)
...
argv := browserCommand(runtime.GOOS, url)   // line 35
```

## Why this is a cache problem, not a code problem

1. **`browser.go` is unmodified.** The branch diff only touches `root.go`
   (MIME registration), the test files, `README.md`, and `go.mod`/`go.sum`.
   Nothing touches `browser.go` or anything related to `runtime`.
2. **`master` CI is green** (run for commit #12 `25277585491`).
3. **Local `go-toolchain` passes fully** on this exact tree: `go vet` clean,
   all tests pass (82.9% coverage), build succeeds. A second local run reports
   "Up to date, nothing to do".
4. **The error is on a stdlib import (`runtime`)** that has nothing to do with
   any change in the branch. A real source error here is impossible -- every
   Go program that calls `runtime.GOOS` would fail.

## Root-cause analysis

The message `"runtime" imported as reflectlite and not used` is the
fingerprint of build-cache cross-contamination. It means: when the
compiler/loader resolved the import path `runtime`, the object/export data it
received was that of a **different** package whose declared package name is
`reflectlite` (i.e. `internal/reflectlite`). The loader therefore (a) saw the
package named `reflectlite` instead of `runtime`, so the `runtime` identifier
was "unused", and (b) could not find `runtime.GOOS`, so `runtime` was
"undefined".

go-toolchain backs `GOCACHE` with a custom `cacheprog` that reads/writes a
**shared remote cache**:

```
cacheprog: local cache: FUSE virtual filesystem (mount=.../buildcache/mnt ...)
cacheprog: web index: 1001621 keys
cacheprog: remote enabled endpoint=https://s3.pazer.io
```

The corruption was served as a successful cache hit -- the cacheprog summary
reported **no** detected read errors (`decompress=0 checksum=0 network=0`).
That points at one of:

- A wrong object stored under (or mapped to) the action/output ID that the
  `runtime` package compile looks up -- i.e. an actionID -> outputID or
  outputID -> object mapping that points the `runtime` key at the
  `internal/reflectlite` object.
- A cache-key collision between two stdlib package builds.
- Missing integrity verification on the **read** path: cacheprog returns the
  object without checking that the export data's package name / import path
  matches what was requested, so a bad mapping is served silently.

Because `master` was cached before the poison (or under different keys) it
still passes; unrelated PRs that cause `vet` to load `runtime` against the
poisoned key fail with this confusing error.

## Impact

- Nondeterministic CI failures that look like compile errors in innocent files
  (`browser.go` here, but it could surface in any file importing `runtime`).
- Blocks unrelated PRs at the `vet` stage. Re-running the same commit may keep
  hitting the same poisoned entry if it is stored remotely (content-addressed
  reads are deterministic).

## Immediate mitigation (to unblock PRs)

- Evict/invalidate the poisoned `runtime` (and any sibling stdlib) entries from
  the remote cache, OR
- Run go-toolchain once with the remote cache disabled / bypassed so the
  stdlib objects are recompiled and the good objects are re-published.

## Real fix (belongs in `wow-look-at-my/go-toolchain`)

- Add integrity verification on the cacheprog **read** path: when returning a
  compiled package object, verify the export data's package path/name matches
  the requested import path; treat a mismatch as a cache miss (recompute) and
  log/evict the bad entry.
- Audit cache-key derivation for compiled packages to rule out collisions
  between distinct stdlib packages (e.g. `runtime` vs `internal/reflectlite`).
- Add a self-check / poison-detection pass and a documented way to purge a key.

## Reproduction pointers for the fixer

- Re-run CI on commit `deda737` (or push any commit whose `go vet` loads the
  `runtime` package) and watch for `"runtime" imported as reflectlite`.
- Inspect the cacheprog mapping for the `runtime` package action ID on the
  affected runner / in the S3 backend (`https://s3.pazer.io`, index
  ~1,001,621 keys) and confirm whether its output ID points at the
  `internal/reflectlite` object.
