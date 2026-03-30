# Architecture

## System Boundary

`sower` is the client-side transparent proxy entrypoint. It exposes local DNS, HTTP, HTTPS, and SOCKS5 listeners, applies rule-based routing, and forwards proxied traffic to an upstream transport.

`sowerd` is the server-side TLS ingress. It exposes `80/tcp` and `443/tcp`, terminates TLS, detects the upstream transport protocol, and relays traffic to the requested target or configured fake site.

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
- Direct/proxy/block routing decisions
- DNS query handling and upstream selection

`transport/sower`
- Sower transport frame encode/decode

`transport/trojan`
- Trojan transport frame encode/decode

`pkg/dhcp`
- DHCP-based upstream DNS discovery for the client side

## sower Data Flow

1. Load config from flags, env, and files.
2. Validate remote type, listener addresses, and DNS IP fields.
3. Log startup metadata with secrets redacted.
4. Resolve the effective upstream DNS server, falling back to the configured fallback DNS when needed.
5. Build the upstream dialer for the configured remote transport, using standard TLS by default and optional uTLS fingerprints for `sower` and `trojan`.
6. Build the router with suffix-tree rules and optional country CIDRs.
7. Start enabled local listeners for `udp/53`, `tcp/80`, `tcp/443`, and `tcp/1080`.
8. For DNS requests, return local proxy IPs for proxy-routed domains and query upstream DNS for direct domains.
9. For HTTP, HTTPS, and SOCKS5 traffic, parse the target host, apply routing rules, and either dial directly or wrap traffic in the configured upstream transport.
10. On shutdown signal, stop listeners and DNS servers through `context` propagation.

## sowerd Data Flow

1. Load config from flags, env, and files.
2. Validate required fields and address/certificate combinations.
3. Initialize logger with configured level and redact secrets from startup logs.
4. Start `:80` HTTP server.
5. Handle ACME HTTP-01 challenge on `:80`.
6. Redirect normal HTTP traffic to HTTPS.
7. If `fakeSite` is a local directory, serve it only for loopback fallback traffic through `127.0.0.1:80`.
8. Start `:443` TLS listener.
9. For each accepted connection, apply a short read deadline for protocol probing.
10. Try `sower` transport first, then `trojan`.
11. Relay matched traffic to the decoded target.
12. If no transport matches, relay to `fakeSite`.

## sowerd Install Flow

1. Detect install mode from CLI flags before normal config loading.
2. Require root privileges because the installer writes to `/usr/local/bin`, `/etc/systemd/system`, and `/etc/sower`.
3. Optionally copy or update the current binary to `/usr/local/bin/sowerd`.
4. Write `/etc/systemd/system/sowerd.service` with `ExecStart=/usr/local/bin/sowerd -c /etc/sower/sowerd.toml`.
5. Ensure `/etc/sower/sowerd.toml` exists and bootstrap `/var/www` for directory-backed fake site mode.
6. Reload systemd and optionally enable/start or restart the service.

## Design Decisions

- Client and server both fail fast on invalid startup configuration instead of silently degrading.
- Sensitive configuration values must never be printed verbatim in logs.
- Local listeners use explicit shutdown hooks instead of blocking forever with unmanaged goroutines.
- Network operations use timeouts and `context` to limit hangs during dialing and remote rule downloads.
- Upstream TLS behavior is configured only on the client side; `sowerd` remains a normal TLS server and does not need uTLS-specific logic.
- Rule loading supports local files and remote gzip-compressed HTTP sources.
- Fake site directory mode is loopback-only on port `80` to avoid exposing local static assets directly to the public internet.

## Operational Notes

- `sower` usually needs elevated privileges to bind `53/udp`, `80/tcp`, and `443/tcp`.
- `sowerd` must bind privileged ports `80` and `443`.
- `sowerd -i` also requires root because it writes system-level files for self-deployment.
- ACME mode requires port `80` to be reachable from the public internet.
- Remote rule download failures stop startup after bounded retries.

## Related Documents

- [README.md](/home/wweir/Mine/sower/README.md)
- [cmd/sower/main.go](/home/wweir/Mine/sower/cmd/sower/main.go)
- [cmd/sower/proxy.go](/home/wweir/Mine/sower/cmd/sower/proxy.go)
- [cmd/sowerd/main.go](/home/wweir/Mine/sower/cmd/sowerd/main.go)
- [config/sower.go](/home/wweir/Mine/sower/config/sower.go)
- [config/sowerd.go](/home/wweir/Mine/sower/config/sowerd.go)
- [internal/install/service.go](/home/wweir/Mine/sower/internal/install/service.go)
