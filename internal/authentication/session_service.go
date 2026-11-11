package authentication

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/Rahmannugar/authlier/accesstoken"
	"github.com/Rahmannugar/authlier/emailaddress"
	"github.com/Rahmannugar/authlier/password"
	"github.com/Rahmannugar/authlier/refreshtoken"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"sitetrack/internal/authentication/repositories"
	authdb "sitetrack/internal/authentication/repositories/generated"
	"sitetrack/internal/infra/ratelimit"
)

const (
	accessLifetime         = 15 * time.Minute
	sessionLifetime        = 7 * 24 * time.Hour
	maximumSessionLifetime = 30 * 24 * time.Hour
)

func (s *Service) initSessions() error {
	if len(s.settings.JWTSeed) != ed25519.SeedSize {
		return errors.New("SITETRACK_JWT_SIGNING_SEED must contain 32 bytes encoded as hex")
	}
	key := ed25519.NewKeyFromSeed(s.settings.JWTSeed)
	s.now = time.Now
	var err error
	s.issuer, err = accesstoken.NewIssuer(accesstoken.IssuerConfig{
		Issuer: "sitetrack", Audience: "sitetrack-api", Lifetime: accessLifetime,
		KeyID: "primary", PrivateKey: key, Now: func() time.Time { return s.now() },
	})
	if err != nil {
		return err
	}
	s.verifierConfig = accesstoken.VerifierConfig{
		Issuer: "sitetrack", Audience: "sitetrack-api",
		PublicKeys: map[string]ed25519.PublicKey{"primary": key.Public().(ed25519.PublicKey)},
		Now:        func() time.Time { return s.now() },
	}
	s.dummyHash, err = password.Hash("sitetrack-login-dummy-not-an-account")
	return err
}

func (s *Service) Login(ctx context.Context, input LoginInput) (TokenPair, error) {
	email, err := emailaddress.Normalize(input.Email)
	if err != nil {
		return TokenPair{}, ErrInvalidCredentials
	}
	user, lookupErr := authdb.New(s.pool).GetLoginUser(ctx, email)
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		return TokenPair{}, lookupErr
	}
	hash := user.PasswordHash
	if lookupErr != nil {
		hash = s.dummyHash
	}
	select {
	case s.hashing <- struct{}{}:
	case <-ctx.Done():
		return TokenPair{}, ctx.Err()
	}
	checked, err := password.Verify(input.Password, hash)
	<-s.hashing
	if err != nil {
		return TokenPair{}, err
	}
	if lookupErr != nil || !checked.Matches {
		return TokenPair{}, ErrInvalidCredentials
	}
	if !user.EmailVerified {
		return TokenPair{}, ErrEmailUnverified
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, err
	}
	defer rollback(tx)
	queries := authdb.New(tx)
	current, err := queries.LockLoginUser(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidCredentials
	}
	if err != nil {
		return TokenPair{}, err
	}
	if current.PasswordHash != hash || !current.EmailVerified {
		return TokenPair{}, ErrInvalidCredentials
	}
	id, err := uuid.NewV7()
	if err != nil {
		return TokenPair{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expires := now.Add(sessionLifetime)
	// We replace the previous session while holding the account lock.
	if err := queries.DeleteUserSessions(ctx, user.ID); err != nil {
		return TokenPair{}, err
	}
	if err := queries.CreateSession(ctx, authdb.CreateSessionParams{ID: id, UserID: user.ID,
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return TokenPair{}, err
	}
	manager, err := refreshtoken.NewManager(repositories.NewRefreshStore(tx), refreshtoken.Config{Lifetime: sessionLifetime, Now: func() time.Time { return now }})
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := manager.Create(ctx, id.String())
	if err != nil {
		return TokenPair{}, err
	}
	access, err := s.issuer.IssueUntil(user.ID.String(), id.String(), expires)
	if err != nil {
		return TokenPair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access.Token, AccessTokenExpiresAt: access.ExpiresAt,
		RefreshToken: refresh.Token, RefreshTokenExpiresAt: refresh.Record.ExpiresAt, TokenType: "Bearer",
		User: Profile{ID: current.ID, Name: current.Name, Email: current.Email, Role: string(current.Role), EmailVerified: current.EmailVerified},
	}, nil
}

func (s *Service) Refresh(ctx context.Context, rawToken string) (TokenPair, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, err
	}
	defer rollback(tx)
	at := s.now().UTC().Truncate(time.Microsecond)
	manager, err := refreshtoken.NewManager(repositories.NewRefreshStore(tx), refreshtoken.Config{Lifetime: sessionLifetime, Now: func() time.Time { return at }})
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := manager.Rotate(ctx, rawToken)
	if errors.Is(err, refreshtoken.ErrReuseDetected) {
		// We commit replay invalidation even though the refresh request is rejected.
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, err
		}
		return TokenPair{}, ErrUnauthenticated
	}
	if errors.Is(err, refreshtoken.ErrInvalidToken) || errors.Is(err, refreshtoken.ErrNotFound) || errors.Is(err, refreshtoken.ErrInactive) {
		return TokenPair{}, ErrUnauthenticated
	}
	if err != nil {
		return TokenPair{}, err
	}
	id, err := uuid.Parse(refresh.Record.SessionID)
	if err != nil {
		return TokenPair{}, err
	}
	queries := authdb.New(tx)
	current, err := queries.GetCurrentSession(ctx, authdb.GetCurrentSessionParams{ID: id, ExpiresAt: pgtype.Timestamptz{Time: at, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrUnauthenticated
	}
	if err != nil {
		return TokenPair{}, err
	}
	expiry := at.Add(sessionLifetime)
	// We check before committing so throttling leaves the original refresh token usable.
	if err := ratelimit.CheckIdentity(ctx, "account:"+current.UserID.String()); err != nil {
		return TokenPair{}, err
	}
	if maximum := current.CreatedAt.Time.Add(maximumSessionLifetime); expiry.After(maximum) {
		expiry = maximum
	}
	expires := pgtype.Timestamptz{Time: expiry, Valid: true}
	if err := queries.SetSessionExpiry(ctx, authdb.SetSessionExpiryParams{ID: id, ExpiresAt: expires}); err != nil {
		return TokenPair{}, err
	}
	if err := queries.ExtendRefreshToken(ctx, authdb.ExtendRefreshTokenParams{
		TokenHash: refresh.Record.TokenHash[:], SessionID: id, ExpiresAt: expires,
	}); err != nil {
		return TokenPair{}, err
	}
	current.ExpiresAt = expires
	access, err := s.issuer.IssueUntil(current.UserID.String(), id.String(), current.ExpiresAt.Time)
	if err != nil {
		return TokenPair{}, ErrUnauthenticated
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access.Token, AccessTokenExpiresAt: access.ExpiresAt,
		RefreshToken: refresh.Token, RefreshTokenExpiresAt: current.ExpiresAt.Time, TokenType: "Bearer",
		User: profile(current),
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	resolver := repositories.NewSessionResolver(s.pool, func() time.Time { return s.now() })
	verifier, err := accesstoken.NewVerifier(resolver, s.verifierConfig)
	if err != nil {
		return Principal{}, err
	}
	_, _, err = verifier.VerifySession(ctx, rawToken)
	if resolver.Failure != nil {
		return Principal{}, resolver.Failure
	}
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{SessionID: resolver.Current.ID, User: profile(resolver.Current)}, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	return authdb.New(s.pool).LogoutSession(ctx, authdb.LogoutSessionParams{ID: principal.SessionID, UserID: principal.User.ID})
}

func profile(row authdb.GetCurrentSessionRow) Profile {
	return Profile{ID: row.UserID, Name: row.Name, Email: row.Email, Role: string(row.Role), EmailVerified: row.EmailVerified}
}
