package projects

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"sitetrack/internal/infra/httpserver"
	"sitetrack/internal/infra/openapi"
	"sitetrack/internal/notifications"
)

type streamFrame struct {
	event, id string
	data      []byte
}

func nextFrame(t *testing.T, reader *bufio.Reader) streamFrame {
	t.Helper()
	var frame streamFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("SSE read failed", err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			if frame.event != "" {
				return frame
			}
			continue
		}
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch key {
		case "id":
			frame.id = value
		case "event":
			frame.event = value
		case "data":
			frame.data = []byte(value)
		}
	}
}
func notificationFrame(t *testing.T, reader *bufio.Reader) (streamFrame, []notifications.Notification) {
	t.Helper()
	for {
		frame := nextFrame(t, reader)
		if frame.event != "notifications" {
			continue
		}
		var data struct {
			Items []notifications.Notification `json:"items"`
		}
		if err := json.Unmarshal(frame.data, &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Items) > 20 {
			t.Fatal("SSE batch exceeded twenty")
		}
		if len(data.Items) > 0 {
			return frame, data.Items
		}
	}
}
func TestNotificationStreamReplayAccessAndSessionRevocation(t *testing.T) {
	f := projectTest(t)
	ctx := context.Background()
	feed := notificationFlow(t, f)
	p := f.create(t, "Streaming Site")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	contracts := openapi.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := notifications.Register(router.Group("", httpserver.Timeout(50*time.Millisecond)), router.Group(""), feed, f.accounts, contracts, false, logger); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(router)
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	jwt := token(t, f, 1)
	streamContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	connect := func(cursor string) *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(streamContext, "GET", server.URL+"/notifications/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+jwt)
		if cursor != "" {
			request.Header.Set("Last-Event-ID", cursor)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			response.Body.Close()
			t.Fatal("stream did not open", response.StatusCode)
		}
		return response
	}
	response := connect("")
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if frame := nextFrame(t, reader); frame.event != "snapshot" {
		t.Fatal("missing initial snapshot")
	}
	<-time.After(100 * time.Millisecond)
	invite, err := f.s.Invite(ctx, f.users[0], p.ID, f.users[1])
	if err != nil {
		t.Fatal(err)
	}
	frame, items := notificationFrame(t, reader)
	if len(items) != 1 || items[0].Invitation == nil || items[0].Invitation.ID != invite.ID {
		t.Fatal("live invitation was not delivered")
	}
	cursor := frame.id
	notificationID := items[0].ID
	response.Body.Close()
	if _, err = f.s.Respond(ctx, f.users[1], invite.ID, "accept"); err != nil {
		t.Fatal(err)
	}
	resumed := connect(cursor)
	defer resumed.Body.Close()
	reader = bufio.NewReader(resumed.Body)
	_, items = notificationFrame(t, reader)
	if len(items) != 1 || items[0].ID != notificationID || items[0].Invitation.Status != "accepted" {
		t.Fatal("resume did not replay invitation status change")
	}
	if err = feed.Read(ctx, f.users[1], notificationID); err != nil {
		t.Fatal(err)
	}
	_, items = notificationFrame(t, reader)
	if items[0].ReadAt == nil {
		t.Fatal("read update was not streamed")
	}
	for range 25 {
		if _, err = f.s.Update(ctx, f.users[0], p.ID, Input{Name: "Streaming Site Updated", StartDate: p.StartDate, EndDate: p.EndDate}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uuid.UUID]bool{}
	for len(seen) < 25 {
		_, items = notificationFrame(t, reader)
		for _, item := range items {
			if item.Type == "project.updated" {
				seen[item.ID] = true
			}
		}
	}
	principal, err := f.accounts.Authenticate(ctx, jwt)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.accounts.Logout(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Update(ctx, f.users[0], p.ID, Input{Name: "After Logout", StartDate: p.StartDate, EndDate: p.EndDate}); err != nil {
		t.Fatal(err)
	}
	for {
		frame := nextFrame(t, reader)
		if frame.event == "session_expired" {
			break
		}
		if frame.event == "notifications" {
			t.Fatal("stream delivered after session revocation")
		}
	}
	if _, err = reader.ReadByte(); err != io.EOF {
		t.Fatal("revoked session stream did not close", err)
	}
	if result := perform(router, "GET", "/notifications/stream", jwt, ""); result.Code != 401 {
		t.Fatal("logged-out stream token accepted")
	}
}
