package ratelimit

import (
	"errors"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const (
	maxClients    = 10000
	clientTTL     = 10 * time.Minute
	cleanupPeriod = time.Minute
)

type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	accepted []time.Time
}

type Policy struct {
	RequestsPerSecond int
	Burst             int
	RequestsPerWindow int
	Window            time.Duration
}

type Limiter struct {
	mu          sync.Mutex
	clients     map[string]client
	policy      Policy
	lastCleanup time.Time
}

func New(policy Policy) (*Limiter, error) {
	if policy.RequestsPerSecond < 1 || policy.Burst < 1 || policy.RequestsPerWindow < 1 || policy.Window <= 0 {
		return nil, errors.New("rate-limit policy must have a positive refill rate, burst, window and request count")
	}
	return &Limiter{clients: make(map[string]client), policy: policy}, nil
}

func (l *Limiter) Middleware(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled || c.FullPath() == "/health/live" || c.FullPath() == "/health/ready" {
			c.Next()
			return
		}
		peer, err := netip.ParseAddrPort(c.Request.RemoteAddr)
		key := "unknown"
		if err == nil {
			key = peer.Addr().Unmap().String()
		}
		if allowed, wait := l.allow(key, time.Now()); !allowed {
			seconds := max(1, int(math.Ceil(wait.Seconds())))
			c.Header("Retry-After", strconv.Itoa(seconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

func (l *Limiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastCleanup) >= cleanupPeriod {
		for key, entry := range l.clients {
			// We expire full buckets so eviction cannot restore spent tokens.
			if now.Sub(entry.lastSeen) >= max(clientTTL, l.policy.Window) && entry.limiter.TokensAt(now) >= float64(l.policy.Burst) {
				delete(l.clients, key)
			}
		}
		l.lastCleanup = now
	}
	entry, found := l.clients[key]
	if !found {
		// We reject new clients at capacity to preserve active clients' quotas.
		if len(l.clients) >= maxClients {
			return false, cleanupPeriod
		}
		entry = client{limiter: rate.NewLimiter(rate.Limit(l.policy.RequestsPerSecond), l.policy.Burst)}
	}
	if now.Before(entry.lastSeen) {
		now = entry.lastSeen
	}
	entry.lastSeen = now
	cutoff := now.Add(-l.policy.Window)
	expired := 0
	for expired < len(entry.accepted) && !entry.accepted[expired].After(cutoff) {
		expired++
	}
	entry.accepted = entry.accepted[:copy(entry.accepted, entry.accepted[expired:])]
	var wait time.Duration
	if len(entry.accepted) >= l.policy.RequestsPerWindow {
		wait = entry.accepted[0].Add(l.policy.Window).Sub(now)
	}
	if tokens := entry.limiter.TokensAt(now); tokens < 1 {
		refill := time.Duration(math.Ceil((1 - tokens) / float64(l.policy.RequestsPerSecond) * float64(time.Second)))
		wait = max(wait, refill)
	}
	if wait > 0 || !entry.limiter.AllowN(now, 1) {
		l.clients[key] = entry
		return false, max(wait, time.Nanosecond)
	}
	entry.accepted = append(entry.accepted, now)
	l.clients[key] = entry
	return true, 0
}
