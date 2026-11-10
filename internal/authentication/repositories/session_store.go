package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/Rahmannugar/authlier/accesstoken"
	"github.com/Rahmannugar/authlier/refreshtoken"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authdb "sitetrack/internal/authentication/repositories/generated"
)

type RefreshStore struct {
	queries *authdb.Queries
}

func NewRefreshStore(tx pgx.Tx) *RefreshStore { return &RefreshStore{queries: authdb.New(tx)} }

func (s *RefreshStore) Create(ctx context.Context, record refreshtoken.Record) error {
	id, err := uuid.Parse(record.SessionID)
	if err != nil {
		return refreshtoken.ErrInvalidRecord
	}
	session, err := s.queries.LockSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return refreshtoken.ErrInactive
	}
	if err != nil {
		return err
	}
	if record.ExpiresAt.After(session.ExpiresAt.Time) {
		return refreshtoken.ErrInvalidRecord
	}
	count, err := s.queries.InsertRefreshToken(ctx, authdb.InsertRefreshTokenParams{
		TokenHash: record.TokenHash[:], SessionID: id,
		CreatedAt: timestamp(record.CreatedAt), ExpiresAt: timestamp(record.ExpiresAt),
	})
	if err == nil && count == 0 {
		return refreshtoken.ErrConflict
	}
	return err
}

func (s *RefreshStore) FindByTokenHash(ctx context.Context, hash refreshtoken.TokenHash) (refreshtoken.Record, error) {
	row, err := s.queries.FindRefreshToken(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return refreshtoken.Record{}, refreshtoken.ErrNotFound
	}
	if err != nil {
		return refreshtoken.Record{}, err
	}
	if len(row.TokenHash) != len(hash) {
		return refreshtoken.Record{}, refreshtoken.ErrInvalidRecord
	}
	var stored refreshtoken.TokenHash
	copy(stored[:], row.TokenHash)
	record := refreshtoken.Record{SessionID: row.SessionID.String(), TokenHash: stored, CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	if row.RotatedAt.Valid {
		record.RotatedAt = &row.RotatedAt.Time
	}
	return record, nil
}

func (s *RefreshStore) Rotate(ctx context.Context, current refreshtoken.TokenHash, replacement refreshtoken.Record, at time.Time) error {
	// We lock users before sessions and tokens, matching login's replacement order.
	if _, err := s.queries.LockRefreshUser(ctx, current[:]); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return refreshtoken.ErrInactive
		}
		return err
	}
	id, err := uuid.Parse(replacement.SessionID)
	if err != nil {
		return refreshtoken.ErrInvalidRecord
	}
	session, err := s.queries.LockSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return refreshtoken.ErrInactive
	}
	if err != nil {
		return err
	}
	record, err := s.queries.LockRefreshToken(ctx, current[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return refreshtoken.ErrInactive
	}
	if err != nil {
		return err
	}
	if record.SessionID != id {
		return refreshtoken.ErrInvalidRecord
	}
	if record.RotatedAt.Valid {
		if err := s.queries.DeleteSession(ctx, id); err != nil {
			return err
		}
		return refreshtoken.ErrReuseDetected
	}
	if !at.Before(session.ExpiresAt.Time) || !at.Before(record.ExpiresAt.Time) {
		return refreshtoken.ErrInactive
	}
	if replacement.ExpiresAt.After(session.ExpiresAt.Time) || !replacement.ExpiresAt.After(replacement.CreatedAt) {
		return refreshtoken.ErrInvalidRecord
	}
	count, err := s.queries.InsertRefreshToken(ctx, authdb.InsertRefreshTokenParams{
		TokenHash: replacement.TokenHash[:], SessionID: id,
		CreatedAt: timestamp(replacement.CreatedAt), ExpiresAt: timestamp(replacement.ExpiresAt),
	})
	if err != nil {
		return err
	}
	if count == 0 {
		return refreshtoken.ErrConflict
	}
	return s.queries.MarkRefreshRotated(ctx, authdb.MarkRefreshRotatedParams{TokenHash: current[:], RotatedAt: timestamp(at)})
}

func (s *RefreshStore) RevokeSession(ctx context.Context, id string, _ time.Time) error {
	sessionID, err := uuid.Parse(id)
	if err != nil {
		return refreshtoken.ErrInvalidRecord
	}
	return s.queries.DeleteSession(ctx, sessionID)
}

type SessionResolver struct {
	queries *authdb.Queries
	now     func() time.Time
	Current authdb.GetCurrentSessionRow
	Failure error
}

func NewSessionResolver(db authdb.DBTX, now func() time.Time) *SessionResolver {
	return &SessionResolver{queries: authdb.New(db), now: now}
}

func (s *SessionResolver) ResolveSession(ctx context.Context, id string) (accesstoken.Session, error) {
	sessionID, err := uuid.Parse(id)
	if err != nil {
		return accesstoken.Session{}, accesstoken.ErrInactiveSession
	}
	row, err := s.queries.GetCurrentSession(ctx, authdb.GetCurrentSessionParams{ID: sessionID, ExpiresAt: timestamp(s.now())})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.Failure = err
		}
		return accesstoken.Session{}, accesstoken.ErrInactiveSession
	}
	s.Current = row
	return accesstoken.Session{ID: row.ID.String(), SubjectID: row.UserID.String(), CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time}, nil
}

func timestamp(at time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: at, Valid: true} }
