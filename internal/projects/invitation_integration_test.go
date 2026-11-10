package projects

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"sitetrack/internal/config"
	"sitetrack/internal/infra/outbox"
	outboxdb "sitetrack/internal/infra/outbox/repositories/generated"
	"sitetrack/internal/infra/telemetry"
	"sitetrack/internal/notifications"
)

func notificationFlow(t *testing.T, f *fixture) (*notifications.Service, *outbox.Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	service := notifications.NewService(f.s.pool, ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() { defer close(done); service.Run() }()
	monitoring, err := telemetry.New(ctx, config.Telemetry{}, "notification-test-worker", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := outbox.NewWorker(f.s.pool, map[string]outbox.Handler{"notification.project": notifications.NewWorker(f.s.pool).Handle}, nil, monitoring)
	if err != nil {
		t.Fatal(err)
	}
	return service, worker
}
func dispatch(t *testing.T, worker *outbox.Worker) {
	t.Helper()
	for {
		count, err := worker.SendBatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return
		}
	}
}
func TestInvitationAcceptanceAndNotificationReadState(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed, worker := notificationFlow(t, f)
	p := f.create(t, "Invitation Site")
	invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[1])
	if err != nil {
		t.Fatal(err)
	}
	if invite.ID.Version() != 7 || invite.Status != "pending" {
		t.Fatal("invalid pending invitation identity")
	}
	if _, err = f.s.Get(ctx, f.users[1], p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("invitation granted project access before acceptance")
	}
	before, err := feed.List(ctx, f.users[1], notifications.ListInput{Limit: 20})
	if err != nil || len(before.Items) != 0 {
		t.Fatal("notification was published synchronously")
	}
	dispatch(t, worker)
	page, err := feed.List(ctx, f.users[1], notifications.ListInput{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != 1 {
		t.Fatal("invitation notification missing", err)
	}
	n := page.Items[0]
	if n.Invitation == nil || n.Invitation.ID != invite.ID || !n.Invitation.CanRespond {
		t.Fatal("notification lacks actionable invitation")
	}
	if err = feed.Read(ctx, f.users[0], n.ID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatal("other user marked invitee notification read")
	}
	if _, err = f.s.Respond(ctx, f.users[3], invite.ID, "accept"); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatal("other user responded to invitation")
	}
	accepted, err := f.s.Respond(ctx, f.users[1], invite.ID, "accept")
	if err != nil || accepted.Status != "accepted" {
		t.Fatal("accept failed", err)
	}
	member, err := f.s.Get(ctx, f.users[1], p.ID)
	if err != nil || member.IsAdmin {
		t.Fatal("accepted invitation did not add ordinary membership")
	}
	page, err = feed.List(ctx, f.users[1], notifications.ListInput{Limit: 20})
	if err != nil || page.Items[0].Invitation.Status != "accepted" || page.Items[0].Invitation.CanRespond {
		t.Fatal("notification status is stale")
	}
	dispatch(t, worker)
	if count, err := feed.Unread(ctx, f.users[0]); err != nil || count != 1 {
		t.Fatal("admin did not receive member-joined activity")
	}
	if err = feed.Read(ctx, f.users[1], n.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := feed.Unread(ctx, f.users[1]); err != nil || count != 0 {
		t.Fatal("read state did not update count")
	}
	if err = feed.ReadAll(ctx, f.users[0]); err != nil {
		t.Fatal(err)
	}
	if count, err := feed.Unread(ctx, f.users[0]); err != nil || count != 0 {
		t.Fatal("read-all did not update count")
	}
}
func TestInvitationsAreScopedAndRaceToOneOutcome(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	p := f.create(t, "Invitation Race")
	if _, err := f.s.Invite(ctx, f.users[3], p.ID, f.users[1]); !errors.Is(err, ErrNotFound) {
		t.Fatal("outsider invited a user")
	}
	if _, err := f.s.Invite(ctx, f.users[2], p.ID, f.users[1]); !errors.Is(err, ErrForbidden) {
		t.Fatal("superadmin bypassed project admin check")
	}
	if _, err := f.s.Invitations(ctx, f.users[1], p.ID, InvitationListInput{Status: "pending", Limit: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatal("outsider listed invitations")
	}
	if _, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[0]); !errors.Is(err, ErrInvalid) {
		t.Fatal("self invitation accepted")
	}
	if _, err := f.s.Invite(ctx, f.users[0], p.ID, uuid.Must(uuid.NewV7())); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatal("unregistered invitee accepted")
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	invitations := make(chan Invitation, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[1])
			results <- err
			if err == nil {
				invitations <- invite
			}
		})
	}
	close(start)
	group.Wait()
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, ErrInvitationConflict)) || (b == nil && errors.Is(a, ErrInvitationConflict))) {
		t.Fatalf("duplicate invite race: %v / %v", a, b)
	}
	invite := <-invitations
	terminal := make(chan error, 3)
	start = make(chan struct{})
	for _, action := range []string{"accept", "reject"} {
		group.Go(func() { <-start; _, err := f.s.Respond(ctx, f.users[1], invite.ID, action); terminal <- err })
	}
	group.Go(func() { <-start; _, err := f.s.Withdraw(ctx, f.users[0], p.ID, invite.ID); terminal <- err })
	close(start)
	group.Wait()
	successes := 0
	for range 3 {
		err := <-terminal
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvitationConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("invitation reached multiple terminal outcomes")
	}
	var status string
	var members int
	if err := f.s.pool.QueryRow(ctx, "SELECT status FROM sitetrack.project_invitations WHERE id=$1", invite.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.project_memberships WHERE project_id=$1 AND user_id=$2", p.ID, f.users[1]).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if (status == "accepted") != (members == 1) {
		t.Fatal("terminal status and membership disagree")
	}
}
func TestInvitationResponseRollsBackWhenNotificationEnqueueFails(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	p := f.create(t, "Rollback Invitation")
	invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.pool.Exec(ctx, `CREATE FUNCTION sitetrack.reject_notification_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='notification.project' THEN RAISE EXCEPTION 'forced enqueue failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_notification_test BEFORE INSERT ON sitetrack.outbox_events FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_notification_test();`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := f.s.pool.Exec(ctx, "DROP TRIGGER reject_notification_test ON sitetrack.outbox_events; DROP FUNCTION sitetrack.reject_notification_test()")
		if err != nil {
			t.Error(err)
		}
	}()
	if _, err = f.s.Respond(ctx, f.users[1], invite.ID, "accept"); err == nil {
		t.Fatal("accept ignored outbox failure")
	}
	if _, err = f.s.Get(ctx, f.users[1], p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed acceptance left membership")
	}
	var status string
	if err = f.s.pool.QueryRow(ctx, "SELECT status FROM sitetrack.project_invitations WHERE id=$1", invite.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatal("failed acceptance changed invitation")
	}
	if _, err = f.s.Invite(ctx, f.users[0], p.ID, f.users[3]); err == nil {
		t.Fatal("invite ignored outbox failure")
	}
	var count int
	if err = f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.project_invitations WHERE project_id=$1", p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed invite left a row")
	}
}
func TestNotificationReplayIsIdempotentAndAccessChangesApply(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed, worker := notificationFlow(t, f)
	p := f.create(t, "Notification Scope")
	f.add(t, p.ID, 1, false)
	if _, err := f.s.Update(ctx, f.users[0], p.ID, Input{Name: "Updated Site", StartDate: p.StartDate, EndDate: p.EndDate}); err != nil {
		t.Fatal(err)
	}
	token := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}
	q := outboxdb.New(f.s.pool)
	jobs, err := q.ClaimJobs(ctx, outboxdb.ClaimJobsParams{LeaseToken: token, EventTypes: []string{"notification.project"}})
	if err != nil || len(jobs) != 1 {
		t.Fatal("notification claim failed", err)
	}
	if _, err = notifications.NewWorker(f.s.pool).Handle(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.pool.Exec(ctx, "UPDATE sitetrack.outbox_events SET lease_until=now()-interval '1 second' WHERE id=$1", jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	dispatch(t, worker)
	page, err := feed.List(ctx, f.users[1], notifications.ListInput{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != 1 {
		t.Fatal("notification was lost or duplicated after reclaim")
	}
	notificationID := page.Items[0].ID
	if err = f.s.ChangeMember(ctx, f.users[0], p.ID, f.users[1], nil); err != nil {
		t.Fatal(err)
	}
	page, err = feed.List(ctx, f.users[1], notifications.ListInput{Limit: 20})
	if err != nil || len(page.Items) != 0 || page.UnreadCount != 0 {
		t.Fatal("removed member retained notification access")
	}
	if err = feed.Read(ctx, f.users[1], notificationID); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatal("removed member changed inaccessible notification")
	}
}
