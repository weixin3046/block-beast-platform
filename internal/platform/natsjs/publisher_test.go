package natsjs

import (
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
)

func TestPublisherStoresEventInJetStream(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("NATS_TEST_URL is not set")
	}

	publisher, err := Connect(url)
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	t.Cleanup(publisher.Close)

	before, err := publisher.jetStream.StreamInfo(streamName)
	if err != nil {
		t.Fatalf("read stream state before publish: %v", err)
	}
	for _, required := range eventStreamSubjects {
		found := false
		for _, subject := range before.Config.Subjects {
			if subject == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stream subjects = %v, missing %q", before.Config.Subjects, required)
		}
	}
	event := events.Event{
		ID:            uuid.NewString(),
		Type:          events.BetPlaced,
		AggregateType: "bet",
		AggregateID:   uuid.NewString(),
		OccurredAt:    time.Now().UTC(),
		Payload:       []byte(`{"bet_id":"test"}`),
	}
	if err := publisher.Publish(event); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	after, err := publisher.jetStream.StreamInfo(streamName)
	if err != nil {
		t.Fatalf("read stream state after publish: %v", err)
	}
	if after.State.Msgs != before.State.Msgs+1 {
		t.Fatalf("stream messages = %d, want %d", after.State.Msgs, before.State.Msgs+1)
	}

	message, err := publisher.jetStream.GetMsg(streamName, after.State.LastSeq)
	if err != nil {
		t.Fatalf("read published message: %v", err)
	}
	if message.Subject != event.Type {
		t.Fatalf("message subject = %q, want %q", message.Subject, event.Type)
	}
	if string(message.Data) != string(event.Payload) {
		t.Fatalf("message payload = %q, want %q", message.Data, event.Payload)
	}
}

func TestMergeSubjectsPreservesExistingAndAddsMissing(t *testing.T) {
	got, changed := mergeSubjects([]string{"game.>", "custom.>"}, eventStreamSubjects)
	if !changed {
		t.Fatal("merge should report a change")
	}
	want := []string{"game.>", "custom.>", "wallet.>", "chain.>", "chat.>"}
	if len(got) != len(want) {
		t.Fatalf("subjects = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("subjects = %v, want %v", got, want)
		}
	}

	unchanged, changed := mergeSubjects(got, eventStreamSubjects)
	if changed || len(unchanged) != len(got) {
		t.Fatalf("second merge = %v, changed %v; want unchanged", unchanged, changed)
	}
}
