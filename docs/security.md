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

Transport auth failures fall back to the same website paths (never RST, never
hang), so a prober without the password observes ordinary site behavior on
every probe.

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

## Residual Accepted Signals

- ALPN `http/1.1`-only: plausible for small self-hosted sites; h2 is
  blocked by the rejection above.
- Flow statistics (many short-lived TLS connections, no HTTP semantics on
  transport connections): common to all TLS-in-TLS proxies; connection reuse
  is deferred.
- The `Head` HMAC key is the raw password without a KDF: only relevant to an
  observer who can already capture a `Head`, which requires defeating TLS.
