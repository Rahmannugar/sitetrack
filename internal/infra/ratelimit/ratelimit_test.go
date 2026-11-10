package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func TestConcurrentBurstAndRefill(t *testing.T) {
	l := testLimiter(t, Policy{RequestsPerSecond: 2, Burst: 3, RequestsPerWindow: 100, Window: time.Minute})
	now := time.Now()
	var allowed atomic.Int32
	var group sync.WaitGroup
	for range 30 {
		group.Go(func() {
			if ok, _ := l.allow("client", now); ok {
				allowed.Add(1)
			}
		})
	}
	group.Wait()
	if got := allowed.Load(); got != 3 {
		t.Fatalf("concurrent requests allowed = %d, want 3", got)
	}
	if ok, _ := l.allow("client", now.Add(500*time.Millisecond)); !ok {
		t.Fatal("expected one replenished token")
	}
	if ok, _ := l.allow("client", now.Add(500*time.Millisecond)); ok {
		t.Fatal("same token was used twice")
	}
	if ok, _ := l.allow("other", now); !ok {
		t.Fatal("different clients must have independent limits")
	}
}

func TestClientCapacityAndExpiry(t *testing.T) {
	l := testLimiter(t, Policy{RequestsPerSecond: 1, Burst: 1, RequestsPerWindow: 10, Window: time.Minute})
	now := time.Now()
	for i := range maxClients {
		l.clients[strconv.Itoa(i)] = client{
			limiter: rate.NewLimiter(1, 1), lastSeen: now,
		}
	}
	l.lastCleanup = now
	if ok, _ := l.allow("new", now); ok || len(l.clients) != maxClients {
		t.Fatal("full client registry must reject new clients without growing")
	}
	if ok, _ := l.allow("new", now.Add(clientTTL+time.Second)); !ok || len(l.clients) != 1 {
		t.Fatal("idle full buckets should expire and free capacity")
	}
}

func TestPeerIPAndHealthExemption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	l := testLimiter(t, Policy{RequestsPerSecond: 1, Burst: 1, RequestsPerWindow: 10, Window: time.Minute})
	router.Use(l.Middleware(true))
	for _, path := range []string{"/resource", "/health/live", "/health/ready"} {
		router.GET(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	request := func(path, peer, forwarded string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if got := request("/resource", "192.0.2.1:10", "198.51.100.1").Code; got != http.StatusNoContent {
		t.Fatalf("first request = %d", got)
	}
	w := request("/resource", "[::ffff:192.0.2.1]:20", "198.51.100.2")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatal("forwarded header and mapped IPv6 must not bypass the exhausted peer limit")
	}
	if got := request("/resource", "192.0.2.2:10", "198.51.100.1").Code; got != http.StatusNoContent {
		t.Fatalf("independent peer = %d", got)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		if got := request(path, "192.0.2.1:10", "").Code; got != http.StatusNoContent {
			t.Fatalf("health probe was limited: %d", got)
		}
	}
}

func testLimiter(t *testing.T, policy Policy) *Limiter {
	t.Helper()
	l, err := New(policy)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSlidingWindowAndIndependentEndpointPolicies(t *testing.T) {
	policy := Policy{RequestsPerSecond: 100, Burst: 100, RequestsPerWindow: 2, Window: time.Minute}
	login := testLimiter(t, policy)
	signup := testLimiter(t, policy)
	now := time.Now()
	for _, elapsed := range []time.Duration{0, 30 * time.Second} {
		if ok, _ := login.allow("client", now.Add(elapsed)); !ok {
			t.Fatal("initial window allowance was rejected")
		}
	}
	if ok, wait := login.allow("client", now.Add(59*time.Second)); ok || wait != time.Second {
		t.Fatal("sliding window must reject a third request and report its expiry")
	}
	if ok, _ := signup.allow("client", now.Add(59*time.Second)); !ok {
		t.Fatal("one endpoint must not consume another endpoint's quota")
	}
	if ok, _ := login.allow("client", now.Add(time.Minute)); !ok {
		t.Fatal("the first request should expire at the window boundary")
	}
	if ok, _ := login.allow("client", now.Add(time.Minute)); ok {
		t.Fatal("a window boundary must not reset the entire allowance")
	}
}

func TestDisabledPolicyDoesNotConsumeQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	l := testLimiter(t, Policy{RequestsPerSecond: 1, Burst: 1, RequestsPerWindow: 1, Window: time.Hour})
	router.GET("/work", l.Middleware(false), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for range 3 {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/work", nil))
		if w.Code != http.StatusNoContent {
			t.Fatal("disabled policy rejected a request")
		}
	}
	if len(l.clients) != 0 {
		t.Fatal("disabled policy allocated client state")
	}
}
