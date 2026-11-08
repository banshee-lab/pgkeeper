# Production deployment

This guide describes a single-host Linux deployment using a systemd service
and a TLS-terminating reverse proxy. The repository currently does not include
a backend Dockerfile, Compose deployment, or systemd unit; build and install
the binary and create the service configuration as described below.

## Runtime requirements

- Linux host with systemd and a reachable Docker daemon.
- Docker CLI (`docker`) on `PATH`; access to the daemon socket used by that
  CLI. pgkeeper invokes `docker exec` against running PostgreSQL containers.
- `restic` version 0.16 or later on `PATH`.
- Persistent writable storage for the SQLite database and activity logs, plus
  adequate temporary space for database dumps and the restic cache.
- A TLS reverse proxy for HTTPS and an OIDC provider if the API is used.
- PostgreSQL containers with `pg_dump` and, when `roles = true`, `pg_dumpall`.
  Configure PostgreSQL authentication in the containers for non-interactive
  use; pgkeeper does not pass or store PostgreSQL passwords.

The account that can access the Docker daemon can generally control the host.
Adding the service account to the `docker` group has effectively root-level
security implications. Use a dedicated host and an access model appropriate
for this trust boundary.

## Install the binary

Build using the Go version required by `go.mod` and install the executable:

```sh
go build -o pgkeeper ./cmd/pgkeeper
sudo install -o root -g root -m 0755 pgkeeper /usr/local/bin/pgkeeper
```

Install Docker CLI and restic separately using your operating system's package
manager or your organization's software distribution process. Confirm both
commands are available to the systemd service account.

## Create the service account and persistent directories

Create a dedicated `pgkeeper` service account and directories. Adjust ownership
and paths to match the configuration you choose:

```sh
sudo useradd --system --home-dir /var/lib/pgkeeper --create-home --shell /usr/sbin/nologin pgkeeper
sudo install -d -o pgkeeper -g pgkeeper -m 0750 /etc/pgkeeper
sudo install -d -o pgkeeper -g pgkeeper -m 0750 /var/lib/pgkeeper
sudo install -d -o pgkeeper -g pgkeeper -m 0750 /var/log/pgkeeper
sudo install -d -o pgkeeper -g pgkeeper -m 0700 /var/tmp/pgkeeper
```

The service account must be able to create the database and its SQLite
sidecar files in the database directory, create per-database activity log
files, and write temporary dumps and restic cache data. The configured
`global.temp-path`, `global.log-path`, and parent of `global.db-path` must
exist and be writable at startup.

## Configure pgkeeper

Install `/etc/pgkeeper/pgkeeper.toml` with restrictive permissions and define
at least one `[backup.<name>]` and its restic repository and password. For
production, configure `[auth]` and use HTTPS so the secure session cookie is
sent by browsers. Register the exact public callback URL
`https://pgkeeper.example.com/auth/callback` with the OIDC provider and set
`auth.redirect-url` to the same URL.

For example, the following environment file provides values referenced from
TOML:

```text
RESTIC_PASSWORD=replace-with-a-secret
OIDC_SECRET=replace-with-a-secret
TG_BOT_TOKEN=replace-with-a-secret
TG_CHAT_ID=replace-with-your-chat-id
```

This file is optional if all values are supplied another way. Set its
permissions so only root and the service account can read it:

```sh
sudo chown root:pgkeeper /etc/pgkeeper/pgkeeper.env
sudo chmod 0640 /etc/pgkeeper/pgkeeper.env
sudo chmod 0640 /etc/pgkeeper/pgkeeper.toml
```

The TOML should bind the HTTP server to loopback when the reverse proxy is on
the same host:

```toml
[global]
listen    = "127.0.0.1:8080"
db-path   = "/var/lib/pgkeeper/pgkeeper.db"
log-path  = "/var/log/pgkeeper"
temp-path = "/var/tmp"

[auth]
issuer       = "https://idp.example.com/realms/production"
client-id    = "pgkeeper"
secret       = "${OIDC_SECRET}"
redirect-url = "https://pgkeeper.example.com/auth/callback"
cookie-secure = true

[restic]
repository = "/srv/restic"
password   = "${RESTIC_PASSWORD}"

[backup.production]
cron      = "@daily"
container = "postgres"
database  = "app"
```

Add your actual retention policy, Telegram settings, include files, and backup
entries as needed. Ensure the restic repository storage is durable and has
capacity for expected growth. Set `auth.cookie-secure = true` (the default)
when using HTTPS; do not disable it for a public deployment. If the OIDC
provider or any restic/Docker dependency is unavailable, inspect the service
logs and run the checks below before serving traffic.

## Configure systemd

Create `/etc/systemd/system/pgkeeper.service`. If the service account needs
Docker socket access, configure `SupplementaryGroups` for the host's Docker
group, understanding the security implications above.

```ini
[Unit]
Description=pgkeeper PostgreSQL backup service
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=pgkeeper
Group=pgkeeper
SupplementaryGroups=docker
EnvironmentFile=-/etc/pgkeeper/pgkeeper.env
ExecStart=/usr/local/bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml
Restart=on-failure
RestartSec=5s
TimeoutStopSec=60s
UMask=0077

[Install]
WantedBy=multi-user.target
```

If the host has no `docker` group or uses a different Docker access model,
adapt or remove `SupplementaryGroups`. The service must still be able to
connect to the Docker daemon socket. The `EnvironmentFile` is systemd syntax,
not a file loaded by pgkeeper directly.

Enable the service and verify its startup:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now pgkeeper
sudo systemctl status pgkeeper
sudo journalctl -u pgkeeper -n 100 --no-pager
```

Before enabling scheduled backups, validate the configuration and test
dependencies and notifications. The systemd `EnvironmentFile` is not inherited
by an interactive `sudo -u` command, so run these checks in transient systemd
units with the same environment and Docker group as the service:

```sh
run_pgkeeper() {
  sudo systemd-run --wait --pipe --collect \
    --property=User=pgkeeper \
    --property=Group=pgkeeper \
    --property=SupplementaryGroups=docker \
    --property=EnvironmentFile=-/etc/pgkeeper/pgkeeper.env \
    /usr/local/bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml "$@"
}
run_pgkeeper -check
run_pgkeeper -check-restic
run_pgkeeper -notify-test
run_pgkeeper -run production
```

Adjust the Docker group property if the service uses a different access model.
The last command runs a real backup; use it only after confirming the target
container, database, repository, retention settings, and available storage.
Check the activity log and restic snapshots, then verify `/healthz` through
the local listener and the public reverse proxy.

## Reverse proxy and operations

Expose the service only through a TLS-enabled reverse proxy. Keep the listener
bound to loopback unless there is a specific protected network design. The
proxy should:

- Forward the original host and HTTPS scheme to the application, as required
  by the OIDC callback and cookie flow.
- Permit long-lived `text/event-stream` connections and disable response
  buffering for SSE routes (`/api/v1/jobs/stream` and
  `/api/v1/activities/*/log/stream`).
- Allow enough read time for long-lived SSE requests and avoid caching API or
  event-stream responses.

After changing environment values or `global.*`, `[telegram]` or `[auth]`
settings, restart the service. Other configuration changes can be applied
without a restart using `pgkeeper -reload` (or `systemctl kill -s HUP
pgkeeper`, `docker exec pgkeeper pgkeeper -reload`). Monitor structured application logs through
the system journal and execution log files under `global.log-path`. Back up
the SQLite database and execution logs according to operational needs, and
ensure the restic repository itself has an independent durability and recovery
plan. pgkeeper does not automatically prune activity records or execution log
files.
