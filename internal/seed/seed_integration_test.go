package seed

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/authentication"
	"sitetrack/internal/config"
)

func TestSeedRollbackCountsAndLogin(t *testing.T) {
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("connect test cluster")
	}
	defer admin.Close(context.Background())
	name := "seed_test_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("parse test database URL")
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const secret = DefaultPassword
	interrupted, stop := context.WithCancel(ctx)
	_, err = Run(interrupted, pool, "small", secret, time.Now(), func(p Progress) {
		if p.Phase == "tasks" {
			stop()
		}
	})
	stop()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected rollback after cancellation, got %v", err)
	}
	tables, err := tables(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := empty(ctx, pool, tables); err != nil {
		t.Fatalf("cancelled seed left data: %v", err)
	}
	var guards int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE tgname IN ('guard_task','guard_dependency') AND tgenabled='O'").Scan(&guards); err != nil || guards != 2 {
		t.Fatalf("task guards not restored: %d, %v", guards, err)
	}
	report, err := Run(ctx, pool, "small", "", time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["delivery_items"] != 600 || report.Counts["tasks"] != 200 || report.PendingCompanyDelivery == uuid.Nil || report.PendingProjectDelivery == uuid.Nil {
		t.Fatalf("incomplete report: %+v", report.Counts)
	}
	if _, err := Run(ctx, pool, "small", secret, time.Now(), nil); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("populated database accepted: %v", err)
	}
	service, err := authentication.NewService(pool, config.Authentication{
		OTPSecret: []byte("12345678901234567890123456789012"), OutboxKey: []byte("abcdefghijklmnopqrstuvxyz1234567"), JWTSecret: []byte("12345678901234567890123456789012"), EmailFrom: "SiteTrack <sender@example.test>",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range report.Accounts {
		pair, err := service.Login(ctx, authentication.LoginInput{Email: account.Email, Password: secret})
		if err != nil {
			t.Fatalf("seed account login: %v", err)
		}
		principal, err := service.Authenticate(ctx, pair.AccessToken)
		if err != nil || principal.User.ID != account.ID {
			t.Fatalf("seed account session: %v", err)
		}
		if err := service.Logout(ctx, principal); err != nil {
			t.Fatal(err)
		}
	}
	var members int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.project_memberships WHERE project_id=$1 AND user_id IN ($2,$3)", report.Projects[0], report.Accounts[2].ID, report.Accounts[3].ID).Scan(&members); err != nil || members != 2 {
		t.Fatalf("sample membership: %d, %v", members, err)
	}
	var outside bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM sitetrack.project_memberships WHERE project_id=$1 AND user_id=$2)", report.Projects[0], report.Accounts[4].ID).Scan(&outside); err != nil || outside {
		t.Fatalf("outsider is a member: %v", err)
	}
	var jobs int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.outbox_events").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("seed triggered email: %v", err)
	}
}
