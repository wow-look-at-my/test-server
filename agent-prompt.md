# Agent prompt: fix go-toolchain distributed build-cache corruption

Copy everything in the fenced block below into the new agent.

---

```
You are fixing an infrastructure bug in the go-toolchain distributed build
cache. This is NOT a bug in any application repo; do not "fix" application code.

## What happened

CI for `wow-look-at-my/test-server` (workflow `.github/workflows/ci.yml`, which
runs `wow-look-at-my/go-toolchain@v1`) failed at the `go vet ./...` stage with:

    Error: vet failed: package load errors:
    browser.go:35:25: undefined: runtime
    browser.go:6:2: "runtime" imported as reflectlite and not used

The file is correct: it imports the stdlib package `runtime` (line 6) and uses
`runtime.GOOS` (line 35). The same tree compiles, vets, tests, and builds GREEN
locally and on `master`. The failing run is `27175109628`, commit `deda737`.

## Diagnosis (already established, high confidence)

`"runtime" imported as reflectlite and not used` is the signature of build-cache
cross-contamination: the loader asked the cache for the compiled object of
import path `runtime` and got back an object whose declared package name is
`reflectlite` (i.e. `internal/reflectlite`). Hence `runtime` looked "unused" and
`runtime.GOOS` looked "undefined".

go-toolchain backs GOCACHE with a custom `cacheprog` that reads/writes a SHARED
REMOTE cache. The CI log shows:

    cacheprog: local cache: FUSE virtual filesystem (mount=.../buildcache/mnt ...)
    cacheprog: web index: 1001621 keys
    cacheprog: remote enabled endpoint=https://s3.pazer.io

Critically, the bad object was served as a SUCCESSFUL hit: the cacheprog summary
reported no detected errors (decompress=0 checksum=0 network=0). So a wrong
object is being mapped to / stored under the `runtime` package's cache key, and
nothing on the READ path catches it.

## Your job

Work in `wow-look-at-my/go-toolchain` (and, if applicable, the remote cache
backend / cacheprog implementation). Concretely:

1. CONFIRM SCOPE FIRST. Locate the cacheprog implementation and the remote
   (S3-backed) cache layer in go-toolchain. Read how it maps Go's GOCACHE
   protocol (actionID -> outputID -> object) onto local FUSE + remote S3.
   Confirm the diagnosis before changing anything.

2. REPRODUCE. Re-run CI on commit `deda737` of `wow-look-at-my/test-server`, or
   push a commit whose `go vet` loads the `runtime` package, and capture the
   `imported as reflectlite` failure. Inspect the cacheprog mapping for the
   `runtime` package's action ID and verify whether its output points at the
   `internal/reflectlite` object (or another wrong object).

3. ROOT CAUSE. Determine HOW the wrong object got associated with the key:
   - actionID/outputID/object mapping bug (e.g. a races or last-writer-wins bug
     in the FUSE or S3 layer that swaps objects between concurrent compiles),
   - cache-key collision between distinct packages,
   - or a truncated/misindexed write that the read path trusts.

4. FIX, with a read-path integrity guard at minimum: when cacheprog returns a
   compiled package object, verify it actually corresponds to the requested key
   (e.g. validate the export-data package path/name matches the requested import
   path, and/or verify the stored object's checksum against the index). On
   mismatch, treat it as a MISS (force recompute) and evict/repair the bad
   entry instead of serving poison. Also fix the underlying mapping/collision
   bug you found in step 3 so poison stops being written.

5. ADD A REGRESSION GUARD. A test that stores a deliberately mismatched
   object under a key and asserts cacheprog rejects it on read (miss + evict),
   so this class of corruption can never be served silently again.

6. PROVIDE A PURGE PATH + MITIGATION. Add/confirm a documented way to evict a
   poisoned key, and evict the currently-poisoned `runtime` (and any sibling
   stdlib) entries so existing PRs unblock.

## Constraints / house rules

- Use `go-toolchain` (no bare `go build`/`go test`) for any Go work; accept and
  commit its auto-formatting/tidy changes.
- Do NOT modify or "work around" this in application repos. The fix is in the
  cache layer.
- Prove the fix: reproduce the failure, apply the fix, then show the same
  scenario now MISSES + recomputes instead of serving the wrong object.
- Keep changes ASCII; follow the repo's existing CI conventions.

## Useful references

- Failing repo/run: wow-look-at-my/test-server, run 27175109628, commit deda737.
- Symptom file: browser.go (imports "runtime", uses runtime.GOOS) -- correct.
- Remote cache endpoint seen in logs: https://s3.pazer.io ; web index ~1,001,621
  keys; local FUSE mount under .../go-toolchain/buildcache/mnt.
- A fuller incident writeup lives at test-server/BUILD_CACHE_CORRUPTION.md.
```
