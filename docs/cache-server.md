# Setting up a cache server (Harbor + Redis)

The distributed cache has two server-side halves, which fit comfortably on a
single small host:

- **An OCI registry** stores the `cache.url: oci://...` entries as
  chunk-deduplicated artifacts. This guide uses [Harbor](https://goharbor.io):
  unlike a plain CNCF `distribution` registry it has per-project access
  control, **tag retention policies** (the only expiry mechanism for the
  `oci://` backend, which has no TTL) and scheduled garbage collection. Any
  OCI registry works the same from the client's point of view.
- **A Redis instance** serves the `cache.lock: redis://...` distributed locks
  (and, optionally, `redis://` cache entries for small blobs).

Sizing: Harbor wants 4 GB of RAM and 2 vCPUs minimum, plus disk for the cache
volume (chunks are zstd-compressed and deduplicated, so plan for the size of
one full cache generation, not one per build). The lock Redis is capped at
64 MB and is negligible.

## Harbor: OCI cache storage

### Install

Harbor ships as a docker-compose bundle. On a host with docker and the
compose plugin:

```sh
curl -LO https://github.com/goharbor/harbor/releases/download/v2.13.0/harbor-offline-installer-v2.13.0.tgz
tar xzf harbor-offline-installer-v2.13.0.tgz && cd harbor
cp harbor.yml.tmpl harbor.yml
```

Edit `harbor.yml` — the relevant keys:

```yaml
hostname: harbor.example.com          # or the host IP
https:
  port: 443
  certificate: /etc/harbor/certs/harbor.crt
  private_key: /etc/harbor/certs/harbor.key
harbor_admin_password: <initial admin password>
data_volume: /srv/harbor              # blobs end up here
```

With a self-signed or private-CA certificate, keep the CA file around: every
client needs it (the `?ca=` URL parameter or `TASK_CACHE_OCI_CA`).

```sh
sudo ./install.sh
```

`install.sh` generates the compose file and starts Harbor; it is restarted on
boot through its own `docker-compose` restart policies.

### Project and robot account

In the Harbor UI (or via the API):

1. Create a **private project** named `task-cache`.
2. In the project, create a **robot account** (e.g. `ci`) with `pull` and
   `push` permission on repositories. The full username Harbor generates is
   `robot$task-cache+ci`; the secret is shown once.

The robot credentials are what CI uses — set them as (masked) CI variables,
not in the Taskfile (see below).

### Retention and garbage collection

The `oci://` backend never deletes anything: each cache key is a tag, and old
tags accumulate until the registry prunes them. Two scheduled jobs do that:

1. **Tag retention** (project → *Policy* → *Tag Retention*): add a rule such
   as "retain the artifacts pushed within the last 14 days" (or "retain the
   most recently pushed 50 artifacts") applied to all repositories of the
   project, and schedule it daily. Use the dry-run button to check the rule
   before letting it delete.
2. **Garbage collection** (*Administration* → *Clean Up* → *Garbage
   Collection*): retention only deletes manifests; GC is what reclaims the
   chunk blobs no longer referenced by any manifest. Schedule it (e.g.
   weekly), with *delete untagged artifacts* enabled.

## Redis: distributed locks

Run a **dedicated, minimal** Redis for the locks — do not reuse Harbor's
internal one (it is not exposed and is sized for Harbor's own job queues).
The configuration is deliberately spartan, because locks are short-TTL leases:

- **no persistence** (`save ""`, `appendonly no`): losing locks on a restart
  just makes waiters re-acquire them;
- **small memory cap with `noeviction`**: evicting a lock key would silently
  break mutual exclusion — better to refuse writes;
- **password auth**: the lock URL embeds it.

```sh
PASS=$(openssl rand -hex 24)
mkdir -p /etc/redis-lock
cat >/etc/redis-lock/redis.conf <<EOF
requirepass $PASS
save ""
appendonly no
maxmemory 64mb
maxmemory-policy noeviction
EOF
docker run -d --name redis-lock --restart always \
    -p 6379:6379 \
    -v /etc/redis-lock/redis.conf:/usr/local/etc/redis/redis.conf:ro \
    redis:8.0 redis-server /usr/local/etc/redis/redis.conf
```

The resulting lock URL is `redis://:$PASS@<host>:6379`. Smoke test (compare
the replies — `redis-cli` exits 0 even on `NOAUTH`/`WRONGPASS` error replies):

```sh
docker exec -e REDISCLI_AUTH=$PASS redis-lock redis-cli ping          # PONG
docker exec redis-lock redis-cli ping                                 # NOAUTH error
docker exec -e REDISCLI_AUTH=$PASS redis-lock redis-cli set l 1 NX PX 5000  # OK
```

## Wiring it into a Taskfile

```yaml
tasks:
  build:
    sources:
      - src/**
    generates:
      - dist/**
    cache:
      enabled: '{{ne .CI_CACHE_REDIS_URL ""}}'
      url: 'oci://harbor.example.com/task-cache/build:{{urlsafe .TASK}}-{{.CHECKSUM}}'
      lock: 'redis://{{.CI_CACHE_REDIS_URL}}/lock:{{urlsafe .TASK}}-{{.CHECKSUM}}'
    cmds:
      - ./build.sh
```

Notes:

- In Harbor the repository path must start with the project name:
  `task-cache/build` is the repository `build` in the project `task-cache`.
  The tag carries the cache key (`[A-Za-z0-9._-]`, 128 chars max).
- Keep the registry credentials out of the Taskfile: export
  `TASK_CACHE_OCI_USER`, `TASK_CACHE_OCI_PASSWORD` and `TASK_CACHE_OCI_CA`
  in the environment (masked CI variables, with the CA as a *file* variable).
  The robot username contains a `$`, so single-quote it in shell:
  `export TASK_CACHE_OCI_USER='robot$task-cache+ci'`.
- The Redis URL (with its password) should likewise come from a masked CI
  variable, e.g. `CI_CACHE_REDIS_URL=:<password>@<host>:6379`.

## Verifying the setup

Run a cached task twice — the first run pushes, the second restores without
executing:

```sh
task build        # task: "build" saved to cache (pushed 42/42 chunks, 13.5 MB)
rm -rf dist .task
task build        # task: "build" restored from cache
```

The pushed entries are visible with `oras` or in the Harbor UI:

```sh
oras repo tags --ca-file harbor-ca.crt -u 'robot$task-cache+ci' \
    harbor.example.com/task-cache/build
```
