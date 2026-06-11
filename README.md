# Task (WALLIX fork)

Fork of [go-task/task](https://github.com/go-task/task) (v3.49.1) with opinionated changes focused on build-system reliability: deterministic fingerprinting, distributed caching and locking, and setup tasks.

Source: [github.com/wallix/task](https://github.com/wallix/task)

## Changes from upstream

### Removed

- **Remote taskfiles** -- `http://` and `git://` includes are no longer supported. Related CLI flags (`--download`, `--offline`, `--insecure`, `--timeout`, `--clear-cache`, `--trusted-hosts`, `--expiry`, `--remote-cache-dir`, `--cacert`, `--cert`, `--cert-key`) have been removed.
- **Timestamp fingerprinting** -- only checksum-based fingerprinting remains. The `method` field on tasks is removed.
- **`none` fingerprint method** -- tasks either use checksum fingerprinting or have no `sources`.

### Added

#### Setup tasks

A new `setup` field runs tasks **unconditionally and sequentially** before deps and fingerprint checks. Unlike deps, setup tasks always run regardless of whether the parent is up-to-date, and they do **not** affect the parent's fingerprint. Use `run: once` to avoid re-executing shared setup tasks.

```yaml
tasks:
  enforce-version:
    run: once
    cmds:
      - date +%Y-%m-%d > version.txt

  build:
    setup:
      - enforce-version
    sources:
      - version.txt
      - src/**/*.go
    generates:
      - bin/app
    cmds:
      - go build -ldflags "-X main.buildDate=$(cat version.txt)" -o bin/app .
```

#### Fingerprint-based generates

For large generated directories where hashing every file is expensive, a `generates` entry can specify a **fingerprint** file -- a single representative file used for checksum-based up-to-date detection instead of hashing every file matched by the glob. The full glob is still used for cache operations (save/restore), so all files are archived correctly.

Four YAML forms are supported in `sources` and `generates`:

```yaml
generates:
  # Scalar: simple glob pattern (hashes all matched files)
  - "build/**/*"

  # Exclude: negated pattern
  - exclude: "build/tmp/**"

  # Glob + fingerprint: the glob defines the full set of files for caching,
  # while fingerprint names a single file for up-to-date checks.
  - glob: "node_modules/**/*"
    fingerprint: "node_modules/.yarn-state.yml"

  # From: inherit entries from related tasks (see "Inherited sources/generates")
  - from: deps
```

**Example: yarn install with fingerprint**

```yaml
tasks:
  install:
    sources:
      - package.json
      - yarn.lock
    generates:
      - glob: "node_modules/**/*"
        fingerprint: "node_modules/.yarn-state.yml"
    cmds:
      - yarn install --immutable
```

Here `node_modules/` may contain thousands of files, but only `.yarn-state.yml` is hashed for staleness checks. When caching is enabled, the full `node_modules/**/*` glob (plus the fingerprint dotfile) is archived.

**Example: mixed generates with caching**

```yaml
tasks:
  build:
    sources:
      - src/**/*.ts
      - package.json
    generates:
      - "dist/**/*"
      - glob: "node_modules/**/*"
        fingerprint: "node_modules/.yarn-state.yml"
      - exclude: "dist/tmp/**"
    cache:
      url: 'file:///tmp/cache/build-{{.CHECKSUM}}.zip'
    cmds:
      - npm run build
```

#### Inherited sources/generates (`from: deps` and `from: cmds`)

Wrapper tasks can inherit `sources` and `generates` from their dependencies or cmd task-calls using the `from:` directive. This avoids duplicating glob patterns across tasks and ensures cache keys reflect the full input/output set. Entries are deduplicated automatically.

**`from: deps`** — copies entries from all direct dependencies:

```yaml
tasks:
  all:
    sources:
      - from: deps
    generates:
      - from: deps
    cache:
      url: 'file:///tmp/cache/all-{{.CHECKSUM}}.zip'
    deps:
      - build-a
      - build-b

  build-a:
    sources: [src/a/**/*.go]
    generates: [bin/a]
    cmds: [go build -o bin/a ./cmd/a]

  build-b:
    sources: [src/b/**/*.go]
    generates: [bin/b]
    cmds: [go build -o bin/b ./cmd/b]
```

**`from: cmds`** — copies entries from all cmd task-calls:

```yaml
tasks:
  build:
    sources:
      - from: cmds
    generates:
      - from: cmds
    cmds:
      - task: compile
      - task: link

  compile:
    sources: [src/**/*.c]
    generates: [build/**/*.o]
    cmds: [make compile]

  link:
    sources: [build/**/*.o]
    generates: [bin/app]
    cmds: [make link]
```

Literal globs and `from:` entries can be mixed freely:

```yaml
sources:
  - config.yml        # own source
  - from: deps        # plus all dep sources
```

#### Per-task cache block (`file://`, `redis://` and `oci://` backends)

Cache generated files so that subsequent runs (or other machines) can skip execution entirely. The `url` and `lock` fields are Go templates with access to all task variables plus `{{.CHECKSUM}}` (SHA256 of sources, commands, and generates).

```yaml
tasks:
  build:
    sources:
      - src/**/*.go
    generates:
      - bin/app
    cache:
      enabled: '{{ne .REDIS_URL ""}}'         # optional, template bool
      url: 'file:///tmp/cache/build-{{.CHECKSUM}}.zip'
      lock: 'redis://{{.REDIS_URL}}/lock:build-{{.CHECKSUM}}'
    cmds:
      - go build -o bin/app .
```

**OCI registry backend.** With an `oci://` URL the entry is stored as an OCI artifact: files are cut into content-defined chunks (FastCDC), compressed with zstd, and pushed as individual blobs that the registry deduplicates by digest. Saving a slightly changed `node_modules` or VM image only uploads the new chunks; pulls go through a local chunk store so repeated restores are incremental. Entries expire through the registry's retention policy (no TTL).

```yaml
cache:
  url: 'oci://registry.example.com/task-cache:{{urlsafe .TASK}}-{{.CHECKSUM}}?ca=/etc/ssl/registry-ca.crt'
```

The URL shape is `oci://[user:password@]host/repo:tag[?ca=<file>][&cas=<dir>][&plainhttp=1]` (the tag carries the cache key and is limited to `[A-Za-z0-9._-]`). Credentials and trust can also come from the environment — `TASK_CACHE_OCI_USER`, `TASK_CACHE_OCI_PASSWORD`, `TASK_CACHE_OCI_CA` and `TASK_CACHE_OCI_CAS_DIR` — keeping secrets out of the Taskfile.

See [docs/cache-server.md](docs/cache-server.md) for setting up the server side (a Harbor registry for the cache entries plus a Redis for the distributed locks).

#### Filesystem-based locking

Tasks with `sources` and `generates` automatically acquire a POSIX advisory file lock (stored in `.task/`). The lock key is `taskname:sourcehash`, so different source states don't contend on the same lock.

#### Redis-based distributed locking

When `cache.lock` evaluates to a `redis://` URL, locking is distributed across machines using Redis `SET NX EX` with TTL-based heartbeat renewal.

#### `urlsafe` template function

`{{urlsafe .TASK}}` percent-encodes a string for use in URLs, replacing special characters like colons from namespaced task names. Useful in cache URLs:

```yaml
cache:
  url: echo "redis://$REDIS_URL/cache:{{urlsafe .TASK}}/$TASK_CACHE_HASH"
```

#### `--status` flag

Show fingerprint status of tasks without running them:

```bash
task --status build           # human-readable
task --status --json build    # machine-readable
```

#### `--export-cache` and `--import-cache`

Portable fingerprint state for CI/CD pipelines:

```bash
# On build machine
task --export-cache state.zip build test

# On CI machine
task --import-cache state.zip
```

Exports checksum state and generated files for up-to-date tasks as a ZIP archive.

### Changed

- **`--force` is now gentle by default** -- `--force` only forces the directly called task; dependent tasks still check their status. Use `--force-all` to force everything (previously the default `--force` behavior). The `TASK_X_GENTLE_FORCE` experiment flag has been removed.

### Improved

- **Richer fingerprints** -- checksums now include serialized commands and variable data, not just file contents.
- **Separate staleness reporting** -- `sources` and `generates` staleness is tracked and reported independently.

## Execution pipeline

```
setup tasks (unconditional, sequential)
  -> acquire lock (file or redis)
  -> run deps (parallel)
  -> check fingerprint (sources + generates, including from: resolution)
     -> try restore from cache (file://, redis:// or oci://)
     -> if miss: execute task, then save to cache
  -> release lock
```
