package projects

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/gin-gonic/gin"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/notifications"
)

func TestInvitationAndNotificationHTTPPagination(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed := notificationFlow(t, f)
	p := f.create(t, "Pagination Site")
	for range 25 {
		invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.Withdraw(ctx, f.users[0], p.ID, invite.ID); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	contracts := openapi.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Register(router.Group(""), f.s, f.accounts, contracts, false, logger); err != nil {
		t.Fatal(err)
	}
	if err := notifications.Register(router.Group(""), router.Group(""), feed, f.accounts, contracts, false, logger); err != nil {
		t.Fatal(err)
	}
	adminToken, inviteeToken := token(t, f, 0), token(t, f, 1)
	result := perform(router, "GET", "/notifications", inviteeToken, "")
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body)
	}
	var page notifications.Page
	var err error
	if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 20 || page.NextCursor == "" || page.UnreadCount != 25 {
		t.Fatal("notification default pagination or count is wrong")
	}
	for _, item := range page.Items {
		if item.Invitation == nil || item.Invitation.CanRespond || item.Invitation.Status != "withdrawn" {
			t.Fatal("withdrawn invitation remains actionable")
		}
	}
	result = perform(router, "GET", "/notifications?cursor="+page.NextCursor, inviteeToken, "")
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body)
	}
	if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil || len(page.Items) != 5 || page.NextCursor != "" {
		t.Fatal("notification cursor skipped or repeated rows", err)
	}
	result = perform(router, "GET", "/projects/"+p.ID+"/invitations?status=all", adminToken, "")
	var invites InvitationPage
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body)
	}
	if err = json.Unmarshal(result.Body.Bytes(), &invites); err != nil || len(invites.Items) != 20 || invites.NextCursor == "" {
		t.Fatal("invitation default pagination is wrong", err)
	}
	result = perform(router, "GET", "/projects/"+p.ID+"/invitations?status=all&cursor="+invites.NextCursor, adminToken, "")
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body)
	}
	if err = json.Unmarshal(result.Body.Bytes(), &invites); err != nil || len(invites.Items) != 5 || invites.NextCursor != "" {
		t.Fatal("invitation cursor is wrong", err)
	}
	for _, path := range []string{"/notifications?limit=101", "/notifications?limit=0", "/notifications?cursor=broken", "/projects/" + p.ID + "/invitations?limit=101"} {
		if result = perform(router, "GET", path, adminToken, ""); result.Code != 400 {
			t.Fatal("invalid pagination accepted", path, result.Code)
		}
	}
	if result = perform(router, "GET", "/projects/"+p.ID+"/invitations", inviteeToken, ""); result.Code != 404 {
		t.Fatal("invitee gained project invitation-list access")
	}
	for range 20 {
		f.create(t, "List Default")
	}
	result = perform(router, "GET", "/projects", adminToken, "")
	var projects Page
	if result.Code != 200 {
		t.Fatal(result.Code, result.Body)
	}
	if err = json.Unmarshal(result.Body.Bytes(), &projects); err != nil || len(projects.Items) != 20 || projects.NextCursor == "" {
		t.Fatal("project default is not twenty", err)
	}
	result = perform(router, "GET", "/projects?limit=100", adminToken, "")
	if err = json.Unmarshal(result.Body.Bytes(), &projects); err != nil || len(projects.Items) != 21 {
		t.Fatal("valid larger page rejected", err)
	}
	raw, err := contracts.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var document openapi.Document
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/projects", "/projects/{id}/members", "/projects/{id}/invitations", "/notifications"} {
		for _, parameter := range document.Paths[path]["get"].Parameters {
			if parameter.Name == "limit" && string(parameter.Schema.Default) != "20" {
				t.Fatal("documented default differs from runtime", path)
			}
		}
	}
	for _, methods := range document.Paths {
		for _, operation := range methods {
			for _, response := range operation.Responses {
				for _, media := range response.Content {
					schema, err := media.Schema.Resolve(nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, example := range media.Examples {
						var value any
						if err = json.Unmarshal(example.Value, &value); err != nil {
							t.Fatal(err)
						}
						if err = schema.Validate(value); err != nil {
							t.Fatal("response example disagrees with schema", operation.OperationID, err)
						}
					}
				}
			}
		}
	}
}
