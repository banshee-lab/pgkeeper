# Environment variables

pgkeeper reads environment variables from its process environment. It does not
load `.env` files itself. For TOML values, variable expansion happens after
parsing and applies to string values in the main configuration and included
files.

Use `${NAME}` to require a variable; configuration loading fails if it is
unset. Use `${NAME:-default}` to supply a fallback when it is unset **or
empty**. These references only substitute values into TOML; they do not
automatically map environment variables to configuration options. For example,
set `PGKEEPER_DB_PATH` in the TOML as `${PGKEEPER_DB_PATH:-/var/lib/pgkeeper/pgkeeper.db}`
to make it configurable this way.

## Variables used by the executable and example configuration

| Variable | Where it is used | Behavior |
| --- | --- | --- |
| `PGKEEPER_CONFIG` | Executable | Selects the TOML file if `-config` is not supplied. Defaults to `/etc/pgkeeper/pgkeeper.toml`. A non-empty `-config` flag takes precedence. |
| `PGKEEPER_TEMP_PATH` | `examples/pgkeeper.toml` | Example reference for `global.temp_path`; defaults to `/var/tmp`. |
| `PGKEEPER_LOG_PATH` | `examples/pgkeeper.toml` | Example reference for `global.log_path`; defaults to `/var/log`. |
| `PGKEEPER_DB_PATH` | `examples/pgkeeper.toml` | Example reference for `global.db_path`; defaults to `/var/lib/pgkeeper/pgkeeper.db`. |
| `TG_BOT_TOKEN` | `examples/pgkeeper.toml` | Example reference for `[telegram].bot_token`; required if that example setting is used. |
| `TG_CHAT_ID` | `examples/pgkeeper.toml` | Example reference for `[telegram].chat_id`; required if that example setting is used. |
| `OIDC_SECRET` | `examples/pgkeeper.toml` | Example reference for `[auth].secret`; required if that example setting is used. |
| `RESTIC_PASSWORD` | `examples/pgkeeper.toml` | Example reference for `[restic].password`; required for backups using that restic configuration. |

`PGKEEPER_CONFIG` is read directly by the executable, while
`PGKEEPER_TEMP_PATH`, `PGKEEPER_LOG_PATH`, and `PGKEEPER_DB_PATH` only affect
configuration when referenced in TOML. `TG_BOT_TOKEN`,
`TG_CHAT_ID`, `OIDC_SECRET`, and `RESTIC_PASSWORD` are names used by the
example; a different TOML file can use different variable names.

All other `${NAME}` references in TOML are also sourced from the process
environment, so deployments can define additional variables without changes
to pgkeeper. A variable without a default is required even if its corresponding
feature is optional but its TOML section is present.

Example:

```sh
export PGKEEPER_CONFIG=/etc/pgkeeper/pgkeeper.toml
export TG_BOT_TOKEN='replace-me'
export TG_CHAT_ID='replace-me'
export OIDC_SECRET='replace-me'
export RESTIC_PASSWORD='replace-me'
```

Do not put real secrets in version-controlled configuration or command-line
arguments. Restrict access to process environment files and configuration
files, and make sure secrets do not appear in deployment logs.
