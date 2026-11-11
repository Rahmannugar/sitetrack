package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"sitetrack/internal/infra/openapi"
)

func resetCode(t *testing.T, s *Service, id uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	var job uuid.UUID
	var ciphertext []byte
	if err := s.pool.QueryRow(context.Background(), `SELECT id,payload FROM sitetrack.outbox_events WHERE aggregate_id=$1 AND event_type='email.password_reset' AND status='pending'`, id.String()).Scan(&job, &ciphertext); err != nil {
		t.Fatal(err)
	}
	payload, err := s.cipher.Open(ciphertext, job[:])
	if err != nil {
		t.Fatal(err)
	}
	var message passwordResetEmailMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	if message.Code == "" || strings.Contains(string(ciphertext), message.Code) {
		t.Fatal("reset OTP not encrypted")
	}
	return job, message.Code
}

func TestPasswordResetDeliveryAndLogout(t *testing.T) {
	s, worker, sender := accountTest(t)
	input := verifiedAccount(t, s)
	session := loggedIn(t, s, input)
	ctx := context.Background()
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	job, code := resetCode(t, s, session.User.ID)
	var matched bool
	if err := s.pool.QueryRow(ctx, `SELECT token_hash=$2 FROM sitetrack.password_resets WHERE user_id=$1`, session.User.ID, s.resetProof(input.Email, code)).Scan(&matched); err != nil || !matched {
		t.Fatal("OTP proof mismatch", err)
	}
	if _, err := worker.SendBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.keys) != 1 || sender.keys[0] != "email.password_reset/"+job.String() {
		t.Fatal("reset delivery key", sender.keys)
	}
	if sender.messages[0].Subject != "Reset your SiteTrack password" || sender.messages[0].To != input.Email || !strings.Contains(sender.messages[0].Text, code) {
		t.Fatal("incorrect reset email")
	}
	replacement := "replacement-password-123"
	reset := ResetPasswordInput{Email: input.Email, Code: code, Password: replacement}
	if err := s.ResetPassword(ctx, reset); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, session.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("JWT survived reset", err)
	}
	if _, err := s.Refresh(ctx, session.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("refresh survived reset", err)
	}
	if err := s.ResetPassword(ctx, reset); !errors.Is(err, ErrInvalidReset) {
		t.Fatal("code replay accepted", err)
	}
	if _, err := s.Login(ctx, LoginInput{Email: input.Email, Password: input.Password}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password accepted", err)
	}
	input.Password = replacement
	loggedIn(t, s, input)
}

func TestPasswordResetAttemptsResendExpiryAndPurpose(t *testing.T) {
	s, _, _ := accountTest(t)
	input, id := newAccount(t, s)
	_, verificationCode := queuedCode(t, s, id)
	ctx := context.Background()
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	first, code := resetCode(t, s, id)
	for code == verificationCode {
		execAccountSQL(t, s.pool, `UPDATE sitetrack.password_resets SET created_at=created_at-interval '61 seconds' WHERE user_id=$1`, id)
		if err := s.ForgotPassword(ctx, input.Email); err != nil {
			t.Fatal(err)
		}
		first, code = resetCode(t, s, id)
	}
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	same, _ := resetCode(t, s, id)
	if first != same {
		t.Fatal("cooldown replaced reset OTP")
	}
	reset := ResetPasswordInput{Email: input.Email, Code: verificationCode, Password: "replacement-password-123"}
	if err := s.ResetPassword(ctx, reset); !errors.Is(err, ErrInvalidReset) {
		t.Fatal("verification OTP reset password", err)
	}
	if err := s.Verify(ctx, VerificationInput{Email: input.Email, Code: code}); !errors.Is(err, ErrInvalidVerification) {
		t.Fatal("reset OTP verified email", err)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	reset.Code = wrong
	for range 4 {
		if err := s.ResetPassword(ctx, reset); !errors.Is(err, ErrInvalidReset) {
			t.Fatal(err)
		}
	}
	reset.Code = code
	if err := s.ResetPassword(ctx, reset); !errors.Is(err, ErrInvalidReset) {
		t.Fatal("exhausted OTP accepted", err)
	}
	var attempts int
	if err := s.pool.QueryRow(ctx, `SELECT attempts FROM sitetrack.password_resets WHERE user_id=$1`, id).Scan(&attempts); err != nil || attempts != 5 {
		t.Fatal("failed attempts lost", attempts, err)
	}
	execAccountSQL(t, s.pool, `UPDATE sitetrack.password_resets SET created_at=created_at-interval '61 seconds' WHERE user_id=$1`, id)
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	second, replacement := resetCode(t, s, id)
	if second == first || replacement == code {
		t.Fatal("resend did not replace OTP")
	}
	var cancelled bool
	if err := s.pool.QueryRow(ctx, `SELECT status='cancelled' AND payload IS NULL FROM sitetrack.outbox_events WHERE id=$1`, first).Scan(&cancelled); err != nil || !cancelled {
		t.Fatal("obsolete email retained", err)
	}
	if err := s.ResetPassword(ctx, reset); !errors.Is(err, ErrInvalidReset) {
		t.Fatal("superseded OTP accepted", err)
	}
	reset.Code = replacement
	if err := s.ResetPassword(ctx, reset); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, LoginInput{Email: input.Email, Password: reset.Password}); !errors.Is(err, ErrEmailUnverified) {
		t.Fatal("reset verified account implicitly", err)
	}
	execAccountSQL(t, s.pool, `UPDATE sitetrack.password_resets SET created_at=created_at-interval '61 seconds' WHERE user_id=$1`, id)
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	_, reset.Code = resetCode(t, s, id)
	execAccountSQL(t, s.pool, `UPDATE sitetrack.password_resets SET created_at=now()-interval '11 minutes',expires_at=now()-interval '1 minute' WHERE user_id=$1`, id)
	if err := s.ResetPassword(ctx, reset); !errors.Is(err, ErrInvalidReset) {
		t.Fatal("expired OTP accepted", err)
	}
}

func TestPasswordResetRollbackAndConcurrentUse(t *testing.T) {
	s, _, _ := accountTest(t)
	input := verifiedAccount(t, s)
	session := loggedIn(t, s, input)
	ctx := context.Background()
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	_, code := resetCode(t, s, session.User.ID)
	reset := ResetPasswordInput{Email: input.Email, Code: code, Password: "replacement-password-123"}
	var previousHash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM sitetrack.users WHERE id=$1`, session.User.ID).Scan(&previousHash); err != nil {
		t.Fatal(err)
	}
	execAccountSQL(t, s.pool, `CREATE FUNCTION sitetrack.reject_test_logout() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'logout rejected by test'; END $$;
CREATE TRIGGER reject_test_logout BEFORE DELETE ON sitetrack.auth_sessions FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_test_logout();`)
	removeTrigger := func() {
		execAccountSQL(t, s.pool, `DROP TRIGGER IF EXISTS reject_test_logout ON sitetrack.auth_sessions; DROP FUNCTION IF EXISTS sitetrack.reject_test_logout()`)
	}
	defer removeTrigger()
	if err := s.ResetPassword(ctx, reset); err == nil {
		t.Fatal("reset ignored session failure")
	}
	if _, err := s.Authenticate(ctx, session.AccessToken); err != nil {
		t.Fatal("rollback lost session", err)
	}
	var currentHash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM sitetrack.users WHERE id=$1`, session.User.ID).Scan(&currentHash); err != nil || previousHash != currentHash {
		t.Fatal("rollback changed password", err)
	}
	var used bool
	if err := s.pool.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM sitetrack.password_resets WHERE user_id=$1`, session.User.ID).Scan(&used); err != nil || used {
		t.Fatal("rollback consumed OTP", err)
	}
	removeTrigger()
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Go(func() { results <- s.ResetPassword(ctx, reset) })
	}
	group.Wait()
	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvalidReset) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("OTP consumption not atomic", successes)
	}
	if _, err := s.Authenticate(ctx, session.AccessToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("session survived", err)
	}
}

func TestPasswordResetCompetingLoginAndRefresh(t *testing.T) {
	for _, action := range []string{"login", "refresh"} {
		t.Run(action, func(t *testing.T) {
			s, _, _ := accountTest(t)
			input := verifiedAccount(t, s)
			pair := loggedIn(t, s, input)
			ctx := context.Background()
			if err := s.ForgotPassword(ctx, input.Email); err != nil {
				t.Fatal(err)
			}
			_, code := resetCode(t, s, pair.User.ID)
			var next TokenPair
			var sessionErr, resetErr error
			start := make(chan struct{})
			var group sync.WaitGroup
			group.Go(func() {
				<-start
				resetErr = s.ResetPassword(ctx, ResetPasswordInput{Email: input.Email, Code: code, Password: "replacement-password-123"})
			})
			group.Go(func() {
				<-start
				if action == "login" {
					next, sessionErr = s.Login(ctx, LoginInput{Email: input.Email, Password: input.Password})
				} else {
					next, sessionErr = s.Refresh(ctx, pair.RefreshToken)
				}
			})
			close(start)
			group.Wait()
			if resetErr != nil {
				t.Fatal(resetErr)
			}
			if sessionErr != nil && !errors.Is(sessionErr, ErrUnauthenticated) && !errors.Is(sessionErr, ErrInvalidCredentials) {
				t.Fatal(sessionErr)
			}
			for _, token := range []string{pair.AccessToken, next.AccessToken} {
				if token != "" {
					if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
						t.Fatal("old-password session survived reset", err)
					}
				}
			}
			if next.RefreshToken != "" {
				if _, err := s.Refresh(ctx, next.RefreshToken); !errors.Is(err, ErrUnauthenticated) {
					t.Fatal("competing refresh survived reset", err)
				}
			}
		})
	}
}

func TestPasswordResetRequestRollbackUnknownAndCleanup(t *testing.T) {
	s, worker, _ := accountTest(t)
	input, id := newAccount(t, s)
	ctx := context.Background()
	execAccountSQL(t, s.pool, `CREATE FUNCTION sitetrack.reject_test_reset_email() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='email.password_reset' THEN RAISE EXCEPTION 'reset email rejected by test'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_test_reset_email BEFORE INSERT ON sitetrack.outbox_events FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_test_reset_email();`)
	drop := func() {
		execAccountSQL(t, s.pool, `DROP TRIGGER IF EXISTS reject_test_reset_email ON sitetrack.outbox_events; DROP FUNCTION IF EXISTS sitetrack.reject_test_reset_email()`)
	}
	defer drop()
	if err := s.ForgotPassword(ctx, input.Email); err == nil {
		t.Fatal("request ignored outbox failure")
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sitetrack.password_resets WHERE user_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("OTP committed without email", err)
	}
	drop()
	if err := s.ForgotPassword(ctx, "missing-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgotPassword(ctx, input.Email); err != nil {
		t.Fatal(err)
	}
	_, used := newAccount(t, s)
	_, active := newAccount(t, s)
	execAccountSQL(t, s.pool, `INSERT INTO sitetrack.password_resets(user_id,token_hash,created_at,expires_at,used_at) VALUES ($1,$3,now(),now()+interval '10 minutes',now()),($2,$3,now(),now()+interval '10 minutes',NULL)`, used, active, make([]byte, 32))
	execAccountSQL(t, s.pool, `UPDATE sitetrack.password_resets SET created_at=now()-interval '11 minutes',expires_at=now()-interval '1 minute' WHERE user_id=$1`, id)
	execAccountSQL(t, s.pool, `DELETE FROM sitetrack.worker_maintenance WHERE name='otp_cleanup'`)
	if err := worker.verification.CleanupOTPs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sitetrack.password_resets WHERE user_id=ANY($1::uuid[])`, []uuid.UUID{id, used}).Scan(&count); err != nil || count != 0 {
		t.Fatal("obsolete reset OTPs survived", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sitetrack.password_resets WHERE user_id=$1`, active).Scan(&count); err != nil || count != 1 {
		t.Fatal("active reset OTP deleted", err)
	}
}

func TestAccountHTTPStatusesAndRecovery(t *testing.T) {
	s, _, _ := accountTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registry := openapi.New()
	if err := Register(router.Group(""), s, registry, false, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	post := func(path string, input any, status int) {
		t.Helper()
		body, _ := json.Marshal(input)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s: status %d, body %s", path, response.Code, response.Body.String())
		}
	}
	input := SignupInput{Name: "Engineer", Email: uuid.NewString() + "@example.test", Password: "A-secure-password-123", Role: "user"}
	post("/auth/signup", input, http.StatusCreated)
	var id uuid.UUID
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM sitetrack.users WHERE email=$1`, input.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		execAccountSQL(t, s.pool, `DELETE FROM sitetrack.outbox_events WHERE aggregate_id=$1`, id.String())
		execAccountSQL(t, s.pool, `DELETE FROM sitetrack.users WHERE id=$1`, id)
	})
	post("/auth/signup", input, http.StatusConflict)
	post("/auth/login", LoginInput{Email: input.Email, Password: "wrong-password"}, http.StatusUnauthorized)
	post("/auth/login", LoginInput{Email: input.Email, Password: input.Password}, http.StatusForbidden)
	_, code := queuedCode(t, s, id)
	post("/auth/verify-email", VerificationInput{Email: input.Email, Code: code}, http.StatusOK)
	post("/auth/signup", input, http.StatusConflict)
	post("/auth/forgot-password", ResendInput{Email: input.Email}, http.StatusAccepted)
	_, code = resetCode(t, s, id)
	post("/auth/reset-password", ResetPasswordInput{Email: input.Email, Code: "123", Password: "short"}, http.StatusBadRequest)
	post("/auth/reset-password", ResetPasswordInput{Email: input.Email, Code: code, Password: "replacement-password-123"}, http.StatusOK)
	post("/auth/reset-password", ResetPasswordInput{Email: input.Email, Code: code, Password: "replacement-password-123"}, http.StatusBadRequest)
}
