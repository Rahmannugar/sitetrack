package authentication

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sitetrack/internal/infra/email"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
)

func awaitWorker(t *testing.T, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-timer.C:
			t.Fatal("worker condition did not complete")
		case <-ticker.C:
		}
	}
}

func TestWorkerBatchesTwentyAndWaitsBeforeNextClaim(t *testing.T) {
	s, worker, _ := accountTest(t)
	ctx := context.Background()
	_, id := newAccount(t, s)
	original, _ := queuedCode(t, s, id)
	var ciphertext []byte
	if err := s.pool.QueryRow(ctx, "SELECT payload FROM sitetrack.outbox_events WHERE id=$1", original).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	payload, err := s.cipher.Open(ciphertext, original[:])
	if err != nil {
		t.Fatal(err)
	}
	for range 40 {
		job := uuid.Must(uuid.NewV7())
		if err = worker.queries.Enqueue(ctx, outboxdb.EnqueueParams{ID: job, AggregateID: id, EventType: "email.verification", Payload: s.cipher.Seal(payload, job[:]), ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{}, 41)
	release := make(chan struct{}, 1)
	var active, peak, calls atomic.Int32
	worker.sender = emailSenderFunc(func(ctx context.Context, _ email.Message, _ string) (string, error) {
		count := active.Add(1)
		defer active.Add(-1)
		for current := peak.Load(); count > current; current = peak.Load() {
			if peak.CompareAndSwap(current, count) {
				break
			}
		}
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return "delivered", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(run) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("worker did not drain")
		}
	}()
	for range 20 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("batch did not start twenty jobs")
		}
	}
	countStatus := func(status string) int {
		t.Helper()
		var count int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.outbox_events WHERE aggregate_id=$1 AND status=$2", id, status).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if countStatus("processing") != 20 || countStatus("pending") != 21 {
		t.Fatal("claim exceeded batch size")
	}
	release <- struct{}{}
	awaitWorker(t, func() bool { return countStatus("published") == 1 })
	if calls.Load() != 20 || countStatus("pending") != 21 {
		t.Fatal("worker claimed another batch before finishing current batch")
	}
	close(release)
	awaitWorker(t, func() bool { return countStatus("published") == 41 })
	cancel()
	<-done
	if peak.Load() != 20 || calls.Load() != 41 {
		t.Fatalf("unexpected concurrency or duplicate execution: peak=%d calls=%d", peak.Load(), calls.Load())
	}
	var unfinished int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.outbox_events WHERE aggregate_id=$1 AND (attempts<>1 OR payload IS NOT NULL OR lease_token IS NOT NULL OR lease_until IS NOT NULL)", id).Scan(&unfinished); err != nil || unfinished != 0 {
		t.Fatal("batch acknowledgements left active job state")
	}
}

func TestWorkerPollingFindsJobWhenItBecomesDueWithoutAnotherNotify(t *testing.T) {
	s, worker, _ := accountTest(t)
	_, id := newAccount(t, s)
	job, _ := queuedCode(t, s, id)
	execAccountSQL(t, s.pool, "UPDATE sitetrack.outbox_events SET available_at=now()+interval '1 second' WHERE id=$1", job)
	sent := make(chan struct{}, 1)
	worker.sender = emailSenderFunc(func(context.Context, email.Message, string) (string, error) { sent <- struct{}{}; return "polled", nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-sent:
		t.Fatal("job processed before its retry time")
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case <-sent:
	case <-time.After(7 * time.Second):
		t.Fatal("polling did not discover due work")
	}
}

func TestRetryDelayHasExponentialJitterAndHonorsProviderWait(t *testing.T) {
	for _, test := range []struct {
		attempt                int32
		provider, lower, upper time.Duration
	}{
		{1, 0, time.Second, 2 * time.Second},
		{4, 0, 8 * time.Second, 16 * time.Second},
		{4, 30 * time.Second, 30 * time.Second, 38 * time.Second},
		{8, 0, 64 * time.Second, 128 * time.Second},
	} {
		for range 100 {
			delay := retryDelay(test.attempt, test.provider)
			if delay < test.lower || delay > test.upper {
				t.Fatalf("retry delay outside bounds: %v", delay)
			}
		}
	}
}
