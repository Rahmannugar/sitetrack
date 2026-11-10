package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Rahmannugar/authlier/password"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sitetrack/internal/config"
	"sitetrack/internal/infra/database"
	"sitetrack/internal/infra/email"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
	"sitetrack/internal/infra/telemetry"
)

type captureEmail struct {
	keys []string
	err  error
}

func (s *captureEmail) Send(_ context.Context, _ email.Message, key string) (string, error) {
	s.keys = append(s.keys, key)
	return "provider-message", s.err
}

func accountTest(t *testing.T) (*Service, *VerificationWorker, *captureEmail) {
	t.Helper()
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, url, 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, config.Authentication{
		OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"),
		EmailFrom: "SiteTrack <sender@example.test>",
	})
	if err != nil {
		t.Fatal(err)
	}
	monitoring, err := telemetry.New(ctx, config.Telemetry{}, "test-worker", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	sender := &captureEmail{}
	worker, err := NewVerificationWorker(pool, service.cipher, sender, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	return service, worker, sender
}

func newAccount(t *testing.T, service *Service) (SignupInput, uuid.UUID) {
	t.Helper()
	input := SignupInput{Name: "Site Engineer", Email: uuid.NewString() + "@example.test", Password: "A-secure-password-123", Role: "user"}
	if err := service.Signup(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := service.pool.QueryRow(context.Background(), "SELECT id FROM sitetrack.users WHERE email=$1", input.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = service.pool.Exec(context.Background(), "DELETE FROM sitetrack.outbox_events WHERE aggregate_id=$1", id)
		_, _ = service.pool.Exec(context.Background(), "DELETE FROM sitetrack.users WHERE id=$1", id)
	})
	return input, id
}

func queuedCode(t *testing.T, service *Service, id uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	var job uuid.UUID
	var encrypted []byte
	if err := service.pool.QueryRow(context.Background(), "SELECT id,payload FROM sitetrack.outbox_events WHERE aggregate_id=$1 AND status='pending'", id).Scan(&job, &encrypted); err != nil {
		t.Fatal(err)
	}
	payload, err := service.cipher.Open(encrypted, job[:])
	if err != nil {
		t.Fatal(err)
	}
	var message verificationEmailMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	return job, message.Code
}

func execAccountSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationDeliveryAndSingleUseOTP(t *testing.T) {
	s, worker, sender := accountTest(t)
	input, id := newAccount(t, s)
	if id.Version() != 7 {
		t.Fatal("account identity is not UUID v7")
	}
	var hash string
	if err := s.pool.QueryRow(context.Background(), "SELECT password_hash FROM sitetrack.users WHERE id=$1", id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	verified, err := password.Verify(input.Password, hash)
	if err != nil || !verified.Matches {
		t.Fatal("password was not hashed through Authlier")
	}
	job, code := queuedCode(t, s, id)
	if err := worker.SendNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.keys) != 1 || sender.keys[0] != "email.verification/"+job.String() {
		t.Fatal("delivery did not use a stable job idempotency key")
	}
	if err := s.Verify(context.Background(), VerificationInput{Email: input.Email, Code: code}); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(context.Background(), VerificationInput{Email: input.Email, Code: code}); !errors.Is(err, ErrInvalidVerification) {
		t.Fatal("consumed OTP was accepted again")
	}
	var used, userVerified bool
	if err := s.pool.QueryRow(context.Background(), "SELECT u.email_verified,v.used_at IS NOT NULL FROM sitetrack.users u JOIN sitetrack.email_verifications v ON v.user_id=u.id WHERE u.id=$1", id).Scan(&userVerified, &used); err != nil {
		t.Fatal(err)
	}
	if !used || !userVerified {
		t.Fatal("OTP consumption and email verification did not commit together")
	}
}

func TestFailedAttemptsPersistAndResendReplacesCode(t *testing.T) {
	s, _, _ := accountTest(t)
	input, id := newAccount(t, s)
	_, code := queuedCode(t, s, id)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for range 5 {
		if err := s.Verify(context.Background(), VerificationInput{Email: input.Email, Code: wrong}); !errors.Is(err, ErrInvalidVerification) {
			t.Fatal("invalid attempt was not rejected")
		}
	}
	if err := s.Verify(context.Background(), VerificationInput{Email: input.Email, Code: code}); !errors.Is(err, ErrInvalidVerification) {
		t.Fatal("exhausted challenge accepted its correct OTP")
	}
	var attempts int
	if err := s.pool.QueryRow(context.Background(), "SELECT attempts FROM sitetrack.email_verifications WHERE user_id=$1", id).Scan(&attempts); err != nil || attempts != 5 {
		t.Fatal("failed verification attempts were rolled back")
	}
	execAccountSQL(t, s.pool, "UPDATE sitetrack.email_verifications SET created_at=created_at-interval '61 seconds' WHERE user_id=$1", id)
	if err := s.Resend(context.Background(), input.Email); err != nil {
		t.Fatal(err)
	}
	_, replacement := queuedCode(t, s, id)
	if err := s.Verify(context.Background(), VerificationInput{Email: input.Email, Code: replacement}); err != nil {
		t.Fatal(err)
	}
}

func TestEmailRetryKeepsIdempotencyKey(t *testing.T) {
	s, worker, sender := accountTest(t)
	_, id := newAccount(t, s)
	job, _ := queuedCode(t, s, id)
	sender.err = &email.DeliveryError{Code: "provider_transport"}
	if err := worker.SendNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	execAccountSQL(t, s.pool, "UPDATE sitetrack.outbox_events SET available_at=now() WHERE id=$1", job)
	sender.err = nil
	if err := worker.SendNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.keys) != 2 || sender.keys[0] != sender.keys[1] {
		t.Fatal("retry changed the provider idempotency key")
	}
	var status string
	var cleared bool
	if err := s.pool.QueryRow(context.Background(), "SELECT status,payload IS NULL FROM sitetrack.outbox_events WHERE id=$1", job).Scan(&status, &cleared); err != nil || status != "published" || !cleared {
		t.Fatal("successful retry did not publish and clear the sensitive payload")
	}
}

func TestDailyCleanupRemovesExpiredAndUsedCodes(t *testing.T) {
	s, worker, _ := accountTest(t)
	_, expired := newAccount(t, s)
	usedInput, used := newAccount(t, s)
	_, active := newAccount(t, s)
	_, code := queuedCode(t, s, used)
	if err := s.Verify(context.Background(), VerificationInput{Email: usedInput.Email, Code: code}); err != nil {
		t.Fatal(err)
	}
	execAccountSQL(t, s.pool, "UPDATE sitetrack.email_verifications SET created_at=now()-interval '11 minutes',expires_at=now()-interval '1 minute' WHERE user_id=$1", expired)
	execAccountSQL(t, s.pool, "DELETE FROM sitetrack.worker_maintenance WHERE name='otp_cleanup'")
	if err := worker.CleanupOTPs(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM sitetrack.email_verifications WHERE user_id=ANY($1::uuid[])", []uuid.UUID{expired, used}).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired/used codes survived cleanup")
	}
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM sitetrack.email_verifications WHERE user_id=$1", active).Scan(&count); err != nil || count != 1 {
		t.Fatal("active code was deleted")
	}
	execAccountSQL(t, s.pool, "UPDATE sitetrack.email_verifications SET used_at=now() WHERE user_id=$1", active)
	if err := worker.CleanupOTPs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM sitetrack.email_verifications WHERE user_id=$1", active).Scan(&count); err != nil || count != 1 {
		t.Fatal("daily cleanup ran twice on the same UTC day")
	}
}

func TestConcurrentRegistrationCreatesOneAccountAndEmail(t *testing.T) {
	s, _, _ := accountTest(t)
	input, id := newAccount(t, s)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if err := s.Signup(context.Background(), input); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	var count int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM sitetrack.outbox_events WHERE aggregate_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate registration queued another email")
	}
}

func TestRegistrationRollsBackWhenOutboxInsertFails(t *testing.T) {
	s, _, _ := accountTest(t)
	execAccountSQL(t, s.pool, `CREATE FUNCTION sitetrack.reject_test_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox insert rejected by test'; END $$;
CREATE TRIGGER reject_test_outbox BEFORE INSERT ON sitetrack.outbox_events FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_test_outbox();`)
	defer func() {
		execAccountSQL(t, s.pool, "DROP TRIGGER reject_test_outbox ON sitetrack.outbox_events; DROP FUNCTION sitetrack.reject_test_outbox()")
	}()
	input := SignupInput{Name: "Engineer", Email: uuid.NewString() + "@example.test", Password: "secure-password-123", Role: "user"}
	if err := s.Signup(context.Background(), input); err == nil {
		t.Fatal("forced outbox failure was ignored")
	}
	var count int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM sitetrack.users WHERE email=$1", input.Email).Scan(&count); err != nil || count != 0 {
		t.Fatal("account committed without its verification email")
	}
}

func TestOutboxLeaseFencesStaleWorker(t *testing.T) {
	s, _, _ := accountTest(t)
	_, id := newAccount(t, s)
	job, _ := queuedCode(t, s, id)
	queries := outboxdb.New(s.pool)
	first := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	second := uuid.NullUUID{UUID: uuid.New(), Valid: true}
	if _, err := queries.ClaimEmail(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.ClaimEmail(context.Background(), second); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("active job was claimed by another worker")
	}
	execAccountSQL(t, s.pool, "UPDATE sitetrack.outbox_events SET lease_until=now()-interval '1 second' WHERE id=$1", job)
	if _, err := queries.ClaimEmail(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	providerID := "provider-id"
	if count, err := queries.PublishEmail(context.Background(), outboxdb.PublishEmailParams{ID: job, LeaseToken: first, ProviderMessageID: &providerID}); err != nil || count != 0 {
		t.Fatal("stale worker changed the recovered job")
	}
	if count, err := queries.PublishEmail(context.Background(), outboxdb.PublishEmailParams{ID: job, LeaseToken: second, ProviderMessageID: &providerID}); err != nil || count != 1 {
		t.Fatal("current worker could not finish the job")
	}
}

type emailSenderFunc func(context.Context, email.Message, string) (string, error)

func (f emailSenderFunc) Send(ctx context.Context, message email.Message, key string) (string, error) {
	return f(ctx, message, key)
}

func TestWorkerShutdownDrainsActiveDelivery(t *testing.T) {
	s, worker, _ := accountTest(t)
	_, id := newAccount(t, s)
	job, _ := queuedCode(t, s, id)
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	worker.sender = emailSenderFunc(func(ctx context.Context, _ email.Message, _ string) (string, error) {
		started <- ctx
		select {
		case <-release:
			return "delivered-on-shutdown", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { worker.Run(ctx); close(finished) }()
	var delivery context.Context
	select {
	case delivery = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start delivery")
	}
	cancel()
	if delivery.Err() != nil {
		t.Error("shutdown canceled an active delivery before its drain timeout")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop after draining")
	}
	var status string
	if err := s.pool.QueryRow(context.Background(), "SELECT status FROM sitetrack.outbox_events WHERE id=$1", job).Scan(&status); err != nil || status != "published" {
		t.Fatal("drained delivery was not acknowledged")
	}
}
