// Package email sends transactional email via Resend — recovery codes
// (internal/auth) and the "email" notification channel (internal/notifications).
package email

import (
	"context"

	"github.com/resend/resend-go/v2"
)

type Client struct {
	resend *resend.Client
	from   string
}

func NewClient(apiKey, fromAddress string) *Client {
	return &Client{resend: resend.NewClient(apiKey), from: fromAddress}
}

func (c *Client) Send(ctx context.Context, to, subject, htmlBody string) error {
	_, err := c.resend.Emails.SendWithContext(ctx, &resend.SendEmailRequest{
		From:    c.from,
		To:      []string{to},
		Subject: subject,
		Html:    htmlBody,
	})
	return err
}
