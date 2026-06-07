# Security

go2serve is built to be a small, auditable static file server with a deliberately
minimal attack surface. This document describes what it defends against, what it does
*not*, how its dependencies are controlled, and how to verify that a build matches the
published source.

## Reporting a vulnerability

Please report security issues privately via GitHub's **"Report a vulnerability"**
(Security → Advisories) on the [repository](https://github.com/crushdepth/go2serve).
This keeps the report confidential until a fix is available. Please do not open a public
issue for a suspected vulnerability.

## Supported versions

Security fixes are made against the latest tagged release. Older tags are not patched.

## Threat model

### What go2serve defends against

- **Path traversal and symlink escape.** Every request path is cleaned and resolved; a
  path that resolves outside the document root — including via symlinks — is rejected.
- **Hidden-file exposure.** Dotfiles (`.git/`, `.env`, `.htpasswd`, editor swap files,
  etc.) return 404 and are omitted from directory listings, whether referenced directly or
  reached through a non-dotfile symlink. `.well-known/` (RFC 8615) is the deliberate
  exception so `security.txt`, ACME challenges, and app-association files are served.
- **Transport security.** TLS 1.2+ only. Optional HSTS, configurable CSP, and the
  `X-Content-Type-Options`, `X-Frame-Options`, and `Referrer-Policy` headers are set on
  every response.
- **Resource exhaustion.** Per-IP token-bucket rate limiting, a concurrent-connection cap,
  bounded header/read timeouts, and a slowloris guard.
- **No control surface.** There is no HTTP shutdown or admin endpoint; the process
  lifecycle is the server lifecycle.

### What it does *not* defend against (non-goals)

- **Authentication / authorization.** go2serve serves everything under the document root
  to everyone. Put it behind a reverse proxy or VPN if you need access control.
- **A writable-volume symlink race.** There is an inherent TOCTOU window between resolving
  a symlink and opening the file (see the `safeFS` type documentation in `handler.go`). On the standard
  read-only volume deployment (`:ro`) this is not exploitable; on a writable volume an
  attacker who can create symlinks could race the check. **Serve from a read-only volume.**
- **CDN / DDoS-scale traffic.** The rate limiter protects a single instance; it is not a
  substitute for an edge network.

## Dependencies

go2serve has exactly **three** dependencies, all under `golang.org/x/*` — the Go team's
own extended standard library:

| Module | Purpose |
|---|---|
| `golang.org/x/crypto` | Let's Encrypt / ACME (`autocert`) |
| `golang.org/x/net` | connection-limit listener (`netutil`) |
| `golang.org/x/text` | indirect (transitive of the above) |

There is no third-party transitive sprawl. All three are pinned by cryptographic hash in
`go.sum`, and the build enforces those hashes with `go mod verify`. CGO is disabled, so the
binary is fully static.

## Reproducible builds

The build is deterministic: the Go toolchain is pinned (`toolchain` in `go.mod`), the
Docker base image is pinned by immutable `@sha256` digest, and builds use `-trimpath`
(strip local paths) and `-buildvcs=false` (drop the VCS stamp). For a given **OS and
architecture**, the same Go version and commit produce a **byte-identical** binary.
Reproducibility is per-platform: a Linux build will not match a macOS build, and a native
Linux build of a given architecture matches the Docker build for that architecture.

### Verify a build

With Go installed:

```bash
make verify        # runs `go mod verify`, builds, and prints the binary sha256
```

Run it from a clean checkout of a release tag; the printed `sha256` should match what
another builder on the same OS/architecture gets.

The pinned toolchain is used only under the default `GOTOOLCHAIN=auto` (which fetches
`go1.26.3` automatically). If you have overridden `GOTOOLCHAIN=local` with a different Go
patch, force the pinned one explicitly:

```bash
GOTOOLCHAIN=go1.26.3 make verify
```

A hash mismatch from a different toolchain means a different *compiler*, not a tampered
source — but matching the pinned toolchain is what makes the comparison meaningful.

## Verify the source is authentic

Release tags are **signed**. After fetching tags:

```bash
git verify-tag vX.Y.Z
```

A valid signature confirms the tagged commit is the maintainer's.

### Release signing key

Tags are signed with a dedicated SSH key (separate from any authentication key). As of this
writing the fingerprint is:

```
SHA256:jcxNYkyxwiNeByLFGSKwAIBNGVGNm+ROVPgFXfM2U1s   (ssh-ed25519)
```

The authoritative, always-current list of the maintainer's signing keys is published by
GitHub at <https://api.github.com/users/crushdepth/ssh_signing_keys> — that list, not the
snapshot above, is canonical if they ever differ. To verify a tag
locally, add the key to your SSH `allowed_signers` file and point git at it:

```bash
mkdir -p ~/.config/git
echo 'simon@isengard.biz ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGRnvWbtyYb2u5k0BGBZagl4qUXqfwXM8ZEe/q0HQBnV' \
  >> ~/.config/git/allowed_signers
git config gpg.ssh.allowedSignersFile ~/.config/git/allowed_signers
git verify-tag vX.Y.Z
```

### Maintainer: one-time signing setup

`make tag TAG=vX.Y.Z` creates a signed tag. The signing config is **repo-local** (it does
not affect your other repositories), so run these from the repo root:

```bash
# Generate a dedicated, passphrase-protected signing key (separate from auth keys):
ssh-keygen -t ed25519 -a 100 -C "go2serve release signing" -f ~/.ssh/id_ed25519_go2serve_signing

# Point this repo's signing at it:
git config --local gpg.format ssh
git config --local user.signingkey ~/.ssh/id_ed25519_go2serve_signing.pub
git config --local tag.gpgsign true
```

Then register the **public** key on GitHub as a **Signing Key** (Settings → SSH and GPG
keys → New SSH key → Key type: *Signing Key*) so signed tags show "Verified".

To avoid re-entering the passphrase each release, load the key into an agent for the
session: `ssh-add ~/.ssh/id_ed25519_go2serve_signing`.
