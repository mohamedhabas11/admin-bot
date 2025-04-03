# admin-bot
admin-bot is a service that aims to make host managment easier in confined networks.

### WebServer
admin-bot handles static content delivery by acting as a webserver that simply returns files or defined repositories in its `config.yaml` file.

```yaml
http:
  enabled: true
  addr: "0.0.0.0"
  port: 8080
  dirs:  # Route: /static/<key-name>
    static-files-ubuntu:
      path: "/var/www/static-files-ubuntu"
    static-files-rhel:
      path: "/var/www/static-files-rhel"
```

### Proxy
admin-bot handles proxy lookups and content fetching, additionally can be configured to account for static content caching.
spawns background worker to clean up expired static content to clear up disk space.

```yaml
proxy:
  enabled: true
  addr: "0.0.0.0"                     # optional, inherits http.addr if undefined.
  port: 8081                          # optional, inherits http.port if undefined.
  cache-ttl: "14d"                    # optional, defaults to '7d' if undefined.
  cache: "/var/cache/admin-bot/admin-bot-proxy" # optional, no centent caching if undefined.
  targets:
    github: # Route: /proxy/github/
      url: "https://github.com"
      # cache-ttl: "1d"               # Optional override
```

### Example Setup
```yaml
# Main HTTP Server Configuration
http:
  enabled: true
  addr: "0.0.0.0"
  port: 8080 # Single port for all HTTP services

  # --- Static File Serving ---
  # Serves local directories via HTTP.
  static:
    enabled: true
    # Base path prefix for all static routes: /static/
    # Map key becomes the next part of the path: /static/<key>/...
    dirs:
      files-ubuntu: # Route: /static/files-ubuntu/
        path: "/var/www/static-files-ubuntu"
      files-rhel:   # Route: /static/files-rhel/
        path: "/var/www/static-files-rhel"
      # Add other static directories as needed

  # Forward Proxy Specific Settings
  forward-proxy:
    # Controls the forward proxy logic on the http listener
    # allows Proxiying of domains if omited and domains is defined
    enabled: true

    # Caching configuration for specific domains (Applies primarily to HTTP requests)
    cache:
      enabled: true # Master switch for caching via this proxy
      cache-dir: "/var/cache/admin-bot/forward-proxy-cache" # Required if cache.enabled=true
      cache-ttl: "7d" # Default TTL for cached domains

    # List of domain names (exact match, case-insensitive) to cache HTTP requests for.
    # Requests to other domains will be proxied but not cached.
    domains:
      - "github.com"
      - "pypi.org"
      - "download.docker.com"

# --- Background Proxy Cache Cleanup Service ---
# This section defines a background task to clean expired files from the proxy cache.
proxy-cache-cleanup:
  # Enabled implicitly if http.proxy.enabled=true and http.proxy.cache-dir is set.
  enabled: true # Could be explicit if needed
  # How often to scan the cache directory for expired files.
  interval: "40s" #"1h" # e.g., "1h", "30m", "6h"
  # Operates on the directory defined in http.proxy.cache-dir,
  # using the TTL defined globally in http.proxy.cache-ttl.
  # Note: Handling per-target TTL overrides during cleanup adds complexity.
  #       A simpler approach is to clean based only on the global TTL or file mod time + global TTL.
```
