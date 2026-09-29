package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Two limits, both checked before anything is sent:
//
//   - per address: one drip per cooldown, whoever asks for it;
//   - per caller (client IP): at most callerLimit addresses per
//     callerWindow, so one bot can't fund a fleet one address at a time
//     faster than the batch endpoint would let it.
//
// Every response carries the caller's quota as RateLimit-* headers,
// and a 429 carries Retry-After, so a bot knows exactly when to come
// back instead of retrying blindly.

type callerWindow struct {
	start time.Time
	used  int
}

type limiter struct {
	mu           sync.Mutex
	now          func() time.Time
	cooldown     time.Duration
	lastRequest  map[string]time.Time // address -> when it was last funded
	callerLimit  int                  // 0: no per-caller limit
	callerWindow time.Duration
	callers      map[string]*callerWindow
	lastPrune    time.Time
}

func newLimiter(cooldown time.Duration, callerLimit int, window time.Duration) *limiter {
	return &limiter{
		now:          time.Now,
		cooldown:     cooldown,
		lastRequest:  map[string]time.Time{},
		callerLimit:  callerLimit,
		callerWindow: window,
		callers:      map[string]*callerWindow{},
	}
}

// addressWait is how long address must still wait; 0 when it may be funded.
func (l *limiter) addressWait(address string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.addressWaitLocked(address, l.now())
}

func (l *limiter) addressWaitLocked(address string, now time.Time) time.Duration {
	if last, ok := l.lastRequest[address]; ok && now.Sub(last) < l.cooldown {
		return l.cooldown - now.Sub(last)
	}
	return 0
}

// quota is a caller's standing in its current window.
type quota struct {
	limit     int
	remaining int
	reset     time.Duration // until the window starts over
}

func (l *limiter) quotaLocked(caller string, now time.Time) quota {
	if l.callerLimit <= 0 {
		return quota{}
	}
	w := l.callers[caller]
	if w == nil || now.Sub(w.start) >= l.callerWindow {
		return quota{limit: l.callerLimit, remaining: l.callerLimit, reset: l.callerWindow}
	}
	return quota{limit: l.callerLimit, remaining: l.callerLimit - w.used, reset: l.callerWindow - now.Sub(w.start)}
}

func (l *limiter) quota(caller string) quota {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quotaLocked(caller, l.now())
}

// reservation is what reserve recorded, so a failed send can hand it back.
type reservation struct {
	caller    string
	addresses []string
	at        time.Time
	window    *callerWindow
}

// reserve records addresses as funded and counts them against caller,
// atomically: two concurrent requests for the same address can't both
// pass. It returns the addresses still cooling down (with their waits)
// and reserves only the rest; ok is false, with nothing reserved, when
// the caller's quota can't cover them.
func (l *limiter) reserve(caller string, addresses []string) (r reservation, waiting map[string]time.Duration, q quota, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneLocked(now)

	waiting = map[string]time.Duration{}
	var ready []string
	for _, a := range addresses {
		if wait := l.addressWaitLocked(a, now); wait > 0 {
			waiting[a] = wait
		} else {
			ready = append(ready, a)
		}
	}
	q = l.quotaLocked(caller, now)
	if len(ready) == 0 {
		return reservation{}, waiting, q, true
	}
	if l.callerLimit > 0 && len(ready) > q.remaining {
		return reservation{}, waiting, q, false
	}

	r = reservation{caller: caller, addresses: ready, at: now}
	for _, a := range ready {
		l.lastRequest[a] = now
	}
	if l.callerLimit > 0 {
		w := l.callers[caller]
		if w == nil || now.Sub(w.start) >= l.callerWindow {
			w = &callerWindow{start: now}
			l.callers[caller] = w
		}
		w.used += len(ready)
		r.window = w
		q = l.quotaLocked(caller, now)
	}
	return r, waiting, q, true
}

// release undoes a reservation whose send failed, so a failure burns
// neither the addresses' cooldowns nor the caller's quota. An entry
// something newer has replaced since is left alone.
func (l *limiter) release(r reservation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, a := range r.addresses {
		if last, ok := l.lastRequest[a]; ok && last.Equal(r.at) {
			delete(l.lastRequest, a)
		}
	}
	if r.window != nil && l.callers[r.caller] == r.window {
		r.window.used -= len(r.addresses)
		if r.window.used < 0 {
			r.window.used = 0
		}
	}
}

// pruneLocked drops expired entries now and then, so the maps don't
// grow with every address ever funded.
func (l *limiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < 10*time.Minute {
		return
	}
	l.lastPrune = now
	for a, t := range l.lastRequest {
		if now.Sub(t) >= l.cooldown {
			delete(l.lastRequest, a)
		}
	}
	for c, w := range l.callers {
		if now.Sub(w.start) >= l.callerWindow {
			delete(l.callers, c)
		}
	}
}

// setQuotaHeaders describes the caller's quota the way the IETF
// RateLimit header fields draft does (RateLimit-Limit/-Remaining/-Reset,
// reset in seconds), plus RateLimit-Policy naming the window.
func setQuotaHeaders(h http.Header, q quota, window time.Duration) {
	if q.limit == 0 {
		return
	}
	h.Set("RateLimit-Limit", strconv.Itoa(q.limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(max(q.remaining, 0)))
	h.Set("RateLimit-Reset", strconv.Itoa(ceilSeconds(q.reset)))
	h.Set("RateLimit-Policy", strconv.Itoa(q.limit)+";w="+strconv.Itoa(ceilSeconds(window)))
}

func ceilSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// clientIP is who's asking: the connection's address, or, when that's
// a trusted reverse proxy (Caddy in front of the public faucet), the
// nearest address in X-Forwarded-For that isn't one. Headers from
// anyone else are ignored, so a direct caller can't pick its own
// identity.
func clientIP(r *http.Request, trusted map[string]bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !trusted[host] {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		if net.ParseIP(hop) == nil {
			break
		}
		if !trusted[hop] {
			return hop
		}
	}
	return host
}

func parseTrusted(list string) map[string]bool {
	out := map[string]bool{}
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out[s] = true
		}
	}
	return out
}
