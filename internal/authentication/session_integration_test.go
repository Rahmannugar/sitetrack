package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"sitetrack/internal/infra/openapi"
)

func verifiedAccount(t *testing.T, service *Service) SignupInput {
	t.Helper()
	input, id := newAccount(t, service)
	_, code := queuedCode(t, service, id)
	if err := service.Verify(context.Background(), VerificationInput{Email: input.Email, Code: code}); err != nil {
		t.Fatal(err)
	}
	return input
}

func loggedIn(t *testing.T, service *Service, input SignupInput) TokenPair {
	t.Helper()
	pair, err := service.Login(context.Background(), LoginInput{Email: input.Email, Password: input.Password})
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestLoginReplacementAndLogoutRevokeImmediately(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	first := loggedIn(t, s, input)
	oldPrincipal, err := s.Authenticate(context.Background(), first.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	second := loggedIn(t, s, input)
	if _, err := s.Authenticate(context.Background(), first.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("previous JWT survived replacement login")
	}
	if _, err := s.Refresh(context.Background(), first.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("previous refresh token survived replacement login")
	}
	if err := s.Logout(context.Background(), oldPrincipal); err != nil {
		t.Fatal(err)
	}
	principal, err := s.Authenticate(context.Background(), second.AccessToken)
	if err != nil {
		t.Fatal("stale logout revoked the replacement session")
	}
	var count int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM sitetrack.auth_sessions WHERE user_id=$1", principal.User.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("account has more than one session")
	}
	if err := s.Logout(context.Background(), principal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(context.Background(), second.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("JWT survived logout")
	}
	if _, err := s.Refresh(context.Background(), second.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("refresh survived logout")
	}
}

func TestConcurrentLoginsLeaveOneSession(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	results := make(chan TokenPair, 4)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			pair, err := s.Login(context.Background(), LoginInput{Email: input.Email, Password: input.Password})
			if err != nil {
				t.Error(err)
				return
			}
			results <- pair
		})
	}
	group.Wait()
	close(results)
	active := 0
	for pair := range results {
		if _, err := s.Authenticate(context.Background(), pair.AccessToken); err == nil {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active sessions after concurrent login = %d", active)
	}
}

func TestRefreshExtendsSevenDaysAndStopsAtThirty(t *testing.T) {
	s, _, _ := accountTest(t)
	poolConfig := s.pool.Config()
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "America/New_York"
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s.pool = pool
	input := verifiedAccount(t, s)
	start := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	now := start
	s.now = func() time.Time { return now }
	pair := loggedIn(t, s, input)
	principal, err := s.Authenticate(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if !pair.RefreshTokenExpiresAt.Equal(start.Add(7 * 24 * time.Hour)) {
		t.Fatal("initial session lifetime is not seven days")
	}
	for _, day := range []int{6, 12, 18, 24, 29} {
		now = start.Add(time.Duration(day) * 24 * time.Hour)
		if _, err := s.Authenticate(context.Background(), pair.AccessToken); !errors.Is(err, ErrUnauthenticated) {
			t.Fatal("expired access JWT was accepted")
		}
		pair, err = s.Refresh(context.Background(), pair.RefreshToken)
		if err != nil {
			t.Fatal(err)
		}
		expected := min(now.Add(7*24*time.Hour).Unix(), start.Add(30*24*time.Hour).Unix())
		if pair.RefreshTokenExpiresAt.Unix() != expected {
			t.Fatalf("day %d expiry = %v", day, pair.RefreshTokenExpiresAt)
		}
		current, err := s.Authenticate(context.Background(), pair.AccessToken)
		if err != nil || current.SessionID != principal.SessionID {
			t.Fatal("refresh changed session identity")
		}
		if pair.AccessTokenExpiresAt.After(pair.RefreshTokenExpiresAt) {
			t.Fatal("access JWT outlives its session")
		}
	}
	now = start.Add(30*24*time.Hour - time.Minute)
	pair, err = s.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if !pair.AccessTokenExpiresAt.Equal(start.Add(30 * 24 * time.Hour)) {
		t.Fatal("JWT was not capped at the remaining session lifetime")
	}
	now = start.Add(30 * 24 * time.Hour)
	if _, err := s.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("refresh exceeded the thirty-day maximum")
	}
}

func TestIdleSessionExpiresAfterSevenDays(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	now := time.Now().UTC().Truncate(time.Second)
	s.now = func() time.Time { return now }
	pair := loggedIn(t, s, input)
	now = now.Add(7 * 24 * time.Hour)
	if _, err := s.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("idle session renewed after expiry")
	}
}

func TestRefreshReplayRevokesTheSession(t *testing.T) {
	s, _, _ := accountTest(t)
	first := loggedIn(t, s, verifiedAccount(t, s))
	second, err := s.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(context.Background(), first.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("refresh token was reusable")
	}
	if _, err := s.Authenticate(context.Background(), second.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("replay invalidation was rolled back")
	}
	if _, err := s.Refresh(context.Background(), second.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("replacement refresh survived replay revocation")
	}
}

func TestConcurrentRefreshRejectsReplayAndRevokesSession(t *testing.T) {
	s, _, _ := accountTest(t)
	pair := loggedIn(t, s, verifiedAccount(t, s))
	results := make(chan TokenPair, 2)
	failures := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			rotated, err := s.Refresh(context.Background(), pair.RefreshToken)
			if err != nil {
				failures <- err
				return
			}
			results <- rotated
		})
	}
	group.Wait()
	if len(results) != 1 || len(failures) != 1 {
		t.Fatal("refresh token was not consumed exactly once")
	}
	if err := <-failures; !errors.Is(err, ErrUnauthenticated) {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(context.Background(), (<-results).AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("concurrent replay did not revoke the session")
	}
}

func TestFailedReplacementLoginPreservesPreviousSession(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	pair := loggedIn(t, s, input)
	execAccountSQL(t, s.pool, `CREATE FUNCTION sitetrack.reject_test_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refresh insert rejected by test'; END $$;
CREATE TRIGGER reject_test_refresh BEFORE INSERT ON sitetrack.refresh_tokens FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_test_refresh();`)
	defer func() {
		execAccountSQL(t, s.pool, "DROP TRIGGER reject_test_refresh ON sitetrack.refresh_tokens; DROP FUNCTION sitetrack.reject_test_refresh()")
	}()
	if _, err := s.Login(context.Background(), LoginInput{Email: input.Email, Password: input.Password}); err == nil {
		t.Fatal("replacement login ignored persistence failure")
	}
	if _, err := s.Authenticate(context.Background(), pair.AccessToken); err != nil {
		t.Fatal("failed replacement login destroyed the previous session")
	}
}

func TestFailedLoginDoesNotRevealAccountStateOrReplaceSession(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	pair := loggedIn(t, s, input)
	unverified, _ := newAccount(t, s)
	for _, attempt := range []LoginInput{
		{Email: input.Email, Password: "wrong-password"},
		{Email: "missing@example.test", Password: "sitetrack-login-dummy-not-an-account"},
		{Email: unverified.Email, Password: unverified.Password},
	} {
		if _, err := s.Login(context.Background(), attempt); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal("login exposes different account states")
		}
	}
	if _, err := s.Authenticate(context.Background(), pair.AccessToken); err != nil {
		t.Fatal("failed login revoked the active session")
	}
}

func TestProfileUsesCurrentRoleAndSessionIdentity(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	pair := loggedIn(t, s, input)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := Register(router.Group(""), s, openapi.New(), false, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	read := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/auth/me?id="+uuid.NewString(), nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	if response := read(""); response.Code != http.StatusUnauthorized {
		t.Fatal("profile is accessible without authentication")
	}
	execAccountSQL(t, s.pool, "UPDATE sitetrack.users SET role='company_store_manager' WHERE id=$1", pair.User.ID)
	response := read(pair.AccessToken)
	var user Profile
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || user.ID != pair.User.ID || user.Role != "company_store_manager" {
		t.Fatal("profile uses stale claims or a caller-supplied account ID")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("profile can be cached")
	}
	if _, err := s.Authenticate(context.Background(), pair.AccessToken+"tampered"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("tampered JWT accepted")
	}
}
