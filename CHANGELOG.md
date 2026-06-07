# Changelog

All notable changes to go2serve are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- Supply-chain hardening for verifiable build-it-yourself distribution:
  - The Go toolchain is now pinned (`toolchain` directive in `go.mod`), and the
    Docker base image is pinned by immutable `@sha256` digest (`golang:1.26.3-alpine`).
  - Builds are reproducible: `-trimpath` and `-buildvcs=false` are applied in both
    the Dockerfile and the new native build target, so the same Go version and commit
    produce a byte-identical binary.
  - `go mod verify` now runs during the Docker build, failing if any module deviates
    from `go.sum`.
- Added `SECURITY.md` documenting the threat model, dependency posture, vulnerability
  reporting, and build/source verification.

### Added
- `make build-bin` (native reproducible build), `make verify` (verify modules and print
  the binary sha256), and `make tag TAG=vX.Y.Z` (create a signed release tag).

## [1.1.0] — 2026-06-01

### Security
- Dotfiles (`.git/`, `.env`, `.htpasswd`, editor swap files, etc.) are no
  longer served and are hidden from directory listings. The `.well-known/`
  directory (RFC 8615) is still served. Symlink aliases that resolve to a
  dotfile (e.g. `foo` → `.git`) are also blocked.
- Updated `golang.org/x/net` to v0.55.0, resolving CVE-2026-39821 in
  `x/net/idna`.
- The rate limiter now aggregates IPv6 clients by their /64 prefix, preventing
  per-/128 bucket explosion and source-address-rotation evasion. IPv4 clients
  are still keyed individually (/32).

### Changed
- Read and write timeouts are now independent. A new `--write-timeout` flag
  (default `0`, meaning no response deadline) prevents large downloads from
  being truncated by `--timeout`, which now governs the read side only. The
  5-second header-read timeout (slowloris guard) is unchanged.

## [1.0] — 2025

- Initial release: static file server with path-traversal-safe filesystem,
  per-IP token-bucket rate limiting, HTTP / Let's Encrypt / manual-TLS modes,
  security headers, and a `scratch`-based non-root Docker image.
