package email

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/resend/resend-go/v4"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResendUsesContextAndIdempotencyKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sender := &ResendSender{client: resend.NewCustomClient(&http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Idempotency-Key") != "email.verification/job-1" {
			t.Fatal("missing provider authentication or stable idempotency key")
		}
		if _, exists := r.Context().Deadline(); !exists {
			t.Fatal("provider request lost the operation timeout")
		}
		var body resend.SendEmailRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Subject != "Verify email" || body.Text != "Code: 123456" || len(body.To) != 1 || body.To[0] != "recipient@example.test" {
			t.Fatal("shared email adapter changed the domain's email content")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"provider-1"}`))}, nil
	})}, "test-key")}
	id, err := sender.Send(ctx, Message{From: "sender@example.test", To: "recipient@example.test", Subject: "Verify email", Text: "Code: 123456"}, "email.verification/job-1")
	if err != nil || id != "provider-1" {
		t.Fatalf("provider result: %q, %v", id, err)
	}
}

func TestResendClassifiesErrorsWithoutExposingResponseText(t *testing.T) {
	for _, test := range []struct {
		status    int
		permanent bool
		retry     time.Duration
	}{
		{http.StatusBadRequest, true, 0}, {http.StatusServiceUnavailable, false, 0}, {http.StatusTooManyRequests, false, 3 * time.Second},
	} {
		sender := &ResendSender{client: resend.NewCustomClient(&http.Client{Transport: testTransport(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, Header: http.Header{"Retry-After": []string{"3"}}, Body: io.NopCloser(strings.NewReader(`{"name":"provider_error","message":"private-provider-response"}`))}, nil
		})}, "test-key")}
		_, err := sender.Send(context.Background(), Message{From: "sender@example.test", To: "recipient@example.test", Subject: "Subject", Text: "Body"}, "stable-key")
		var delivery *DeliveryError
		if !errors.As(err, &delivery) || delivery.Permanent != test.permanent || delivery.RetryAfter != test.retry {
			t.Fatalf("incorrect error classification for HTTP %d: %v", test.status, err)
		}
		if strings.Contains(err.Error(), "private-provider-response") {
			t.Fatal("provider payload leaked into an operational error")
		}
	}
}
