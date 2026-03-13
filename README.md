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

A new `setup` field runs tasks **unconditionally and sequentially** before deps and fingerprint checks. Setup tasks' sources and commands are merged into the parent task's fingerprint.

```yaml
tasks:
  enforce-version:
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

#### Per-task cache block (`file://` and `redis://` backends)

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

#### Filesystem-based locking

Tasks with `sources` and `generates` automatically acquire a POSIX advisory file lock (stored in `.task/`) to prevent concurrent execution of the same task.

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

### Improved

- **Richer fingerprints** -- checksums now include serialized commands and variable data, not just file contents.
- **Separate staleness reporting** -- `sources` and `generates` staleness is tracked and reported independently.

## Execution pipeline

```
setup tasks (unconditional, sequential)
  -> merge setup fingerprints into parent sources
  -> acquire lock (file or redis)
  -> run deps (parallel)
  -> check fingerprint
     -> try restore from cache (file:// or redis://)
     -> if miss: execute task, then save to cache
  -> release lock
```
