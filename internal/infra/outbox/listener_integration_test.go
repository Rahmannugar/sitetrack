package outbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/infra/database"
)

func TestOutboxWakeupFollowsCommitAndRecovers(t *testing.T) {
	url := os.Getenv("SITETRACK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SITETRACK_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	application := "outbox-listener-test-" + uuid.NewString()
	cfg.ConnConfig.RuntimeParams["application_name"] = application
	cfg.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	listenContext, stop := context.WithCancel(ctx)
	go func() {
		defer close(done)
		Listen(listenContext, pool, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	defer func() { stop(); <-done }()
	receive := func() {
		t.Helper()
		select {
		case <-wake:
		case <-ctx.Done():
			t.Fatal("listener did not wake")
		}
	}
	receive()
	aggregate := uuid.Must(uuid.NewV7())
	defer func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM sitetrack.outbox_events WHERE aggregate_id=$1", aggregate)
	}()
	enqueue := func(commit bool) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, "INSERT INTO sitetrack.outbox_events(id,aggregate_id,event_type,payload,expires_at) VALUES($1,$2,'listener.test',$3,now()+interval '1 minute')", uuid.Must(uuid.NewV7()), aggregate, []byte("test payload"))
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-wake:
			t.Fatal("outbox notification arrived before commit")
		case <-time.After(100 * time.Millisecond):
		}
		if commit {
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	enqueue(false)
	select {
	case <-wake:
		t.Fatal("rolled-back outbox row woke listener")
	case <-time.After(100 * time.Millisecond):
	}
	enqueue(true)
	receive()
	var terminated bool
	if err = pool.QueryRow(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name=$1 AND query='LISTEN sitetrack_outbox' AND pid<>pg_backend_pid()", application).Scan(&terminated); err != nil || !terminated {
		t.Fatal("could not terminate listener connection", err)
	}
	receive()
	enqueue(true)
	receive()
	stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("listener did not stop")
	}
	released := time.NewTicker(10 * time.Millisecond)
	defer released.Stop()
	for pool.Stat().AcquiredConns() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("listener retained its pool connection")
		case <-released.C:
		}
	}
}
