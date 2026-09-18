// Package eventbus wraps NATS JetStream publish/subscribe and implements the
// outbox pattern described in PRD §19: services write domain events to the
// outbox_events table in the same DB transaction as their state change;
// cmd/worker polls the table and publishes here, so a crash between "DB
// committed" and "event published" can never lose an event.
package eventbus

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	StreamName          = "HIVEMIND_EVENTS"
	streamCreateTimeout = 5 * time.Second
)

type Publisher struct {
	js jetstream.JetStream
}

func Connect(natsURL string) (*Publisher, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), streamCreateTimeout)
	defer cancel()
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{"events.>"},
	})
	if err != nil {
		return nil, err
	}

	return &Publisher{js: js}, nil
}

func (p *Publisher) Publish(ctx context.Context, subject string, data []byte) error {
	_, err := p.js.Publish(ctx, "events."+subject, data)
	return err
}

func (p *Publisher) Consume(ctx context.Context, consumerName string, subject string, handler func(msg jetstream.Msg) error) error {
	cons, err := p.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: "events." + subject,
	})
	if err != nil {
		return err
	}

	_, err = cons.Consume(func(msg jetstream.Msg) {
		if err := handler(msg); err != nil {
			msg.Nak()
			return
		}
		msg.Ack()
	})
	return err
}
