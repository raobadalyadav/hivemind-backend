// Package push sends push notifications via Firebase Cloud Messaging — the
// "push" channel in internal/notifications. Uses the official Firebase
// Admin SDK rather than hand-rolling FCM's HTTP v1 API, since that requires
// OAuth2 service-account JWT signing/refresh the SDK already handles.
package push

import (
	"context"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

type Client struct {
	fcm *messaging.Client
}

func NewClient(ctx context.Context, credentialsPath string) (*Client, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		return nil, err
	}
	fcm, err := app.Messaging(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{fcm: fcm}, nil
}

func (c *Client) Send(ctx context.Context, deviceToken, title, body string, data map[string]string) error {
	_, err := c.fcm.Send(ctx, &messaging.Message{
		Token:        deviceToken,
		Notification: &messaging.Notification{Title: title, Body: body},
		Data:         data,
	})
	return err
}
