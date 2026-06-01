// Copyright (c) 2025 Simon Wilkinson. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRoot builds a temporary document root containing a mix of normal files,
// dotfiles, and a .well-known directory, plus a subdirectory without an
// index.html (so it produces a listing).
func setupRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<h1>home</h1>")
	write("public.txt", "ok")
	write(".env", "SECRET=1")
	write(".git/config", "[core]")
	write(".well-known/security.txt", "Contact: mailto:a@b.c")
	write("sub/visible.txt", "v")
	write("sub/.hidden", "h")
	return root
}

func newTestHandler(t *testing.T, root string, listing bool) http.Handler {
	t.Helper()
	h, err := newFileHandler(root, listing, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func doGet(t *testing.T, h http.Handler, target string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(body)
}

func TestDotfilesReturn404(t *testing.T) {
	h := newTestHandler(t, setupRoot(t), true)
	for _, p := range []string{"/.env", "/.git/config", "/.git/", "/sub/.hidden"} {
		if code, _ := doGet(t, h, p); code != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", p, code)
		}
	}
}

func TestNormalFilesServed(t *testing.T) {
	h := newTestHandler(t, setupRoot(t), true)
	if code, body := doGet(t, h, "/public.txt"); code != http.StatusOK || body != "ok" {
		t.Errorf("GET /public.txt: got %d %q, want 200 \"ok\"", code, body)
	}
	if code, body := doGet(t, h, "/"); code != http.StatusOK || !strings.Contains(body, "home") {
		t.Errorf("GET /: got %d, want 200 with index.html content", code)
	}
}

func TestWellKnownServed(t *testing.T) {
	h := newTestHandler(t, setupRoot(t), true)
	code, body := doGet(t, h, "/.well-known/security.txt")
	if code != http.StatusOK || !strings.Contains(body, "Contact:") {
		t.Errorf("GET /.well-known/security.txt: got %d %q, want 200 with contact", code, body)
	}
}

func TestListingHidesDotfiles(t *testing.T) {
	h := newTestHandler(t, setupRoot(t), true)
	code, body := doGet(t, h, "/sub/")
	if code != http.StatusOK {
		t.Fatalf("GET /sub/: got %d, want 200 listing", code)
	}
	if !strings.Contains(body, "visible.txt") {
		t.Errorf("listing should contain visible.txt; body=%q", body)
	}
	if strings.Contains(body, ".hidden") {
		t.Errorf("listing must not expose .hidden; body=%q", body)
	}
}

// TestSymlinkAliasToDotfileBlocked verifies that a symlink with a non-dot name
// pointing at a dotfile directory cannot be used to bypass the hidden-file
// policy: the resolved path is re-checked, so access is still denied.
func TestSymlinkAliasToDotfileBlocked(t *testing.T) {
	root := setupRoot(t)
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	h := newTestHandler(t, root, true)
	for _, p := range []string{"/alias/config", "/alias/"} {
		if code, _ := doGet(t, h, p); code != http.StatusNotFound {
			t.Errorf("GET %s (alias → .git): got %d, want 404", p, code)
		}
	}
}

func TestIsHidden(t *testing.T) {
	cases := map[string]bool{
		"/public.txt":               false,
		"/sub/visible.txt":          false,
		"/.env":                     true,
		"/.git/config":              true,
		"/sub/.hidden":              true,
		"/.well-known/security.txt": false,
		"/.well-known/":             false,
		"/.well-known/.secret":      true,
		"/":                         false,
	}
	for name, want := range cases {
		if got := isHidden(name); got != want {
			t.Errorf("isHidden(%q) = %v, want %v", name, got, want)
		}
	}
}
