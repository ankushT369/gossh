# CLI reference

Run `gossh <mode> --help` for the usage text. Flags are parsed after the mode, for example
`gossh server --port 7777`.

## Shared flags

| Flag | Default | Description |
| --- | --- | --- |
| `-q`, `--quiet` | Info logging | Show errors only. |
| `-v`, `--verbose` | Info logging | Use info-level logging. |
| `-vv` | Info logging | Use debug-level logging. |
| `-vvv` | Info logging | Use verbose trace logging. |
| `--config <path>` | Auto-discovered | Read settings from a YAML or TOML file. See [Configuration](#configuration). |

The verbosity flags are mutually prioritized as `--quiet`, `-vvv`, `-vv`, then `-v` or
`--verbose`; when more than one is supplied, the first matching level in that order is used.
`-h` and `--help` print the usage text.

## `gossh server`

| Flag | Default | Description |
| --- | --- | --- |
| `--port <port>` | `7777` | HTTP and WebSocket listener port. |
| `--ssh <port>` | `22` | SSH daemon port that the server connects to. |
| `--daemon` | Off | Run the server in the background. |
| `--max-conns <n>` | Largest supported integer | Maximum concurrent connections. |
| `--web <origin>` | Disabled | Enable the browser terminal for the supplied HTTPS origin. |

## `gossh client`

| Flag | Default | Description |
| --- | --- | --- |
| `--connect <url>` | Required | Server URL to connect to. |
| `--port <port>` | `8888` | Local TCP port exposed for SSH clients. |
| `--daemon` | Off | Run the client in the background. |

Connect to the local tunnel with `ssh user@localhost -p 8888` (replace `user` with an account
on the remote SSH server).

## `gossh status`

| Flag | Default | Description |
| --- | --- | --- |
| `--target <url>` | Required | Server URL whose `/status` endpoint should be queried. |

## Other modes

- `gossh version` or `gossh --version` prints the installed version.
- `gossh upgrade` downloads and runs the current installer.

## Configuration

If `--config` is omitted, gossh checks these paths in order:

1. `gossh.yaml`, then `gossh.toml` in the current directory;
2. `~/.config/gossh/config.yaml`, then `config.toml`;
3. `/etc/gossh/config.yaml`, then `config.toml`.

A YAML or TOML file can set mode-specific values under `server`, `client`, or `status`.
Environment variables override file values; explicit command-line flags override environment
variables. Server settings include `port`, `ssh`, `daemon`, and `verbosity`; client settings
include `port`, `connect`, `daemon`, and `verbosity`; status accepts `target`.

For connection examples and browser access, see the [README](../README.md).
