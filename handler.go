// Copyright (c) 2025 Simon Wilkinson. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// safeFS is an http.FileSystem that resolves symlinks and rejects any path
// that escapes the document root, and optionally disables directory listings.
// Known limitation: there is an inherent TOCTOU window between EvalSymlinks
// and os.Open. On a read-only volume (the standard deployment) this is not
// exploitable; on a writable volume an attacker who can create symlinks could
// race the check. Closing this fully requires O_NOFOLLOW which is not
// available in pure Go without platform-specific syscall code.
type safeFS struct {
	root    string // canonical absolute path, no trailing separator
	listing bool
}

// newSafeFS constructs a safeFS rooted at root, resolving any symlinks in the
// root path itself so the canonical path is fixed at startup.
func newSafeFS(root string, listing bool) (safeFS, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return safeFS{}, fmt.Errorf("document root: %w", err)
	}
	abs, err := filepath.Abs(canonical)
	if err != nil {
		return safeFS{}, fmt.Errorf("document root: %w", err)
	}
	return safeFS{root: abs, listing: listing}, nil
}

// Open resolves name relative to the document root and opens it. Symlinks are
// followed but the resolved path must remain within the root; any attempt to
// escape — including via symlinks — is rejected with a permission error.
func (s safeFS) Open(name string) (http.File, error) {
	cleaned := filepath.Join(s.root, filepath.FromSlash(path.Clean("/"+name)))
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return nil, err
	}
	// Reject any path that resolves outside the root, including via symlinks.
	// filepath.Rel is used rather than a string-prefix check because the
	// prefix approach breaks when root is "/" ("/" + sep == "//").
	rel, err := filepath.Rel(s.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, os.ErrPermission
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	// When listings are disabled, deny directory opens that have no index.html.
	// Returning os.ErrPermission here causes http.FileServer to respond with
	// 403. If index.html exists and is within the root, we allow the directory
	// open — the file server will open and serve index.html via a separate
	// Open call.
	// Note: http.FileServer's dirList treats errors from Readdir as 500, so
	// the check must happen here rather than in a Readdir override.
	// Note: os.Stat must not be used here because it follows symlinks — an
	// index.html symlink pointing outside the root would pass the os.Stat
	// check, then fail when the file server calls safeFS.Open on it, causing
	// the file server to fall through to dirList and produce a listing.
	if !s.listing {
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if stat.IsDir() {
			indexResolved, err := filepath.EvalSymlinks(filepath.Join(resolved, "index.html"))
			if err != nil {
				// index.html absent or broken symlink: deny to prevent listing.
				f.Close()
				return nil, os.ErrPermission
			}
			// index.html must itself resolve within the root.
			rel2, err := filepath.Rel(s.root, indexResolved)
			if err != nil || rel2 == ".." || strings.HasPrefix(rel2, ".."+string(filepath.Separator)) {
				f.Close()
				return nil, os.ErrPermission
			}
		}
	}
	return f, nil
}

// Pre-allocated header values avoid per-request slice allocations.
// Keys are already in canonical MIME form, so map assignment bypasses
// the textproto.CanonicalMIMEHeaderKey scan done by Header.Set.
var (
	hdrNoSniff  = []string{"nosniff"}
	hdrDeny     = []string{"DENY"}
	hdrReferrer = []string{"strict-origin-when-cross-origin"}
)

// newFileHandler returns an http.Handler that serves static files from root,
// adding security headers on every response. hsts and csp are included as
// response headers only when non-empty.
func newFileHandler(root string, listing bool, hsts, csp string) (http.Handler, error) {
	safe, err := newSafeFS(root, listing)
	if err != nil {
		return nil, err
	}
	srv := http.FileServer(safe)

	// Pre-build optional header slices so the closure captures them without
	// allocating on every request.
	var hdrHSTS, hdrCSP []string
	if hsts != "" {
		hdrHSTS = []string{hsts}
	}
	if csp != "" {
		hdrCSP = []string{csp}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h["X-Content-Type-Options"] = hdrNoSniff
		h["X-Frame-Options"] = hdrDeny
		h["Referrer-Policy"] = hdrReferrer
		if hdrHSTS != nil {
			h["Strict-Transport-Security"] = hdrHSTS
		}
		if hdrCSP != nil {
			h["Content-Security-Policy"] = hdrCSP
		}
		srv.ServeHTTP(w, r)
	}), nil
}
