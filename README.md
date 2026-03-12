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

#### `--status` flag

Show fingerprint status of tasks without running them:

```bash
task --status build           # human-readable
task --status --json build    # machine-readable
```

### Improved

- **Richer fingerprints** -- checksums now include serialized commands and variable data, not just file contents.
- **Separate staleness reporting** -- `sources` and `generates` staleness is tracked and reported independently.
