# admin-bot

admin-bot is a single-binary Go service that makes host management easier in confined
networks. It serves static content over HTTP and acts as a caching forward proxy,
so machines without direct internet access can reach packages and files through it.

## Features

- **Static file serving** — expose local directories under `/static/<name>/`.
- **Forward proxy** — proxies plain HTTP requests and `CONNECT` tunnels.
- **Response caching** — caches responses for allow-listed domains with TTL-based expiry.
- **Hot config reload** — edits to `config.yaml` apply without restarting the process;
  only the services whose settings changed are restarted.
- **Background cache cleanup** — a worker periodically deletes expired cache entries.

## Quick start

```sh
make build          # produces bin/admin-bot
./bin/admin-bot     # runs with ./config.yaml by default
```

### Flags and environment

| Flag | Env | Description |
|------|-----|-------------|
| `-config <path>` | `ADMINBOT_CONFIG_PATH` | Path to the YAML config file (`./config.yaml` by default; flag wins over env) |
| `-validate <path>` | — | Validate a config file and exit |

## Configuration

```yaml
# Main HTTP server configuration
http:
  enabled: true            # default: true
  addr: "0.0.0.0"          # default: "0.0.0.0"
  port: 8080               # default: 8080

  # --- Static file serving ---
  static:
    enabled: true
    dirs:
      files-ubuntu:                          # route: /static/files-ubuntu/
        path: "/var/www/static-files-ubuntu"
      files-rhel:
        path: "/var/www/static-files-rhel"

  # --- Forward proxy ---
  forward-proxy:
    enabled: true
    cache:
      enabled: true
      cache-dir: "/var/cache/admin-bot/forward-proxy-cache"  # required when cache.enabled
      cache-ttl: "7d"                                        # supports s/m/h and d/w units
    domains:             # exact-match, case-insensitive; requests to these are cached
      - "github.com"
      - "pypi.org"
      - "download.docker.com"

# --- Background proxy cache cleanup ---
proxy-cache-cleanup:
  interval: "1h"           # default: "1h"; must be positive
```

Defaults apply when keys are omitted. Validation errors are reported together, so a
bad config shows every problem at once — run `-validate` to check before deploying.

Durations accept Go units (`30s`, `10m`, `1h`) plus day/week shorthand (`7d`, `2w`).

## How caching works

Responses from allow-listed domains are stored on disk (status, headers, and body)
and served on subsequent requests until the TTL expires, with an `X-Cache-Status`
header of `HIT`, `MISS`, or `BYPASS` per response. A cleanup worker removes expired
entries at the configured interval. Only successful (2xx) responses are cached.

## Development

```sh
make test        # go test ./pkg/... -v
make lint        # golangci-lint run ./...
make build       # build into ./bin
make docker-dev  # hot-reload dev container via air
```

Project layout:

```
cmd/                 entrypoint, flags, service lifecycle and reload orchestration
pkg/config/          config loading, defaults, validation (viper + fsnotify)
pkg/httpserver/      top-level server wiring CONNECT vs. regular requests
pkg/staticfiles/     static route registration
pkg/forwardproxy/    CONNECT tunneling, origin fetches, disk cache
pkg/cachecleaner/    periodic expired-entry removal
```
