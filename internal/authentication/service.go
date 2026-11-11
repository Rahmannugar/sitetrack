package authentication

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Rahmannugar/authlier/accesstoken"
	"github.com/Rahmannugar/authlier/emailaddress"
	"github.com/Rahmannugar/authlier/emailverification"
	"github.com/Rahmannugar/authlier/password"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sitetrack/internal/authentication/repositories"
	authdb "sitetrack/internal/authentication/repositories/generated"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/outbox"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
)

type Accounts interface {
	Signup(context.Context, SignupInput) error
	Verify(context.Context, VerificationInput) error
	Resend(context.Context, string) error
	Login(context.Context, LoginInput) (TokenPair, error)
	Refresh(context.Context, string) (TokenPair, error)
	Authenticate(context.Context, string) (Principal, error)
	Logout(context.Context, Principal) error
	ForgotPassword(context.Context, string) error
	ResetPassword(context.Context, ResetPasswordInput) error
}

type Service struct {
	pool           *pgxpool.Pool
	settings       config.Authentication
	cipher         *outbox.PayloadCipher
	hashing        chan struct{}
	issuer         *accesstoken.Issuer
	verifierConfig accesstoken.VerifierConfig
	dummyHash      string
	now            func() time.Time
}

func NewService(pool *pgxpool.Pool, settings config.Authentication) (*Service, error) {
	if len(settings.OTPSecret) != 32 {
		return nil, errors.New("SITETRACK_OTP_SECRET must contain 32 bytes encoded as hex")
	}
	cipher, err := outbox.NewPayloadCipher(settings.OutboxKey)
	if err != nil {
		return nil, err
	}
	service := &Service{pool: pool, settings: settings, cipher: cipher, hashing: make(chan struct{}, 2)}
	if err := service.initSessions(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) Signup(ctx context.Context, input SignupInput) error {
	input.Name = strings.TrimSpace(input.Name)
	email, err := emailaddress.Normalize(input.Email)
	if err != nil || utf8.RuneCountInString(input.Name) < 2 || utf8.RuneCountInString(input.Name) > 100 ||
		utf8.RuneCountInString(input.Password) < 12 || utf8.RuneCountInString(input.Password) > 128 {
		return ErrInvalidInput
	}
	switch input.Role {
	case "user", "company_store_manager", "superadmin":
	default:
		return ErrInvalidInput
	}
	select {
	case s.hashing <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	hash, err := password.Hash(input.Password)
	<-s.hashing
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	_, err = authdb.New(tx).CreateUser(ctx, authdb.CreateUserParams{
		ID: id, Name: input.Name, Email: email, PasswordHash: hash, Role: input.Role,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEmailRegistered
	}
	if err != nil {
		return err
	}
	manager, err := s.verificationManager(tx)
	if err != nil {
		return err
	}
	if err := manager.Request(ctx, emailverification.RequestInput{Email: email}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Resend(ctx context.Context, email string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	manager, err := s.verificationManager(tx)
	if err != nil {
		return err
	}
	err = manager.Request(ctx, emailverification.RequestInput{Email: email})
	if err != nil && !errors.Is(err, emailverification.ErrAttemptBlocked) {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Verify(ctx context.Context, input VerificationInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	manager, err := s.verificationManager(tx)
	if err != nil {
		return err
	}
	_, err = manager.Verify(ctx, emailverification.VerifyInput{Email: input.Email, Code: input.Code})
	invalid := errors.Is(err, emailverification.ErrInvalidToken) || errors.Is(err, emailverification.ErrAttemptBlocked)
	if err != nil && !invalid {
		return err
	}
	// We commit failed attempts so retrying an invalid code cannot reset its limit.
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return commitErr
	}
	if invalid {
		return ErrInvalidVerification
	}
	return nil
}

func (s *Service) verificationManager(tx pgx.Tx) (*emailverification.Manager, error) {
	store := repositories.NewVerificationStore(tx)
	return emailverification.NewManager(store, emailverification.Config{
		Delivery: emailverification.DeliveryMethodOTP, OTPSecret: s.settings.OTPSecret,
		Lifetime: 10 * time.Minute, Sender: &verificationSender{events: outboxdb.New(tx), cipher: s.cipher, from: s.settings.EmailFrom}, AttemptGuard: store,
	})
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
