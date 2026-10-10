# Security: sowerd Edge Threat Model and Anti-Probing Rules

Authoritative security design for the `sowerd` TLS edge. Rules here are
normative; historical analysis lives in `docs/plans/` (non-authoritative).

## Threat Model

Two adversaries are in scope:

- **Passive DPI observer** — sees wire bytes and flow metadata; does not
  complete handshakes.
- **Active prober** — completes TLS handshakes, sends arbitrary application
  bytes, observes responses. Does not know the sower password.

Goal: keep the `443` edge behaviorally consistent with an ordinary TLS
website for both adversaries, so that no single observation — wire bytes,
handshake behavior, or application responses — is by itself proof that the
sower transport exists. This is a design goal, not a guarantee: known
residual signals that fall short of full indistinguishability are listed
under "Residual Accepted Signals" and drive the rejected-directions
recorded below.

## Layer 1 — Wire Format Is Ordinary HTTPS

- Real TLS 1.2+ with publicly trusted certificates: autocert/Let's Encrypt,
  or a configured static pair. All sower metadata (the transport `Head`)
  rides inside TLS; there is no protocol-specific plaintext.
- ALPN is `http/1.1` only. This is a residual but accepted signal (see
  "Rejected directions" for why h2 is not served).

## Layer 2 — Protocol Identification Happens After the Handshake

- The sower transport is recognized by the first application byte `0x80`.
  `~0x80 == 0x7F` is the highest printable code point, so no valid HTTP/1.1
  plaintext can begin with `0x80`; probe dispatch is unambiguous.
- The `Head` checksum (HMAC-SHA256 over cmd/port/target keyed by the
  password) authenticates the frame and prevents MITM retargeting of
  captured frames. It is not an encryption layer; confidentiality and
  integrity come from TLS.
- **ALPN is never trusted for dispatch.** A sower client may offer `h2`
  (e.g. uTLS Chrome fingerprint); the post-handshake sniff routes it to the
  transport path regardless of the negotiated protocol.

## Layer 3 — Everything Else Is a Website

Dispatch order for non-transport traffic (`cmd/sowerd/main.go:handleConn`):

1. TLS SNI hit in `site_routes` → reverse proxy to the route's upstream.
2. Otherwise, a valid HTTP/1.1 request line → reverse proxy to `fake_site`.
3. Otherwise → raw TCP relay to `fake_site`.

A matched route is always fed to the reverse proxy, HTTP request line or not:
`site_routes` serves HTTP/1.1 only. Non-HTTP traffic on a routed domain fails
HTTP parsing rather than being relayed, which is also why an HTTP-layer visitor
gate (below) covers the route's entire surface.

Transport auth failures fall back to the same website paths (never RST, never
hang), so a prober without the password observes ordinary site behavior on
every probe.

## Site Route Visitor Gate

`site_routes` upstreams are public: autocert issues certificates for the routed
domains on demand, and issued certificates are published in Certificate
Transparency logs. The optional per-route `site_routes.auth` block gates the
route with HTTP Basic credentials, for upstreams that cannot authenticate their
own visitors — the edge is the only insertion point, because sowerd owns 80/443
on the host.

Rules:

- Opt-in and default off. A route without `auth` (or with both fields empty)
  behaves exactly as before, including passing the client's `Authorization`
  header through to the upstream.
- The gate runs in the reverse-proxy handler (`reverseProxyConn`), so it covers
  the route's whole surface: path-prefix overrides and WebSocket upgrades
  included. The sower transport path is dispatched before SNI routing and
  carries its own frame-checksum authentication; it is never gated.
- Credentials are compared in constant time (`sha256` + `subtle`, so length
  does not leak).
- A passing request has its `Authorization` header removed. The edge
  credential authenticates the visitor to the edge, not to the upstream:
  forwarding it would copy the edge password into the upstream and its logs,
  and would collide with an upstream realm that carries the same name.
- Failures are throttled per direct peer address: `authMaxFails` failed
  attempts from one source accumulate (there is no decay window; only a request
  that passes the gate clears the count) and lock that source out for
  `authLockDuration`. The failure map is bounded (`authMaxEntries`) so
  source-IP rotation cannot grow it without bound. A lockout only ever affects
  the IP that caused it; the counter is shared by all gated routes so a visitor
  cannot get a fresh budget by moving between them.
- Upstream credentials are a separate mechanism: URL userinfo
  (`upstream = "http://user:pass@host"`) is sent to the upstream as Basic
  credentials. `net/http` turns userinfo into that header only in
  `http.Client`; `httputil.ReverseProxy` round-trips through a `Transport` and
  drops it, so sowerd sets the header explicitly. Upstream passwords are
  redacted in error logs (`url.URL.Redacted`).
- ACME is unaffected: HTTP-01 runs on `:80` and TLS-ALPN-01 completes during
  the handshake, both before any request reaches the reverse proxy.
- What it is not: a substitute for the upstream's own authentication, an
  account system (one credential per route, no session, no logout), or a
  second factor. A weak password stays weak against a distributed or
  IPv6-rotating attacker. Cross-origin browser calls that need a CORS
  preflight also stay broken: a preflight carries no credentials, so the edge
  rejects it before the cross-origin request is even attempted.
- Browser WebSocket clients are the open edge case. The gate does run on the
  `Upgrade` request, but script cannot set `Authorization` on a WebSocket
  handshake: a browser can only pass it by replaying the origin's cached Basic
  credential, and whether it does so is browser-specific and unverified here.
  The gate is per-route, so a `/ws` path cannot be exempted while the rest of
  the route stays gated. A reconnecting client makes this self-inflicted: every
  failed handshake feeds the shared per-IP counter, so an auto-reconnecting
  browser client (socket.io and friends) can lock its own source IP out of the
  whole route — ordinary page loads included — for `authLockDuration`, and
  again after each expiry. Verify in the target browser before putting a
  browser WebSocket route behind the gate, or reach that upstream through the
  sower transport instead.

## Unknown-SNI Certificate Fallback

**Rule: the TLS handshake must complete for arbitrary SNI.** A handshake
failure on unknown names is an active-probing signal — real sites present
their default vhost certificate.

Implementation rules (`cmd/sowerd/main.go:getCertificateWithFallback`,
autocert mode only):

- The issuance whitelist is `cert.domains` + every `site_routes` domain —
  the same source as the autocert `HostPolicy`, so the membership decision
  cannot drift from issuance behavior.
- **Whitelisted names** take the plain autocert path. Failures (ACME
  issuance, cache) surface as-is and are never masked by a wrong-name
  certificate.
- **Unknown or empty SNI** is answered with the primary domain's
  certificate. "Primary" is the first entry of the issuance whitelist, so
  keep `cert.domains[0]` a concrete domain (a leading wildcard entry would
  break the fallback lookup).
- The client's capabilities (signature schemes, curves, cipher suites) are
  carried over to the fallback lookup so autocert resolves its usual
  ECDSA/RSA cache key; a bare hello would count as non-ECDSA and force a
  spurious RSA issuance.
- Static certificate mode (`cert.cert` configured) already answers every
  SNI with the configured pair; the wrapper is autocert-mode only.

## Client-Side Fingerprint

The default Go TLS `ClientHello` is an uncommon fingerprint for a "browser".
sower clients should set `[remote.tls] client_hello = "chrome"` (uTLS) in
deployment configs. This is a client configuration knob, not a protocol
property, and has no server-side effect.

## Rejected Directions

Decisions recorded here so they are not re-litigated without new evidence:

- **QUIC / HTTP3 transport** — requires either a dedicated UDP endpoint or a
  custom ALPN. Both expose nonstandard-protocol existence to an active
  prober (port scanning / ALPN enumeration), which is strictly weaker than
  the current byte sniff that reveals nothing. Would also require a TCP
  fallback path everywhere. Experimented with on 2026-09-17/18 and removed.
- **h2 on the fallback path** — once `h2` is advertised, browsers negotiate
  it and WebSockets require RFC 8441 extended CONNECT. The Go standard
  library `httputil.ReverseProxy` has no extended-CONNECT support (it treats
  the request as a plain CONNECT), and `x/net/http2` (through v0.59) gates
  extended CONNECT behind `GODEBUG=http2xconnect=1`, default off
  (golang/go#71128). The per-SNI ALPN workaround (h1-only for WS-bearing
  routes) was declined: it makes "new WS routes must opt out of h2" a silent
  failure mode on an anti-censorship edge.
- **Wildcard `site_routes` domains** (`*.example.com`) — rejected in
  `config/sowerd.go` (`validateSiteRoutes`) and must stay rejected. SNI
  dispatch is an exact-match map (`siteRouter.lookup`); a wildcard either
  collapses every subdomain onto one upstream — losing the per-domain
  routing it was meant to add — or needs priority/ambiguity rules on the
  edge's hottest path. `autocert.HostWhitelist` is exact-match too, so
  wildcard issuance would additionally force a hand-written `HostPolicy`.
- **Additional listen addresses and configurable 80/443 ports** — one
  `serve_ip` and hardcoded 80/443 are deliberate: an edge listening on a
  non-standard port is itself an active-probing signal. Multiple public
  addresses are served by one instance per address, not by widening this
  listener. `serve_ip` may be a concrete address to bind a single interface.
- **Per-site certificate binding** — the edge's certificate set is
  independent of the `site_routes` table (autocert issues for the union of
  routed domains plus `cert.domains`; static mode serves one pair for every
  SNI). Keeping it independent means a prober cannot enumerate configured
  sites from the certificate. Multi-SAN certificates already cover the
  legitimate multi-domain case.
- **Domain routing on plain HTTP (:80)** — the :80 listener only completes
  ACME challenges and 308-redirects public traffic to HTTPS; the `fake_site`
  directory is served there on loopback only (`isLocalRemoteAddr`). Routing
  :80 by domain would hand out a cleartext bypass of the TLS edge.
- **Session-cookie login for site routes** — declined in favor of HTTP Basic:
  a login page plus server-side sessions adds edge state and login-page
  surface, and non-browser clients (curl, media players, mobile apps) speak
  Basic rather than HTML forms. Basic costs one browser credential prompt per
  origin and keeps the gate stateless.
- **Gating the `fake_site` fallback** — the fake site is the decoy that answers
  unknown SNI (probes and failed transport auth included) and must keep
  answering like an ordinary public website. An operator's real site that needs
  a gate belongs in `site_routes`.

## Residual Accepted Signals

- ALPN `http/1.1`-only: plausible for small self-hosted sites; h2 is
  blocked by the rejection above.
- Flow statistics (many short-lived TLS connections, no HTTP semantics on
  transport connections): common to all TLS-in-TLS proxies; connection reuse
  is deferred.
- The `Head` HMAC key is the raw password without a KDF: only relevant to an
  observer who can already capture a `Head`, which requires defeating TLS.
