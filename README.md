# Task (WALLIX fork)

Fork of [go-task/task](https://github.com/go-task/task) (v3.49.1) with opinionated changes focused on build-system reliability: deterministic fingerprinting, distributed caching and locking, and setup tasks.

Source: [github.com/wallix/task](https://github.com/wallix/task)

## Changes from upstream

### Removed

- **Remote taskfiles** -- `http://` and `git://` includes are no longer supported. Related CLI flags (`--download`, `--offline`, `--insecure`, `--timeout`, `--clear-cache`, `--trusted-hosts`, `--expiry`, `--remote-cache-dir`, `--cacert`, `--cert`, `--cert-key`) have been removed.
