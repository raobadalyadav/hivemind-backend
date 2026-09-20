// Package eventbus wraps NATS JetStream publish/subscribe and implements the
// outbox pattern described in PRD §19: services write domain events to the
// outbox_events table in the same DB transaction as their state change;
// cmd/worker polls the table and publishes here, so a crash between "DB
// committed" and "event published" can never lose an event.
package eventbus

import (
	"context"
	"log/slog"
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

// maxDeliveries is how many times an event is tried before it's dead-lettered.
const maxDeliveries = 6

func (p *Publisher) Consume(ctx context.Context, consumerName string, subject string, handler func(msg jetstream.Msg) error) error {
	cons, err := p.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: "events." + subject,
		// A message that keeps failing is retried with growing pauses and then given up on (dead-lettered),
		// instead of being redelivered forever and blocking everything behind it.
		MaxDeliver: maxDeliveries,
		BackOff:    []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour},
	})
	if err != nil {
		return err
	}

	_, err = cons.Consume(func(msg jetstream.Msg) {
		if err := handler(msg); err != nil {
			if md, merr := msg.Metadata(); merr == nil && md.NumDelivered >= maxDeliveries {
				slog.Error("event dead-lettered after repeated failures", "consumer", consumerName, "subject", msg.Subject(), "error", err, "attempts", md.NumDelivered)
				_ = msg.Term()
				return
			}
			_ = msg.Nak() // redelivered after the consumer's BackOff
			return
		}
		msg.Ack()
	})
	return err
}
