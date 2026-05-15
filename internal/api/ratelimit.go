package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxAttempts = 5
	loginWindow      = 15 * time.Minute
)

// loginRateLimiter tracks failed login attempts per IP using a sliding
// window. check() gates the request without consuming an attempt;
// record() is called only on failure so successful logins never count
// against the limit.
type loginRateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	max     int
	window  time.Duration
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{
		buckets: make(map[string][]time.Time),
		max:     loginMaxAttempts,
		window:  loginWindow,
	}
}

// check returns false if ip has already hit the attempt ceiling in the
// current window. Does not record an attempt — call record() on failure.
func (l *loginRateLimiter) check(ip string) bool {
	cutoff := time.Now().Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, t := range l.buckets[ip] {
		if t.After(cutoff) {
			count++
		}
	}
	return count < l.max
}

// record stamps a failed attempt for ip and prunes stale entries.
func (l *loginRateLimiter) record(ip string) {
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	times := l.buckets[ip]
	j := 0
	for _, t := range times {
		if t.After(cutoff) {
			times[j] = t
			j++
		}
	}
	l.buckets[ip] = append(times[:j], now)
}

// cleanup removes per-IP buckets whose most recent attempt has expired.
func (l *loginRateLimiter) cleanup() {
	cutoff := time.Now().Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, times := range l.buckets {
		if len(times) == 0 || times[len(times)-1].Before(cutoff) {
			delete(l.buckets, ip)
		}
	}
}

// clientIP returns the real client IP. When behind a trusted proxy it
// prefers X-Forwarded-For (first hop) then X-Real-IP before falling
// back to RemoteAddr.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip := xff
			if idx := strings.IndexByte(xff, ','); idx >= 0 {
				ip = xff[:idx]
			}
			if parsed := strings.TrimSpace(ip); net.ParseIP(parsed) != nil {
				return parsed
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" && net.ParseIP(xri) != nil {
			return xri
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
