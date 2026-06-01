// Copyright (c) 2025 Simon Wilkinson. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ipEntry tracks the token bucket state for a single client IP address.
type ipEntry struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// rateLimiter implements a per-IP token bucket rate limiter. Each IP address
// gets its own bucket that refills at rate tokens per second up to a maximum of
// burst tokens. When trustedProxies is configured, requests from those IPs are
// rate-limited by the client IP in X-Forwarded-For instead of RemoteAddr.
// A background goroutine periodically evicts stale entries to bound memory usage.
type rateLimiter struct {
	rate           float64
	burst          float64
	trustedProxies []*net.IPNet
	ips            sync.Map
	done           chan struct{}
}

// newRateLimiter creates a rateLimiter that allows rate requests per second
// per IP with an initial and maximum capacity of burst. It starts a background
// cleanup goroutine that must be stopped by calling stop.
func newRateLimiter(rate float64, burst int, trustedProxies []*net.IPNet) *rateLimiter {
	rl := &rateLimiter{
		rate:           rate,
		burst:          float64(burst),
		trustedProxies: trustedProxies,
		done:           make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// stop terminates the background cleanup goroutine.
func (rl *rateLimiter) stop() {
	close(rl.done)
}

// allow reports whether a request from ip is permitted under the rate limit.
// It refills the token bucket based on elapsed time since the last request,
// then consumes one token. Returns false when no tokens are available.
func (rl *rateLimiter) allow(ip string) bool {
	val, ok := rl.ips.Load(ip)
	if !ok {
		val, _ = rl.ips.LoadOrStore(ip, &ipEntry{tokens: rl.burst})
	}
	entry := val.(*ipEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	if !entry.lastSeen.IsZero() {
		entry.tokens += now.Sub(entry.lastSeen).Seconds() * rl.rate
		if entry.tokens > rl.burst {
			entry.tokens = rl.burst
		}
	}
	entry.lastSeen = now

	if entry.tokens < 1 {
		return false
	}
	entry.tokens--
	return true
}

// clientIP extracts the client IP for rate limiting. If the direct peer is a
// trusted proxy, the X-Forwarded-For chain is walked right-to-left, skipping
// trusted proxy IPs, to find the first non-trusted client IP. Entries that
// cannot be parsed as IPs are ignored (treated as corrupt/spoofed). If no
// valid non-trusted IP is found, RemoteAddr is used.
func (rl *rateLimiter) clientIP(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if len(rl.trustedProxies) == 0 {
		return ip
	}
	if !rl.isTrusted(net.ParseIP(ip)) {
		return ip
	}
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if xff == "" {
		return ip
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		candidateIP := net.ParseIP(candidate)
		if candidateIP == nil {
			continue
		}
		if !rl.isTrusted(candidateIP) {
			return candidateIP.String()
		}
	}
	return ip
}

// bucketKey normalises a client IP into the key used for its rate-limit bucket.
// IPv6 addresses are aggregated to their /64 prefix: a single client is
// routinely assigned a whole /64 (or larger), so keying on the full /128 would
// let one client mint unlimited buckets — exhausting memory between cleanup
// passes and rotating addresses to evade the per-IP limit. IPv4 addresses are
// keyed in full (/32). Unparseable input is returned unchanged.
func bucketKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}

func (rl *rateLimiter) isTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, cidr := range rl.trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// wrap returns an http.Handler middleware that extracts the client IP and
// rejects requests that exceed the rate limit with 429 Too Many Requests.
func (rl *rateLimiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(bucketKey(rl.clientIP(r))) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// cleanup runs in its own goroutine and evicts IP entries that have been idle
// long enough to have fully refilled their token bucket. It runs every 60
// seconds and exits when the done channel is closed.
func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			stale := time.Now().Add(-2 * time.Duration(rl.burst/rl.rate) * time.Second)
			rl.ips.Range(func(key, val any) bool {
				entry := val.(*ipEntry)
				entry.mu.Lock()
				if entry.lastSeen.Before(stale) {
					rl.ips.Delete(key)
				}
				entry.mu.Unlock()
				return true
			})
		}
	}
}
