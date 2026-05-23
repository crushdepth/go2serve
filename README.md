# go2serve

A secure, lightweight static file webserver. Deploy anywhere, run with a single command.

## Design principles

- Standard library first — minimal third-party dependencies
- Emphasis on secure default operation — path traversal prevention, security headers, TLS 1.2+
- Lightweight — no CGO, single static binary, runs in a scratch Docker image

---

## Configuration

Before running go2serve, you must configure which directory to serve. Edit `docker-compose.yml` and replace the placeholder volume path:

```yaml
volumes:
  - /path/to/files:/srv:ro    # <-- change /path/to/files to your directory
```

For HTTPS, uncomment the relevant port and command lines in the same file. See the [Usage](#usage) section for details.

A `QUICKSTART.md` guide is included for the fastest path from clone to running server.

---

## Installation

### Docker (recommended)

```bash
make up
```

A `docker-compose.yml` is provided with the HTTP-only configuration as the default, and HTTPS variants as comments. Edit the volume path and uncomment the relevant lines for your setup. See `QUICKSTART.md` for a step-by-step walkthrough.

To build the image manually:

```bash
docker build -t go2serve .
```

### From source

Requires Go 1.22+.

```bash
go build -o go2serve .
```

To stamp a version into the binary at build time:

```bash
go build -ldflags "-X main.version=v1.0" -o go2serve .
```

The current version is printed at startup and can be checked with:

```bash
go2serve --version
```

---

## Usage

### HTTP only (LAN / internal)

No certificates needed. Suitable for trusted networks.

```bash
go2serve --root /path/to/files
```

Docker:

```bash
docker run -p 80:8080 \
  -v /path/to/files:/srv:ro \
  go2serve --root /srv
```

### HTTPS — Let's Encrypt (automatic)

Requires a public domain name with DNS pointing at this server, and port 80 reachable from the internet for the ACME challenge.

```bash
go2serve --root /path/to/files --domain example.com --cache-dir /path/to/certs
```

Docker (using docker-compose.yml is recommended — see `QUICKSTART.md`):

```bash
docker run -p 80:8080 -p 443:8443 \
  -v /path/to/files:/srv:ro \
  -v go2serve-certs:/certs \
  go2serve --root /srv --domain example.com
```

Certificates are obtained on first connection and renewed automatically. The named volume `go2serve-certs` is managed by Docker — no directory setup needed.

### HTTPS — manual certificates

```bash
go2serve --root /path/to/files --cert /path/to/cert.pem --key /path/to/key.pem
```

Docker:

```bash
docker run -p 80:8080 -p 443:8443 \
  -v /path/to/files:/srv:ro \
  -v /path/to/certs:/certs:ro \
  go2serve --root /srv --cert /certs/cert.pem --key /certs/key.pem
```

Certificates are re-read from disk every 60 seconds, so rotation requires no restart.

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--version` | | Print version and exit |
| `--root` | *(required)* | Directory to serve files from |
| `--domain` | | Domain for Let's Encrypt. Enables HTTPS. Mutually exclusive with `--cert`/`--key`. |
| `--host` | | Expected hostname for HTTP→HTTPS redirect validation (manual HTTPS mode). Requests with a different `Host` header are rejected with 400. Has no effect in HTTP-only mode. |
| `--cache-dir` | `/certs` | Directory to cache Let's Encrypt certificates |
| `--cert` | | TLS certificate file (PEM). Requires `--key`. |
| `--key` | | TLS private key file (PEM). Requires `--cert`. |
| `--http-addr` | `:8080` | Address for the HTTP listener |
| `--https-addr` | `:8443` | Address for the HTTPS listener |
| `--timeout` | `30s` | Read and write timeout per request |
| `--hsts-max-age` | `0` | If non-zero, sets `Strict-Transport-Security: max-age=N` on HTTPS responses. Only takes effect in HTTPS mode. Do not enable until HTTPS is confirmed working. |
| `--csp` | | If set, the value is sent as the `Content-Security-Policy` header on every response. The appropriate value depends on the content being served. |
| `--no-listing` | | Disable directory listings. Directories without an `index.html` return 403 instead of a file listing. |
| `--max-conns` | `10000` | Maximum concurrent connections. `0` disables the limit. |
| `--rate-limit` | `100` | Per-IP requests per second. `0` disables rate limiting. |
| `--rate-burst` | `200` | Per-IP burst allowance (initial and max tokens). |
| `--trusted-proxies` | | Comma-separated IPs or CIDRs (e.g. `10.0.0.1,172.16.0.0/12`) whose `X-Forwarded-For` header is trusted for rate limiting. Without this, all requests are rate-limited by their direct connection address. |

In HTTPS mode, the HTTP listener redirects all requests to HTTPS. In HTTP-only mode, the HTTP listener serves files directly.

### Rate limiting

Rate limiting is per-IP using a token bucket algorithm. Each client IP gets an independent bucket that refills at `--rate-limit` tokens per second up to `--rate-burst`. Requests that exceed the limit receive a `429 Too Many Requests` response.

When behind a reverse proxy, all traffic arrives from the proxy's IP, which defeats per-IP rate limiting. Use `--trusted-proxies` to tell go2serve to extract the real client IP from the `X-Forwarded-For` header instead:

```bash
# Trust a single reverse proxy
go2serve --root /srv --trusted-proxies 10.0.0.1

# Trust a CIDR range (e.g. Docker network)
go2serve --root /srv --trusted-proxies 172.17.0.0/16

# Trust multiple proxies
go2serve --root /srv --trusted-proxies 10.0.0.1,10.0.0.2
```

Only the rightmost non-trusted IP in the `X-Forwarded-For` chain is used, which prevents clients from spoofing their IP by prepending fake entries to the header. Do not set `--trusted-proxies` to broad ranges — only include your actual proxy IPs.

---

## Stopping and restarting

The server shuts down gracefully on `SIGTERM` or `SIGINT` (Ctrl+C), finishing any in-flight requests before exiting.

There is no built-in HTTP control endpoint — a shutdown endpoint on a public file server would be a security risk.

**Docker:**

```bash
docker stop <container>    # graceful shutdown (sends SIGTERM)
docker start <container>   # start again
docker restart <container> # stop then start
```

The container lifecycle is the server lifecycle — starting and stopping the container starts and stops the server.

To start automatically on boot and restart on crash, add `--restart unless-stopped` to your `docker run` command:

```bash
docker run --restart unless-stopped -p 80:8080 \
  -v /path/to/files:/srv:ro \
  go2serve --root /srv
```

**Bare binary:**

```
Ctrl+C              # interactive
kill <pid>          # from another terminal (SIGTERM)
```
