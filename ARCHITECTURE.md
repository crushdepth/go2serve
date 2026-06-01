# Architecture

A developer's guide to the structure, patterns, and design decisions behind go2serve.

go2serve is a single-binary static file server built almost entirely on the Go standard library. It is small by design — three source files, no framework, no plugin system. This document explains how those pieces fit together so you can navigate and extend the code quickly.

---

## At a glance

- **Language:** Go 1.26, `package main`, no CGO.
- **Dependencies:** only `golang.org/x/crypto` (ACME/Let's Encrypt) and `golang.org/x/net` (connection limiting). Everything else is `net/http` and the standard library.
- **Output:** one static binary that runs in a `scratch` Docker image as a non-root user.
- **Design ethos:** standard library first, secure by default, minimal surface area. No config files, no runtime control plane — everything is set via command-line flags at startup.

---

## File organization

The entire program is three files in the root package:

| File | Responsibility |
|------|----------------|
| `main.go` | Entry point, configuration, server lifecycle, TLS setup, HTTP→HTTPS redirect, certificate caching. |
| `handler.go` | The file-serving handler: path-traversal-safe filesystem (`safeFS`) and security-header injection. |
| `ratelimit.go` | Per-IP token-bucket rate limiter and `X-Forwarded-For` client-IP resolution. |

Supporting files:

| File | Purpose |
|------|---------|
| `Dockerfile` | Multi-stage build producing a `scratch` image with a dedicated non-root user (UID 10847). |
| `docker-compose.yml` | Default HTTP-only service definition; HTTPS variants are commented out. |
| `Makefile` | Thin wrappers around `docker compose` (`make up`, `down`, `restart`, `logs`, `status`). |
| `README.md` / `QUICKSTART.md` | User-facing operation and flag reference. |

There is no `internal/`, no sub-packages, and no test files yet. The flat layout is intentional: the program is small enough that splitting it across packages would add ceremony without clarity.

---

## Request lifecycle

A request passes through a small, explicit middleware chain assembled in `run()` (`main.go`):

```
                  ┌─────────────────────────────────────────────┐
  TCP connection  │  net.Listener (optionally LimitListener)     │  max-conns
        │         └─────────────────────────────────────────────┘
        ▼
  ┌───────────────┐
  │ http.Server   │  conservative timeouts (read/write/idle/header)
  └───────────────┘
        │
        ▼
  ┌───────────────┐
  │ rateLimiter   │  per-IP token bucket → 429 if exhausted   (ratelimit.go)
  │   .wrap()     │
  └───────────────┘
        │
        ▼
  ┌───────────────┐
  │ fileHandler   │  inject security headers, then serve       (handler.go)
  │               │
  │  http.        │
  │  FileServer   │  ← backed by safeFS (path-traversal guard)
  │  (safeFS)     │
  └───────────────┘
```

The chain is built by plain function composition — `handler = rl.wrap(fileHandler)` — not a middleware framework. Each layer is a standard `http.Handler`, so adding a layer means writing a `func(http.Handler) http.Handler` and inserting it in `run()`.

---

## Operating modes

`run()` selects one of three modes based on which flags are set. This is the central branching logic of the program:

| Mode | Trigger | Behavior |
|------|---------|----------|
| **HTTP-only** | neither `--domain` nor `--cert`/`--key` | Single listener serves files directly over HTTP. For trusted/LAN use. |
| **Let's Encrypt** | `--domain` set | `autocert.Manager` obtains and renews certificates automatically. HTTP listener serves the ACME challenge + redirects to HTTPS. |
| **Manual TLS** | `--cert` and `--key` set | Certificates loaded from disk via `certCache`. HTTP listener redirects to HTTPS (validates `Host` against `--host`). |

`--domain` and `--cert`/`--key` are mutually exclusive; this and the other flag-combination rules are validated near the top of `run()` before anything is constructed. In both HTTPS modes the HTTP listener exists only to redirect (and, for Let's Encrypt, serve the ACME challenge) — it never serves files.

---

## Key components and patterns

### Configuration (`config` struct, `main.go`)

All configuration is a single `config` struct populated entirely from `flag` definitions in `main()`. There is no config file, no environment-variable parsing, no defaults loaded at runtime. `main()` only parses flags and calls `run(cfg)`; `run()` does all validation and construction. This keeps the entry point trivial and makes the full set of tunables discoverable in one place (the `flag.*Var` block).

### Lifecycle and graceful shutdown (`main.go`)

The server listens for `SIGINT`/`SIGTERM` via `signal.NotifyContext`. On signal (or a listener error), it calls `shutdown()` on each server, which uses `srv.Shutdown(ctx)` with a 10-second grace period for in-flight requests. In HTTPS mode both servers run in goroutines coordinated by a `sync.WaitGroup`, and a buffered error channel surfaces startup failures from either listener. There is deliberately **no** HTTP control/shutdown endpoint — exposing one on a public file server would be a security risk.

### Path-traversal-safe filesystem (`safeFS`, `handler.go`)

This is the security core of the file server. `safeFS` implements `http.FileSystem` and is handed to `http.FileServer`. Every `Open` call:

1. Cleans and joins the request path against the canonical root.
2. Resolves symlinks (`EvalSymlinks`).
3. Uses `filepath.Rel` to reject any path that escapes the root — including via symlinks. (`Rel` is used rather than a string-prefix check because the prefix approach breaks when root is `/`.)

The document root itself is canonicalized (symlinks resolved) **once at startup** in both `run()` and `newSafeFS`, so an attacker cannot swap the root symlink after launch.

**Known limitation documented in the code:** there is a TOCTOU window between `EvalSymlinks` and `os.Open`. It is not exploitable on a read-only volume (the standard deployment) but could be raced on a writable volume. Fully closing it would require `O_NOFOLLOW` and platform-specific syscalls, which the project avoids to stay pure-Go. The standard deployment mounts the webroot read-only (`:ro`) precisely for this reason.

The `--no-listing` flag is enforced here too: when listings are disabled, opening a directory with no valid in-root `index.html` returns `os.ErrPermission`, which `http.FileServer` renders as a 403. The code comments explain why this check lives in `Open` rather than a `Readdir` override and why `os.Stat` must not be used (it follows symlinks).

**Hidden-file policy.** `safeFS` refuses to serve dotfiles regardless of listing mode. `Open` calls `isHidden` on the request path and returns `fs.ErrNotExist` (a 404, chosen over a 403 so the response does not confirm the file exists) for any path with a component beginning with `.` — `.git/`, `.env`, `.htpasswd`, editor swap files, and so on. The single exception is `.well-known` (RFC 8615), the standard public location for `security.txt`, ACME challenges, and app-association files. Blocking access alone is not enough while listings are on, so the returned file is wrapped in `hiddenFilteringFile`, which omits hidden entries from listings. It implements `ReadDir` (the `fs.ReadDirFile` fast path `http.FileServer` prefers) as well as the legacy `Readdir`, so the efficient `getdents`-based listing is preserved and filtering is just an in-memory slice filter — no per-request stat or measurable overhead.

### Security headers (`newFileHandler`, `handler.go`)

The file handler wraps `http.FileServer` and sets `X-Content-Type-Options`, `X-Frame-Options`, and `Referrer-Policy` on every response, plus optional `Strict-Transport-Security` (HSTS) and `Content-Security-Policy` when configured. A micro-optimization pattern is used throughout: header values are pre-allocated `[]string` slices assigned directly to the header map (`h["Key"] = slice`) rather than `Header.Set`, bypassing per-request allocation and the canonical-key scan. HSTS is only ever populated in HTTPS mode (guarded in `run()`), since sending it over plain HTTP would be incorrect.

### Certificate caching (`certCache`, `main.go`)

For manual-TLS mode, `certCache.get` implements `tls.Config.GetCertificate` with a 60-second TTL and a read-write mutex (double-checked locking on the write path). This enables **zero-downtime certificate rotation**: drop new cert/key files on disk and they are picked up within 60 seconds with no restart. On reload failure the existing certificate is kept and the TTL reset, so a transient bad file never breaks live TLS handshakes.

### Rate limiting (`rateLimiter`, `ratelimit.go`)

A per-IP token bucket. Each client IP gets an `ipEntry` (tokens + last-seen timestamp) stored in a `sync.Map`. `allow()` lazily refills the bucket based on elapsed time, caps it at `burst`, and consumes one token. A background `cleanup()` goroutine runs every 60 seconds and evicts entries idle long enough to have fully refilled — this bounds memory under churning client IPs. The goroutine is owned via a `done` channel and stopped in `run()`'s `defer`.

**Client-IP resolution** is the subtle part. By default the direct connection address (`RemoteAddr`) is the rate-limit key. When `--trusted-proxies` is set, and the direct peer is in that trusted set, `clientIP()` walks the `X-Forwarded-For` chain **right-to-left**, skipping trusted-proxy IPs, and uses the first non-trusted IP. Walking right-to-left is a deliberate anti-spoofing measure: a client can prepend fake entries to `X-Forwarded-For`, but cannot forge the rightmost entries appended by your own trusted proxies. Unparseable entries are ignored as corrupt/spoofed.

**Bucket key normalisation.** Before lookup, the resolved IP is passed through `bucketKey`. IPv4 addresses are keyed in full (`/32`); IPv6 addresses are aggregated to their **`/64` prefix**. A single client is routinely assigned an entire IPv6 `/64` (or larger), so keying on the full `/128` would let one client mint unlimited buckets — exhausting memory between cleanup passes and rotating source addresses to evade the per-IP limit. Aggregating to `/64` closes both. The residual case (a large population of genuine distinct IPv4 clients) is naturally bounded by the active set and reclaimed by the 60-second cleanup.

### Connection limiting and timeouts (`main.go`)

`listen()` optionally wraps the TCP listener with `netutil.LimitListener` (`--max-conns`). `newServer()` applies conservative timeouts to limit resource exhaustion from slow or idle clients. Read and write deadlines are **decoupled**: a short `ReadHeaderTimeout` (5s, the slowloris guard) plus `ReadTimeout` (`--timeout`) bound the request side, while `WriteTimeout` (`--write-timeout`) bounds the response side independently. Coupling them — the previous behaviour — meant any download exceeding the read timeout was truncated, so a 30s default silently cut off large files. `--write-timeout` defaults to `0` (no response deadline), which is the correct choice for a file server: large files are never truncated. The honest tradeoff is that a client slowly draining a response body is then bounded only by `--max-conns` (the connection limit) and any fronting reverse proxy — `IdleTimeout` covers only between-request idling, not an in-progress transfer. Operators exposing the server directly to the public internet who are concerned about slow-read clients should set a positive `--write-timeout`; those behind a reverse proxy can rely on the proxy's response timeouts. These are defense-in-depth against trivial DoS, independent of the per-IP rate limiter.

---

## Conventions for contributors

- **Standard library first.** Reach for a third-party dependency only when the stdlib genuinely cannot do the job (the two `golang.org/x` deps are the bar). New dependencies should be justified by a security or correctness need, not convenience.
- **Security comments are load-bearing.** The longer comment blocks in `handler.go` and `ratelimit.go` document *why* a non-obvious approach was chosen (TOCTOU, `Rel` vs prefix, right-to-left XFF, `os.Stat` symlink hazard). If you change that code, update or preserve the reasoning — don't silently drop it.
- **Secure-by-default.** New flags should default to the safe behavior. Anything that could weaken security (e.g. trusting proxy headers) must be explicitly opt-in.
- **Handlers are plain `http.Handler`s.** Add new middleware as `func(http.Handler) http.Handler` and compose it in `run()`. Don't introduce a router or framework for a server whose job is to serve a directory.
- **No runtime control surface.** Resist adding admin/metrics/shutdown HTTP endpoints to the public listener. Lifecycle is driven by signals (and, in practice, the Docker container lifecycle).
- **Validate in `run()`, not `main()`.** Keep `main()` to flag parsing and the call into `run()`; put all validation and wiring in `run()` so it is testable and the failure messages live in one place.

---

## Deployment model

The Dockerfile is a two-stage build: compile a static binary (`CGO_ENABLED=0`, stripped with `-ldflags="-s -w"`), then copy it into a `scratch` image alongside CA certificates and a synthetic `passwd`/`group` so it can run as a dedicated non-root user (UID/GID 10847). The webroot is expected to be mounted read-only (`/srv:ro`), and `/certs` is owned by the runtime user for Let's Encrypt or manual certs. The container lifecycle **is** the server lifecycle: `docker stop` sends `SIGTERM`, triggering graceful shutdown. `docker-compose.yml` ships the HTTP-only configuration as default with HTTPS variants commented out.
