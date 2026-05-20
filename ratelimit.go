// Copyright (c) 2025 Simon Wilkinson. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"net"
	"net/http"
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
// burst tokens. A background goroutine periodically evicts stale entries to
// bound memory usage.
type rateLimiter struct {
	rate  float64
	burst float64
	ips   sync.Map
	done  chan struct{}
}

// newRateLimiter creates a rateLimiter that allows rate requests per second
// per IP with an initial and maximum capacity of burst. It starts a background
// cleanup goroutine that must be stopped by calling stop.
func newRateLimiter(rate float64, burst int) *rateLimiter {
	rl := &rateLimiter{
		rate:  rate,
		burst: float64(burst),
		done:  make(chan struct{}),
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
	now := time.Now()

	val, ok := rl.ips.Load(ip)
	if !ok {
		val, _ = rl.ips.LoadOrStore(ip, &ipEntry{tokens: rl.burst, lastSeen: now})
	}
	entry := val.(*ipEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	elapsed := now.Sub(entry.lastSeen).Seconds()
	entry.tokens += elapsed * rl.rate
	if entry.tokens > rl.burst {
		entry.tokens = rl.burst
	}
	entry.lastSeen = now

	if entry.tokens < 1 {
		return false
	}
	entry.tokens--
	return true
}

// wrap returns an http.Handler middleware that extracts the client IP from
// RemoteAddr and rejects requests that exceed the rate limit with 429 Too
// Many Requests.
func (rl *rateLimiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}
		if !rl.allow(ip) {
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
