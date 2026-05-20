// Copyright (c) 2025 Simon Wilkinson. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/netutil"
)

// version is the current release. It can be overridden at build time:
//
//	go build -ldflags "-X main.version=v1.0" .
var version = "v1.0-alpha"

// config holds all runtime configuration parsed from command-line flags.
type config struct {
	root       string
	host       string
	domain     string
	cacheDir   string
	cert       string
	key        string
	httpAddr   string
	httpsAddr  string
	timeout    time.Duration
	hstsMaxAge int
	csp        string
	noListing  bool
	maxConns   int
	rateLimit  float64
	rateBurst  int
}

// main parses command-line flags and starts the server.
func main() {
	cfg := config{}
	showVersion := false
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&cfg.root, "root", "", "document root (required)")
	flag.StringVar(&cfg.domain, "domain", "", "domain for Let's Encrypt (enables HTTPS)")
	flag.StringVar(&cfg.host, "host", "", "expected hostname for HTTP→HTTPS redirect validation (manual HTTPS mode)")
	flag.StringVar(&cfg.cacheDir, "cache-dir", "/certs", "certificate cache directory (Let's Encrypt mode)")
	flag.StringVar(&cfg.cert, "cert", "", "TLS certificate file (manual HTTPS mode)")
	flag.StringVar(&cfg.key, "key", "", "TLS key file (manual HTTPS mode)")
	flag.StringVar(&cfg.httpAddr, "http-addr", ":8080", "HTTP listen address")
	flag.StringVar(&cfg.httpsAddr, "https-addr", ":8443", "HTTPS listen address")
	flag.DurationVar(&cfg.timeout, "timeout", 30*time.Second, "read/write timeout")
	flag.IntVar(&cfg.hstsMaxAge, "hsts-max-age", 0, "Strict-Transport-Security max-age in seconds; 0 disables the header")
	flag.StringVar(&cfg.csp, "csp", "", "Content-Security-Policy header value (omitted if empty)")
	flag.BoolVar(&cfg.noListing, "no-listing", false, "return 403 for directories that have no index.html")
	flag.IntVar(&cfg.maxConns, "max-conns", 10000, "maximum concurrent connections (0 = unlimited)")
	flag.Float64Var(&cfg.rateLimit, "rate-limit", 50, "per-IP requests per second (0 = unlimited)")
	flag.IntVar(&cfg.rateBurst, "rate-burst", 100, "per-IP burst allowance")
	flag.Parse()

	if showVersion {
		fmt.Println("go2serve", version)
		return
	}

	log.Printf("go2serve %s", version)
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

// run validates configuration, constructs the server(s), and blocks until a
// signal is received or a server fails to start.
func run(cfg config) error {
	if cfg.root == "" {
		return errors.New("--root is required")
	}
	// Resolve symlinks so the document root is canonical. This prevents an
	// attacker from swapping the root symlink to a different directory after
	// startup.
	canonical, err := filepath.EvalSymlinks(cfg.root)
	if err != nil {
		return fmt.Errorf("document root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return fmt.Errorf("document root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("document root %q is not a directory", cfg.root)
	}
	cfg.root = canonical

	autoMode := cfg.domain != ""
	manualMode := cfg.cert != "" || cfg.key != ""

	if autoMode && manualMode {
		return errors.New("--domain and --cert/--key are mutually exclusive")
	}
	if manualMode && (cfg.cert == "" || cfg.key == "") {
		return errors.New("--cert and --key must both be provided")
	}
	if manualMode && cfg.host == "" {
		return errors.New("--host is required in manual HTTPS mode to prevent open redirects")
	}
	if cfg.rateLimit < 0 {
		return errors.New("--rate-limit must not be negative")
	}
	if cfg.rateLimit > 0 && cfg.rateBurst < 1 {
		return errors.New("--rate-burst must be at least 1 when rate limiting is enabled")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// HSTS must not be set in HTTP-only mode. In HTTPS mode it is only
	// reached via the HTTPS listener, so it is safe to include.
	hstsValue := ""
	if (autoMode || manualMode) && cfg.hstsMaxAge > 0 {
		hstsValue = fmt.Sprintf("max-age=%d", cfg.hstsMaxAge)
	}

	fileHandler, err := newFileHandler(cfg.root, !cfg.noListing, hstsValue, cfg.csp)
	if err != nil {
		return err
	}

	var rl *rateLimiter
	if cfg.rateLimit > 0 {
		rl = newRateLimiter(cfg.rateLimit, cfg.rateBurst)
		defer rl.stop()
	}

	var handler http.Handler = fileHandler
	if rl != nil {
		handler = rl.wrap(handler)
	}

	if !autoMode && !manualMode {
		// HTTP-only mode
		srv := newServer(cfg.httpAddr, handler, cfg.timeout)
		ln, err := listen(cfg.httpAddr, cfg.maxConns)
		if err != nil {
			return err
		}
		errCh := make(chan error, 1)
		go func() {
			log.Printf("serving %s on http://%s", cfg.root, cfg.httpAddr)
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
		}
		return shutdown(srv)
	}

	// HTTPS mode: HTTP redirect + HTTPS file server
	var tlsConfig *tls.Config
	var httpHandler http.Handler

	if autoMode {
		manager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.domain),
			Cache:      autocert.DirCache(cfg.cacheDir),
		}
		tlsConfig = manager.TLSConfig()
		httpHandler = manager.HTTPHandler(redirectHandler(cfg.domain))
	} else {
		cc := &certCache{certFile: cfg.cert, keyFile: cfg.key}
		tlsConfig = &tls.Config{
			GetCertificate: cc.get,
			MinVersion:     tls.VersionTLS12,
		}
		httpHandler = redirectHandler(cfg.host)
	}

	if rl != nil {
		httpHandler = rl.wrap(httpHandler)
	}

	httpSrv := newServer(cfg.httpAddr, httpHandler, cfg.timeout)
	httpsSrv := newServer(cfg.httpsAddr, handler, cfg.timeout)
	httpsSrv.TLSConfig = tlsConfig

	httpLn, err := listen(cfg.httpAddr, cfg.maxConns)
	if err != nil {
		return err
	}
	httpsLn, err := listen(cfg.httpsAddr, cfg.maxConns)
	if err != nil {
		httpLn.Close()
		return err
	}

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		log.Printf("HTTP redirect on %s", cfg.httpAddr)
		if err := httpSrv.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		defer wg.Done()
		log.Printf("serving %s on https://%s", cfg.root, cfg.httpsAddr)
		if err := httpsSrv.ServeTLS(httpsLn, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		stop()
		shutdown(httpSrv)
		shutdown(httpsSrv)
		wg.Wait()
		return err
	case <-ctx.Done():
	}

	log.Println("shutting down...")
	if err := shutdown(httpSrv); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	if err := shutdown(httpsSrv); err != nil {
		log.Printf("HTTPS shutdown: %v", err)
	}
	wg.Wait()
	return nil
}

// listen creates a TCP listener, optionally wrapped with a connection limit.
func listen(addr string, maxConns int) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		ln = netutil.LimitListener(ln, maxConns)
	}
	return ln, nil
}

// newServer constructs an http.Server with conservative timeouts to limit
// resource exhaustion from slow or idle clients.
func newServer(addr string, h http.Handler, timeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       timeout,
		WriteTimeout:      timeout,
		IdleTimeout:       timeout * 2,
	}
}

// shutdown gracefully stops a server, allowing in-flight requests up to 10
// seconds to complete before forcing closure.
func shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// redirectHandler returns a handler that issues a permanent redirect from HTTP
// to HTTPS. If host is non-empty, requests whose Host header does not match
// are rejected with 400 to prevent open-redirect abuse.
func redirectHandler(host string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host != "" {
			rHost := r.Host
			// Strip the port if present before comparing.
			if h, _, err := net.SplitHostPort(rHost); err == nil {
				rHost = h
			}
			if !strings.EqualFold(rHost, host) {
				http.Error(w, "Bad Request", http.StatusBadRequest)
				return
			}
		}
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// certCache loads a TLS certificate from disk with a 60-second TTL,
// allowing zero-downtime certificate rotation without a server restart.
type certCache struct {
	certFile string
	keyFile  string
	mu       sync.RWMutex
	cert     *tls.Certificate
	loadedAt time.Time
}

// get implements tls.Config.GetCertificate, returning a cached certificate and
// reloading from disk when the 60-second TTL expires. On reload failure the
// existing certificate is kept rather than breaking active TLS connections.
func (c *certCache) get(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	if c.cert != nil && time.Since(c.loadedAt) < 60*time.Second {
		cert := c.cert
		c.mu.RUnlock()
		return cert, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert != nil && time.Since(c.loadedAt) < 60*time.Second {
		return c.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		if c.cert != nil {
			// Keep serving the existing certificate rather than breaking all TLS
			// handshakes. Reset the TTL so we don't hammer the filesystem on
			// every connection.
			log.Printf("certCache: reload failed, keeping existing certificate: %v", err)
			c.loadedAt = time.Now()
			return c.cert, nil
		}
		return nil, err
	}
	c.cert = &cert
	c.loadedAt = time.Now()
	return c.cert, nil
}
