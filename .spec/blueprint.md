# blueprint — admin-bot design blueprint

| | |
|---|---|
| Status | Draft |
| Domain | admin-bot |
| Version | 0.2.0 |

## Purpose

admin-bot is a single-binary Go service for host management in confined networks.
It serves static content over HTTP and acts as a caching forward proxy, so machines
without direct internet access can reach packages and files through it.

Two capabilities, one listener:

1. **Static file serving** — local directories exposed under `/static/<name>/`.
2. **Forward proxy** — plain-HTTP proxying plus `CONNECT` tunneling, with
   TTL-based disk caching restricted to an allow-list of domains.

## Signals

Inputs that should change decisions here:

- A config reload event (viper/fsnotify watch on `config.yaml`); only services
  whose settings actually changed are restarted (`compareConfigs` in `cmd/main.go`).
- Cache entry state: `X-Cache-Status: HIT|MISS|BYPASS` per response; expiry is
  file-mtime + TTL, enforced both at read time and by the cleanup worker.
- Validation results: all config problems are aggregated into one joined error
  at load time; a bad reload keeps the previous good config.

## What's kept

Invariants that must not regress:

- Single port serves everything; CONNECT requests bypass the mux and are
  tunneled directly, everything else goes through `http.ServeMux`.
- Proxy fetches never recurse into the proxy itself (loopback/localhost +
  own-port detection, case-insensitive, fail-safe on missing port).
- Origin fetches use one shared pooled `http.Client` with proxying explicitly
  disabled — no ambient environment proxy, no loops through self.
- Library code returns errors and logs via `log/slog`; only `main` decides to
  exit. No global mutable state outside `pkg/config`'s documented accessors.
- Cached responses preserve original status code and headers (gob envelope,
  atomic temp-file+rename writes). Corrupt/legacy entries degrade to cache miss
  and are removed, never served.
- Graceful shutdown drains the HTTP server; selective restart touches only the
  server or the cleaner when their config slice changed.

## What's changed and why

The August 2026 professionalization pass (11 verified ward tasks):

- **Cache format**: body-only flat files → gob envelope with status/headers/body.
  Why: cached content was previously served as `application/octet-stream` with a
  faked 200 regardless of origin type.
- **Validation**: bool-plus-side-effect logging → error aggregation. Why: callers
  need machine-readable failures and users deserve all problems at once.
- **Error-returning constructors**: `NewCacheHandler` no longer `log.Fatal`s.
- **Connection pooling**: per-request transports → package-level shared client.
- **Dependency injection**: proxy loop-detection reads injected listen port, not
  the global config singleton.
- **Observability**: stdlib log with ad-hoc prefixes → structured slog with
  semantic levels (Debug for hot-path noise).
- **Hygiene**: dead code removed, placeholder tests replaced by real coverage
  (cache metadata, corruption recovery, TTL expiry, key normalization, self-request
  detection, duration parsing), README rewritten to match the real schema,
  Makefile fixed (build target pointed at `./cmd`, fmt/vet added), GitHub Actions CI.

## Open questions

- Per-domain cache TTL overrides? Deferred; current model is one global TTL and
  the cleanup worker assumes it.
- Header-only conditional revalidation (If-Modified-Since) against origin —
  not implemented; entries serve until mtime+TTL expiry.
- golangci-lint is wired in CI but not installed locally; consider a Makefile
  bootstrap target or tool dependency pinning.
- Static route keys come from YAML map ordering; document that route names must
  be unique after `/`-trimming (currently silently skipped on collision).
