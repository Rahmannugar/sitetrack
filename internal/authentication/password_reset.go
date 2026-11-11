package authentication

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"time"
	"unicode/utf8"

	"github.com/Rahmannugar/authlier/emailaddress"
	"github.com/Rahmannugar/authlier/password"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authdb "sitetrack/internal/authentication/repositories/generated"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
)

func (s *Service) resetProof(email, code string) []byte {
	proof := hmac.New(sha256.New, s.settings.OTPSecret)
	_, _ = proof.Write([]byte("password-reset\x00" + email + "\x00" + code))
	return proof.Sum(nil)
}

func (s *Service) ForgotPassword(ctx context.Context, rawEmail string) error {
	email, err := emailaddress.Normalize(rawEmail)
	if err != nil {
		return ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	accounts := authdb.New(tx)
	user, err := accounts.LockUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	previous, err := accounts.GetPasswordReset(ctx, user.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	now := s.now().UTC()
	if err == nil && now.Sub(previous.CreatedAt.Time) < time.Minute {
		return nil
	}
	var code string
	var proof []byte
	for {
		value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			return err
		}
		code = fmt.Sprintf("%06d", value.Int64())
		proof = s.resetProof(email, code)
		if !hmac.Equal(proof, previous.TokenHash) {
			break
		}
	}
	expires := now.Add(10 * time.Minute)
	if err := accounts.IssuePasswordReset(ctx, authdb.IssuePasswordResetParams{UserID: user.ID, TokenHash: proof,
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}); err != nil {
		return err
	}
	events := outboxdb.New(tx)
	if err := events.CancelPasswordResetEmails(ctx, user.ID.String()); err != nil {
		return err
	}
	if err := s.queuePasswordReset(ctx, events, user.ID, email, code, expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPassword(ctx context.Context, input ResetPasswordInput) error {
	email, err := emailaddress.Normalize(input.Email)
	if err != nil || utf8.RuneCountInString(input.Password) < 12 || utf8.RuneCountInString(input.Password) > 128 {
		return ErrInvalidInput
	}
	if len(input.Code) != 6 {
		return ErrInvalidReset
	}
	for _, digit := range input.Code {
		if digit < '0' || digit > '9' {
			return ErrInvalidReset
		}
	}
	select {
	case s.hashing <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.hashing }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	accounts := authdb.New(tx)
	// We lock the account before changing its password or session, as login and refresh do.
	user, err := accounts.LockUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidReset
	}
	if err != nil {
		return err
	}
	record, err := accounts.GetPasswordReset(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidReset
	}
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if record.UsedAt.Valid || !record.ExpiresAt.Time.After(now) || record.Attempts >= 5 {
		return ErrInvalidReset
	}
	if err := accounts.CountPasswordResetAttempt(ctx, user.ID); err != nil {
		return err
	}
	if !hmac.Equal(record.TokenHash, s.resetProof(email, input.Code)) {
		// We retain failed attempts even when the reset is rejected.
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrInvalidReset
	}
	hash, err := password.Hash(input.Password)
	if err != nil {
		return err
	}
	if err := accounts.ChangePassword(ctx, authdb.ChangePasswordParams{ID: user.ID, PasswordHash: hash}); err != nil {
		return err
	}
	if err := accounts.UsePasswordReset(ctx, authdb.UsePasswordResetParams{UserID: user.ID, UsedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return err
	}
	if err := accounts.DeleteUserSessions(ctx, user.ID); err != nil {
		return err
	}
	if err := outboxdb.New(tx).CancelPasswordResetEmails(ctx, user.ID.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
