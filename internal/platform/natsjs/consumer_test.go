package natsjs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
)

func TestDecide(t *testing.T) {
	handlerErr := errors.New("handler failed")
	if decide(1, 3, nil, nil).kind != dispositionAck {
		t.Fatal("successful handling must ack")
	}
	if decide(1, 3, nil, handlerErr).kind != dispositionRetry {
		t.Fatal("failure below max deliveries must retry")
	}
	if decide(2, 3, nil, handlerErr).kind != dispositionRetry {
		t.Fatal("second of three deliveries must retry")
	}
	if decide(3, 3, nil, handlerErr).kind != dispositionDeadLetter {
		t.Fatal("failure at max deliveries must dead-letter")
	}
	if decide(4, 3, nil, handlerErr).kind != dispositionDeadLetter {
		t.Fatal("failure beyond max deliveries must dead-letter")
	}
}

func TestBackoffFor(t *testing.T) {
	backoff := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
	cases := []struct {
		deliveries int
		want       time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 5 * time.Second},
		{4, 5 * time.Second},
		{100, 5 * time.Second},
	}
	for _, testCase := range cases {
		if got := backoffFor(testCase.deliveries, backoff); got != testCase.want {
			t.Fatalf("backoffFor(%d) = %v, want %v", testCase.deliveries, got, testCase.want)
		}
	}
	if got := backoffFor(1, nil); got != time.Second {
		t.Fatalf("empty backoff = %v, want 1s fallback", got)
	}
}

func TestConsumerConfigDefaults(t *testing.T) {
	config := ConsumerConfig{}.withDefaults()
	if config.MaxDeliver != 5 || config.AckWait != 60*time.Second || len(config.Backoff) != 5 || config.Logger == nil {
		t.Fatalf("unexpected defaults: %+v", config)
	}
}

func testConsumer(t *testing.T, url string) *Consumer {
	t.Helper()
	consumer, err := NewConsumer(url, ConsumerConfig{
		MaxDeliver: 3,
		Backoff:    []time.Duration{50 * time.Millisecond, 100 * time.Millisecond},
		AckWait:    10 * time.Second,
		Logger:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	t.Cleanup(consumer.Close)
	return consumer
}

func publishTestEvent(t *testing.T, publisher *Publisher, subject string, payload string) events.Event {
	t.Helper()
	event := events.Event{
		ID:            uuid.NewString(),
		Type:          subject,
		AggregateType: "test",
		AggregateID:   uuid.NewString(),
		OccurredAt:    time.Now().UTC(),
		Payload:       []byte(payload),
	}
	if err := publisher.Publish(event); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	return event
}

func TestConsumerAcksSuccessfulEvents(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("NATS_TEST_URL is not set")
	}
	publisher, err := Connect(url)
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	t.Cleanup(publisher.Close)
	consumer := testConsumer(t, url)

	subject := "game.test." + uuid.NewString()
	durable := "test-" + uuid.NewString()
	t.Cleanup(func() { _ = consumer.jetStream.DeleteConsumer(streamName, durable) })
	processed := make(chan events.Event, 1)
	err = consumer.Subscribe(subject, durable, func(_ context.Context, event events.Event) error {
		processed <- event
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	published := publishTestEvent(t, publisher, subject, `{"ok":true}`)

	select {
	case event := <-processed:
		if event.ID != published.ID || event.Type != subject || string(event.Payload) != `{"ok":true}` {
			t.Fatalf("consumed event = %+v, want id %s on %s", event, published.ID, subject)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("event was not consumed in time")
	}
	stats := consumer.Stats()
	if stats.Received != 1 || stats.Processed != 1 || stats.Retried != 0 || stats.DeadLettered != 0 {
		t.Fatalf("stats = %+v, want 1 received / 1 processed", stats)
	}
}

func TestConsumerRetriesThenSucceeds(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("NATS_TEST_URL is not set")
	}
	publisher, err := Connect(url)
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	t.Cleanup(publisher.Close)
	consumer := testConsumer(t, url)

	subject := "game.test." + uuid.NewString()
	durable := "test-" + uuid.NewString()
	t.Cleanup(func() { _ = consumer.jetStream.DeleteConsumer(streamName, durable) })
	attempts := make(chan int, 8)
	remaining := 2
	err = consumer.Subscribe(subject, durable, func(_ context.Context, _ events.Event) error {
		attempts <- 1
		if remaining > 0 {
			remaining--
			return errors.New("transient failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publishTestEvent(t, publisher, subject, `{"retry":true}`)

	for expected := 1; expected <= 3; expected++ {
		select {
		case <-attempts:
		case <-time.After(10 * time.Second):
			t.Fatalf("attempt %d did not happen in time", expected)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats := consumer.Stats()
		if stats.Processed == 1 && stats.Retried == 2 && stats.DeadLettered == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v, want 1 processed / 2 retried", stats)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConsumerDeadLettersExhaustedEvents(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("NATS_TEST_URL is not set")
	}
	publisher, err := Connect(url)
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	t.Cleanup(publisher.Close)
	consumer := testConsumer(t, url)

	subject := "game.test." + uuid.NewString()
	durable := "test-" + uuid.NewString()
	t.Cleanup(func() { _ = consumer.jetStream.DeleteConsumer(streamName, durable) })
	deadLettered := make(chan struct{}, 1)
	err = consumer.Subscribe(subject, durable, func(_ context.Context, _ events.Event) error {
		return errors.New("permanent failure")
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	before, err := consumer.jetStream.StreamInfo(deadLetterStreamName)
	if err != nil {
		t.Fatalf("read dead-letter stream before: %v", err)
	}
	published := publishTestEvent(t, publisher, subject, `{"dead":true}`)

	deadline := time.Now().Add(15 * time.Second)
	for {
		stats := consumer.Stats()
		if stats.DeadLettered == 1 {
			close(deadLettered)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v, want 1 dead-lettered", stats)
		}
		time.Sleep(50 * time.Millisecond)
	}
	<-deadLettered

	after, err := consumer.jetStream.StreamInfo(deadLetterStreamName)
	if err != nil {
		t.Fatalf("read dead-letter stream after: %v", err)
	}
	if after.State.Msgs != before.State.Msgs+1 {
		t.Fatalf("dead-letter messages = %d, want %d", after.State.Msgs, before.State.Msgs+1)
	}
	message, err := consumer.jetStream.GetMsg(deadLetterStreamName, after.State.LastSeq)
	if err != nil {
		t.Fatalf("read dead-letter message: %v", err)
	}
	if message.Subject != "deadletter."+subject {
		t.Fatalf("dead-letter subject = %q, want %q", message.Subject, "deadletter."+subject)
	}
	if string(message.Data) != string(published.Payload) {
		t.Fatalf("dead-letter payload = %q, want %q", message.Data, published.Payload)
	}
	if message.Header.Get("X-Event-Id") != published.ID {
		t.Fatalf("dead-letter event id = %q, want %q", message.Header.Get("X-Event-Id"), published.ID)
	}
	if message.Header.Get("X-Original-Subject") != subject {
		t.Fatalf("dead-letter original subject = %q, want %q", message.Header.Get("X-Original-Subject"), subject)
	}
	if message.Header.Get("X-Delivery-Count") != "3" {
		t.Fatalf("delivery count header = %q, want 3", message.Header.Get("X-Delivery-Count"))
	}
	if message.Header.Get("X-Failure-Reason") == "" {
		t.Fatal("failure reason header must not be empty")
	}
	stats := consumer.Stats()
	if stats.Processed != 0 || stats.Retried != 2 || stats.DeadLettered != 1 {
		t.Fatalf("stats = %+v, want 2 retried / 1 dead-lettered", stats)
	}
}
