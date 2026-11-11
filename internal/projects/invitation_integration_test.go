package projects

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"sitetrack/internal/notifications"
)

func notificationFlow(t *testing.T, f *fixture) *notifications.Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	service := notifications.NewService(f.s.pool, ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() { defer close(done); service.Run() }()
	return service
}
func TestInvitationAcceptanceAndNotificationReadState(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed := notificationFlow(t, f)
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
func TestInvitationResponseRollsBackWhenNotificationInsertFails(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	p := f.create(t, "Rollback Invitation")
	invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.pool.Exec(ctx, `CREATE FUNCTION sitetrack.reject_notification_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced notification failure'; END $$;
 CREATE TRIGGER reject_notification_test BEFORE INSERT ON sitetrack.notifications FOR EACH ROW EXECUTE FUNCTION sitetrack.reject_notification_test();`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := f.s.pool.Exec(ctx, "DROP TRIGGER reject_notification_test ON sitetrack.notifications; DROP FUNCTION sitetrack.reject_notification_test()")
		if err != nil {
			t.Error(err)
		}
	}()
	if _, err = f.s.Respond(ctx, f.users[1], invite.ID, "accept"); err == nil {
		t.Fatal("accept ignored notification failure")
	}
	if _, err = f.s.Get(ctx, f.users[1], p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed acceptance left membership")
	}
	var status string
	if err = f.s.pool.QueryRow(ctx, "SELECT status FROM sitetrack.project_invitations WHERE id=$1", invite.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatal("failed acceptance changed invitation")
	}
	if _, err = f.s.Invite(ctx, f.users[0], p.ID, f.users[3]); err == nil {
		t.Fatal("invite ignored notification failure")
	}
	var count int
	if err = f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.project_invitations WHERE project_id=$1", p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed invite left a row")
	}
}
func TestNotificationCreationAndAccessChangesApply(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed := notificationFlow(t, f)
	p := f.create(t, "Notification Scope")
	f.add(t, p.ID, 1, false)
	if _, err := f.s.Update(ctx, f.users[0], p.ID, Input{Name: "Updated Site", StartDate: p.StartDate, EndDate: p.EndDate}); err != nil {
		t.Fatal(err)
	}
	page, err := feed.List(ctx, f.users[1], notifications.ListInput{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.UnreadCount != 1 {
		t.Fatal("notification was not created with the project update")
	}
	var jobs int
	if err = f.s.pool.QueryRow(ctx, "SELECT count(*) FROM sitetrack.outbox_events WHERE event_type='notification.project'").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("notification creation used the outbox")
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

func TestConcurrentNotificationCreationAndReads(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed := notificationFlow(t, f)
	for range 3 {
		left := f.create(t, "Left Site")
		right, err := f.s.Create(ctx, f.users[1], Input{Name: "Right Site", StartDate: left.StartDate, EndDate: left.EndDate})
		if err != nil {
			t.Fatal(err)
		}
		a, err := f.s.Invite(ctx, f.users[0], left.ID, f.users[1])
		if err != nil {
			t.Fatal(err)
		}
		b, err := f.s.Invite(ctx, f.users[1], right.ID, f.users[0])
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 4)
		var group sync.WaitGroup
		for _, call := range []func() error{
			func() error { _, err := f.s.Respond(ctx, f.users[1], a.ID, "accept"); return err },
			func() error { _, err := f.s.Respond(ctx, f.users[0], b.ID, "accept"); return err },
			func() error { return feed.ReadAll(ctx, f.users[0]) },
			func() error { return feed.ReadAll(ctx, f.users[1]) },
		} {
			group.Go(func() { <-start; results <- call() })
		}
		close(start)
		group.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal("concurrent notification operation failed", err)
			}
		}
	}
}

func TestInvitationExpiryAndReinvite(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed := notificationFlow(t, f)
	p := f.create(t, "Expiry Site")
	for _, target := range []int{1, 2} {
		invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[target])
		if err != nil {
			t.Fatal(err)
		}
		if invite.ExpiresAt.Sub(invite.CreatedAt) != 7*24*time.Hour {
			t.Fatal("invitation lifetime is not seven days")
		}
		created := time.Now().UTC().Add(-8 * 24 * time.Hour).Truncate(time.Microsecond)
		if _, err := f.s.pool.Exec(ctx, "UPDATE sitetrack.project_invitations SET created_at=$2,expires_at=$3 WHERE id=$1", invite.ID, created, created.Add(7*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		pending, err := f.s.Invitations(ctx, f.users[0], p.ID, InvitationListInput{Status: "pending", Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range pending.Items {
			if row.ID == invite.ID {
				t.Fatal("expired invitation remained pending")
			}
		}
		history, err := f.s.Invitations(ctx, f.users[0], p.ID, InvitationListInput{Status: "expired", Limit: 20})
		if err != nil || len(history.Items) == 0 {
			t.Fatalf("expired invitation missing: %v", err)
		}
		page, err := feed.List(ctx, f.users[target], notifications.ListInput{Limit: 20})
		if err != nil || len(page.Items) != 1 || page.Items[0].Invitation.Status != "expired" || page.Items[0].Invitation.CanRespond {
			t.Fatalf("expired notification remains actionable: %v", err)
		}
		if target == 1 {
			if _, err := f.s.Respond(ctx, f.users[target], invite.ID, "accept"); !errors.Is(err, ErrInvitationConflict) {
				t.Fatalf("expired invitation accepted: %v", err)
			}
			if _, err := f.s.Get(ctx, f.users[target], p.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("expired invitation granted access")
			}
		}
		replacement, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[target])
		if err != nil || replacement.ID == invite.ID || replacement.Status != "pending" {
			t.Fatalf("expired invitation blocked reinvite: %v", err)
		}
		if _, err := f.s.Respond(ctx, f.users[target], invite.ID, "reject"); !errors.Is(err, ErrInvitationConflict) {
			t.Fatal("old invitation remained usable")
		}
		accepted, err := f.s.Respond(ctx, f.users[target], replacement.ID, "accept")
		if err != nil || accepted.Status != "accepted" {
			t.Fatalf("replacement could not be accepted: %v", err)
		}
	}
}
