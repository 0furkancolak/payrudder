package httpapi

import (
	"sync"
	"time"
)

// retryAfterMinute is the Retry-After of a visitor over a per-minute limit.
const retryAfterMinute = "60"

// publicLimits are the per-address limits of the routes that need no key: the hosted pages and the sign-in routes. The per-minute counts are the constants
// next to the handlers they guard (signInsPerMinute, hostedPerMinute, ...); the account lock in plans-svc handles one account being guessed, these handle one
// address trying many.
type publicLimits struct {
	// pages limits the pages of a hosted invoice or portal, pays the POSTs that start a payment on them.
	pages, pays hostedLimiter
	// signIns and signUps limit the public sign-in and sign-up routes.
	signIns, signUps hostedLimiter
	// resets limits "forgot password" requests, tokenUses the routes that spend a mailed token (verify, reset, accept an invitation).
	resets, tokenUses hostedLimiter
	// otpRequests, otpVerifies and oauths limit the one-time code and provider sign-in routes.
	otpRequests, otpVerifies, oauths hostedLimiter
}

// hostedLimiter lets a visitor load a handful of pages a minute. A token is 256 random bits, so guessing one is hopeless; this keeps scrapers and
// accidents from hammering billing-svc through a public route. It counts by the connection's address (a proxy in front should pass it on as such).
type hostedLimiter struct {
	mu     sync.Mutex
	window map[string]*hostedWindow
	last   time.Time
}

type hostedWindow struct {
	start time.Time
	n     int
}

const (
	hostedPerMinute = 60
	// hostedPaysPerMinute limits the POSTs that start a payment, per address.
	hostedPaysPerMinute = 10
	hostedMaxKeys       = 50000
	// hostedSweepWhenFull is the least time between two sweeps of a full table.
	hostedSweepWhenFull = time.Second
)

func (l *hostedLimiter) allow(key string, now time.Time) bool {
	return l.allowN(key, now, hostedPerMinute)
}

// allowN counts a request of key and reports whether it is within perMinute requests in the current minute.
func (l *hostedLimiter) allowN(key string, now time.Time, perMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window == nil {
		l.window = map[string]*hostedWindow{}
	}
	// Expired addresses are swept once a minute; a table that is full is swept at most once a second, so a flood of new addresses does not make every
	// request walk the whole table.
	if now.Sub(l.last) > time.Minute || (len(l.window) >= hostedMaxKeys && now.Sub(l.last) > hostedSweepWhenFull) {
		for k, w := range l.window {
			if now.Sub(w.start) > time.Minute {
				delete(l.window, k)
			}
		}
		l.last = now
	}
	w := l.window[key]
	if w == nil || now.Sub(w.start) > time.Minute {
		if w == nil && len(l.window) >= hostedMaxKeys {
			// Every address in the table is within its minute: make room by forgetting one, so memory stays bounded and a new visitor is still served.
			for k := range l.window {
				delete(l.window, k)
				break
			}
		}
		w = &hostedWindow{start: now}
		l.window[key] = w
	}
	w.n++
	return w.n <= perMinute
}
