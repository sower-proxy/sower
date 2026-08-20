# Architecture

## System Boundary

`sower` is the client-side transparent proxy entrypoint. It exposes local DNS, HTTP, HTTPS, and SOCKS5 listeners, applies rule-based routing, and forwards proxied traffic to an upstream transport.

`sowerd` is the server-side TLS ingress. It exposes `80/tcp` and `443/tcp`, terminates TLS, detects the upstream transport protocol, and relays traffic to the requested target or configured fake site.
For non-transport fallback traffic, it can route by TLS SNI to per-domain upstream site URLs, then falls back to the configured `fake_site`; directory-backed `fake_site` is served by the loopback local fileserver.

## Layer Responsibilities

`cmd/sower`

- Process bootstrap
- Config loading and validation
- Local listener lifecycle and graceful shutdown
- Rule file loading
- Upstream proxy dialing

`cmd/sowerd`

- Process bootstrap
- Interactive self-install mode dispatch
- Config loading and validation
- TLS and HTTP server lifecycle
- Graceful shutdown

`config`

- Runtime configuration schema
- Input validation rules
- Embedded default `sowerd` install config template

`internal/install`

- Interactive systemd installation flow for `sowerd`
- Binary copy/update and service file generation
- Default config and fake site directory bootstrap

`router`

- Domain and CIDR rule matching
- Thread-safe `RuleSet` (list/retain/rebuild) backing runtime rule management
- Direct/proxy/block routing decisions
- DNS query handling and upstream selection

`internal/admin`

- Admin HTTP server: session-cookie auth, rule CRUD API, traffic stats API
- Traffic stats recorder: aggregate counters and bounded per-domain accounting
- Serves the embedded admin console frontend

`web`

- Svelte 5 + Vite (Tailwind v4 + hand-maintained shadcn-svelte-style UI, no bits-ui) admin console frontend
- Build output `web/dist` is embedded into the binary via `go:embed`

`transport/sower`

- Sower transport frame encode/decode

`pkg/dhcp`

- DHCP-based upstream DNS discovery for the client side; the received OFFER is validated (matching transaction id, BOOTREPLY opcode, message type OFFER) so unrelated or spoofed LAN packets cannot inject DNS servers.

## sower Data Flow

1. Load config from flags, env, and files.
2. Validate remote type, listener addresses, and DNS IP fields.
3. Log startup metadata with secrets redacted.
4. Build the upstream proxy dialer with a stable DNS target. If `dns.upstream` is empty, the dialer uses `dns.fallback` to avoid recursive lookup through the local DNS listener.
5. Build the upstream dialer for the configured remote transport, using standard TLS by default and optional uTLS fingerprints for `sower`.
6. Build the router with suffix-tree rules and optional country CIDRs.
   Remote rule files are fetched through the configured upstream proxy dialer, never by direct outbound HTTP, so rule bootstrap uses the same stable egress path as proxied traffic.
   Remote domain rule files are filtered through per-router `file_skip_rules` before their prefixed entries are appended.
7. Start enabled local listeners for `udp/53`, `tcp/80`, `tcp/443`, and `tcp/1080` only after rule loading completes.
8. For DNS requests, return local proxy IPs only for explicitly proxy-routed domains and query upstream DNS for direct or unknown domains.
   DNS routing intentionally does not mirror smart TCP routing: DNS must support arbitrary protocols and ports, so unknown names stay conservative and are not mapped to local HTTP/HTTPS proxy listeners by default.
   Empty `dns.upstream` keeps router-side DHCP DNS discovery enabled; `dns.fallback` is appended as a backup upstream and is also used while initial discovery is in flight.
   Proxy-routed domains return local A/AAAA records, suppress HTTPS/SVCB and other non-address metadata locally, and never leak proxy-matched names to direct upstream DNS.
   Direct upstream DNS failures fall back only for retryable upstream service errors; when no fallback succeeds, the last upstream DNS response code is returned as-is.
   Service discovery names are matched against both the full query name and the base domain only for service record types.
   Reverse lookups (PTR) for internal ranges (RFC1918, CGNAT 100.64/10, link-local, loopback, IPv6 ULA) are answered with NXDOMAIN locally unless the upstream that would serve the query is an internal DNS server. The gate judges the currently selected upstream rather than the whole pool, so internal layout never leaks to public DNS — including a degraded mixed pool that fell back to a public resolver — and internal reverse resolution still works while an internal DNS server is selected.
   Client attribution for DNS statistics prefers the EDNS Client Subnet (ECS) address when a forwarding resolver (e.g. dnsmasq --add-subnet) carries the real client in the query with a full-length source prefix (32/128; a truncated prefix such as --add-subnet=24 only identifies the subnet base and is ignored), falling back to the transport source address; ECS is stripped before any query is forwarded upstream so client subnets never reach public resolvers. ECS is trusted only from the local forwarding resolver — a direct client can spoof it, which affects console attribution only.
   When `dns.reverse` is configured, the traffic console resolves client IPs to hostnames via PTR queries to that resolver (typically the local dnsmasq, which answers LAN leases and tailnet names and forwards everything else); results are cached for an hour, failed lookups for five minutes, and failures degrade to the raw IP.
9. For DNS-mode transparent HTTP traffic, parse the target host from the request line and always forward through the upstream proxy. For DNS-mode transparent HTTPS traffic, peek the TLS ClientHello to extract SNI and always forward through the upstream proxy.
   These transparent `80/443` listeners are second-stage proxy-only handlers for domains already mapped to local proxy IPs by DNS; they do not run smart routing again.
   HTTPS transparent proxying reads only the TLS ClientHello, then replays the untouched bytes to the selected upstream; it must not complete or terminate TLS locally.
10. For SOCKS5 traffic and explicit HTTP proxy traffic, read the client-supplied target host and port, apply smart routing rules, and either dial directly or wrap traffic in the configured upstream transport.
11. Wrap every proxied client connection in the admin stats recorder before protocol parsing, attribute bytes to the discovered domain after parsing, and count DNS queries through a handler decorator. Admin rule mutations take effect immediately and persist as `add` / `remove` deltas relative to the startup baseline; state write failures reject the mutation without changing the runtime rule set.
12. When `[admin]` is enabled, serve the admin console: session-cookie auth for the API, persisted rule deltas, sanitized effective-config display, whitelisted config overrides (immediate for `log_level` and DNS upstreams, restart-mode for the rest), per-rule hit and rule-miss statistics, and an in-place process restart endpoint; secrets never leave the server. The Svelte frontend is served from the embedded `web/dist`. By default the admin server owns a dedicated listener; when `admin.addr` exactly matches `dns.serve:80`, the admin console and the HTTP proxy share one listener and each connection is classified by its request head (origin-form with the listener IP as Host goes to admin; CONNECT, absolute-form, and other Hosts go to the proxy).
13. On shutdown signal, stop listeners and DNS servers through `context` propagation.

## sowerd Data Flow

1. Load config from flags, env, and files.
2. Validate required fields and address/certificate combinations.
3. Initialize logger with configured level and redact secrets from startup logs.
4. Start `:80` HTTP server.
5. Handle ACME HTTP-01 challenge on `:80`.
6. Redirect normal HTTP traffic to HTTPS.
7. If `fakeSite` is a local directory, serve it only for loopback fallback traffic through `127.0.0.1:80`.
8. Start `:443` TLS listener with HTTP/1.1 ALPN only.
9. Complete the TLS handshake explicitly (60s budget; first contact can drive synchronous ACME issuance) before applying the short probe read deadline.
10. Probe the connection's first bytes to identify the `sower` transport.
11. If matched, authenticate (the frame checksum covers command, port, and target via HMAC-SHA256; empty or control-character targets are rejected) and relay traffic to the decoded target. The header read is bounded by its own deadline so a connection that sends only the probe byte cannot hold a goroutine and fd forever.
12. If authentication fails or no transport matches, read the TLS SNI from the terminated TLS connection.
13. If the SNI exactly matches a configured `site_routes` domain, reverse-proxy the decrypted HTTP/1.1 request to that route's `http://` or `https://` upstream URL.
14. If the SNI has no route, relay to `fakeSite`.

## sowerd Install Flow

1. Detect install mode from CLI flags before normal config loading.
2. Require root privileges because the installer writes to `/usr/local/bin`, `/etc/systemd/system`, and `/etc/sower`.
3. Optionally copy or update the current binary to `/usr/local/bin/sowerd`.
4. Write `/etc/systemd/system/sowerd.service` with `ExecStart=/usr/local/bin/sowerd -c /etc/sower/sowerd.toml`.
5. Ensure `/etc/sower/sowerd.toml` exists and bootstrap `/var/www` for directory-backed fake site mode.
6. Reload systemd and optionally enable/start or restart the service.

## Design Decisions

- Client and server both fail fast on invalid startup configuration instead of silently degrading.
- The client and server load TOML configuration files only; YAML and HCL are not supported.
- Sensitive configuration values must never be printed verbatim in logs.
- Local listeners use explicit shutdown hooks instead of blocking forever with unmanaged goroutines.
- Network operations use timeouts and `context` to limit hangs during dialing and remote rule downloads.
- The admin console persists rule changes as bounded deltas in `admin.state_file`, without rewriting TOML or rule sources. It also exposes a sanitized effective-config view and persists whitelisted overrides: `log_level` and the DNS upstreams apply immediately, every other whitelisted field (remote, listeners, rule sources) takes effect on the next restart. Clearing an override reverts the field to the file/flag configuration. `--ignore-admin-state` is the startup escape hatch for a bad state file or override.
- The admin server is disabled by default, binds to loopback by default, and requires a password when enabled; an empty password falls back to a startup-generated random one printed once in the log, and the login page tells the user where to find it. Login is rate-limited per remote IP (five failures lock for fifteen minutes). The frontend never stores the password (HttpOnly session cookie only).
- The admin restart endpoint replaces the process image in place (`exec` on Unix, same PID) so systemd stays unaware; on platforms without in-place restart the endpoint reports an error instead of acknowledging a restart that would fail. Sessions persist to `admin.session_file` (atomic write, 0600) so a restart keeps the browser logged in; `admin.disable_session_persistence` and `admin.cookie_secure` tune that behavior. Session persistence is best-effort: a disk write failure never blocks login or leaves a revoked session valid in memory — the in-memory state stays authoritative and the failure is logged.
- Rule hit statistics (per matched rule, per category) and rule-miss statistics (per domain for connections that matched no rule) are tracked in bounded in-memory maps fed by router observers; rule mutations invalidate the hit domain cache so counts stay attributable to the current rule set.
- The admin console can share the DNS HTTP proxy listener on port 80 when `admin.addr` equals `dns.serve:80`; classification is Host-based (origin-form requests with the listener IP as Host are admin traffic), so proxying a target whose Host equals the listener IP is inherently ambiguous and routes to admin. The HTTPS and DNS listeners speak different protocols and cannot be shared.
- Traffic monitoring reports proxied payload bytes and request/connection counters, not packet-level network accounting. Per-domain byte attribution is batched per connection (32 KiB threshold or 500 ms window) so the relay hot path pays two atomics per I/O instead of a global mutex; Close always drains the remainder.
- The process sets a soft Go memory limit (128 MiB by default; an explicit `GOMEMLIMIT` wins, `SOWER_MEMORY_LIMIT_MB` overrides with a MiB value, `0` disables). The default ad/china/gfw rule lists build ~40 MiB of suffix trees and GOGC's 2x target would otherwise keep resident memory near 250 MiB on an idle gateway.
- Upstream TLS behavior is configured only on the client side; `sowerd` remains a normal TLS server and does not need uTLS-specific logic.
- Rule loading supports local files and remote HTTP sources; remote downloads must use the configured upstream proxy and fail startup if the proxy path cannot fetch them.
- Domain rule files support per-router skip rules for filtering third-party file entries without removing explicit local rules.
- HTTP/HTTPS access probes are cached with an hour-long write TTL through `github.com/maypok86/otter/v2`, keeping repeated smart-routing checks bounded without hiding later reachability changes indefinitely.
- Country routing treats `router.country.mmdb` as optional; an empty value disables GeoIP lookup and keeps CIDR-based matching active without startup warnings. A non-empty invalid MMDB path is a startup error.
- DNS routing, transparent HTTP/HTTPS forwarding, and smart TCP routing are separate policies. DNS uses conservative explicit proxy rules only; transparent HTTP/HTTPS is proxy-only; SOCKS5 and explicit HTTP proxy traffic use smart routing.
- Fake site directory mode is loopback-only on port `80` to avoid exposing local static assets directly to the public internet.
- `sowerd` prefers the user cache directory for ACME state, but falls back to `/var/cache/sower` so systemd services can start without `HOME`/`XDG_CACHE_HOME` or a config file.
- `sowerd` fallback site routing is based only on exact TLS SNI matches; wildcard domains are not supported.
- `sowerd` site routes use HTTP reverse proxying to support full `http://` and `https://` upstream URLs. The reverse proxy rewrites the outbound Host to the upstream host.
- `sowerd` site routing rejects HTTP upgrade requests instead of hijacking the fallback connection; fallback sites are intended for normal HTTP/1.1 decoy traffic.
- `sowerd` site routing applies bounded client header, upstream dial, TLS handshake, and upstream response-header timeouts.
- `sowerd` advertises only HTTP/1.1 over TLS because fallback site routing and fake-site serving are HTTP/1.1 paths.
- In autocert mode, `sowerd` obtains certificates only for configured domains: every `site_routes` domain plus the `cert.domains` whitelist (autocert HostPolicy), so an arbitrary SNI cannot drive ACME issuance and exhaust the account's rate limits. Direct-connection domains (e.g. the sower client's remote addr) must be listed in `cert.domains`. In custom certificate mode, the configured certificate must cover every routed domain through SANs.

## Operational Notes

- `sower` usually needs elevated privileges to bind `53/udp`, `80/tcp`, and `443/tcp`.
- `sowerd` must bind privileged ports `80` and `443`.
- `sowerd -i` also requires root because it writes system-level files for self-deployment.
- ACME mode requires port `80` to be reachable from the public internet.
- Remote rule download failures stop startup after bounded retries before local listeners are exposed.
- `sowerd` custom certificate mode must use a certificate whose SANs cover all configured `site_routes` domains.

## Related Documents

- [README.md](README.md)
- [docs/README.md](docs/README.md) — documentation index
