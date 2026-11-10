package email

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/resend/resend-go/v4"
)

type Sender interface {
	Send(context.Context, Message, string) (string, error)
}

type Message struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

type DeliveryError struct {
	Code       string
	Permanent  bool
	RetryAfter time.Duration
}

func (e *DeliveryError) Error() string { return e.Code }

type ResendSender struct{ client *resend.Client }

func NewResendSender(key string) (*ResendSender, error) {
	if key == "" {
		return nil, errors.New("SITETRACK_RESEND_API_KEY is required for the worker")
	}
	return &ResendSender{client: resend.NewCustomClient(&http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}, key)}, nil
}

func (s *ResendSender) Send(ctx context.Context, message Message, key string) (string, error) {
	response, err := s.client.Emails.SendWithOptions(ctx, &resend.SendEmailRequest{
		From: message.From, To: []string{message.To}, Subject: message.Subject,
		Text: message.Text,
	}, &resend.SendEmailOptions{IdempotencyKey: key})
	if err == nil {
		if response == nil || response.Id == "" {
			return "", &DeliveryError{Code: "empty_provider_response"}
		}
		return response.Id, nil
	}
	var limited *resend.RateLimitError
	if errors.As(err, &limited) {
		seconds, _ := strconv.ParseInt(limited.RetryAfter, 10, 32)
		return "", &DeliveryError{Code: "provider_rate_limit", RetryAfter: time.Duration(max(0, seconds)) * time.Second}
	}
	var provider *resend.ResendError
	if errors.As(err, &provider) {
		return "", &DeliveryError{Code: "provider_rejected", Permanent: provider.StatusCode >= 400 && provider.StatusCode < 500 && provider.StatusCode != 408 && provider.StatusCode != 409}
	}
	return "", &DeliveryError{Code: "provider_transport"}
}
