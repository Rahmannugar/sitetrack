package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/Rahmannugar/authlier/emailverification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authdb "sitetrack/internal/authentication/repositories/generated"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
)

type VerificationStore struct {
	queries *authdb.Queries
	events  *outboxdb.Queries
}

func NewVerificationStore(tx pgx.Tx) *VerificationStore {
	return &VerificationStore{queries: authdb.New(tx), events: outboxdb.New(tx)}
}

func (s *VerificationStore) FindUserByEmail(ctx context.Context, email string) (emailverification.User, error) {
	user, err := s.queries.LockUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return emailverification.User{}, emailverification.ErrNotFound
	}
	if err != nil {
		return emailverification.User{}, err
	}
	return emailverification.User{ID: user.ID.String(), Email: user.Email, Verified: user.EmailVerified}, nil
}

func (s *VerificationStore) Check(ctx context.Context, attempt emailverification.Attempt) error {
	user, err := s.queries.LockUserByEmail(ctx, attempt.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		if attempt.Operation == emailverification.OperationVerify {
			return emailverification.ErrInvalidToken
		}
		return nil
	}
	if err != nil {
		return err
	}
	if attempt.Operation == emailverification.OperationVerify {
		_, err := s.queries.CountVerificationAttempt(ctx, authdb.CountVerificationAttemptParams{
			UserID: user.ID, ExpiresAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return emailverification.ErrInvalidToken
		}
		return err
	}
	record, err := s.queries.GetVerification(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if time.Since(record.CreatedAt.Time) < time.Minute {
		return emailverification.ErrAttemptBlocked
	}
	return nil
}

func (s *VerificationStore) Issue(ctx context.Context, record emailverification.Record) error {
	id, err := uuid.Parse(record.UserID)
	if err != nil {
		return emailverification.ErrInvalidRecord
	}
	if err := s.queries.IssueVerification(ctx, authdb.IssueVerificationParams{
		UserID: id, TokenHash: record.TokenHash[:],
		CreatedAt: pgtype.Timestamptz{Time: record.CreatedAt, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: record.ExpiresAt, Valid: true},
	}); err != nil {
		return err
	}
	return s.events.CancelVerificationEmails(ctx, id.String())
}

func (s *VerificationStore) Verify(ctx context.Context, hash emailverification.TokenHash, at time.Time) (emailverification.User, error) {
	user, err := s.queries.VerifyEmail(ctx, authdb.VerifyEmailParams{
		TokenHash: hash[:], ExpiresAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return emailverification.User{}, emailverification.ErrInactiveToken
	}
	if err != nil {
		return emailverification.User{}, err
	}
	if err := s.queries.MarkVerificationUsed(ctx, user.ID); err != nil {
		return emailverification.User{}, err
	}
	if err := s.events.CancelVerificationEmails(ctx, user.ID.String()); err != nil {
		return emailverification.User{}, err
	}
	return emailverification.User{ID: user.ID.String(), Email: user.Email, Verified: true}, nil
}
