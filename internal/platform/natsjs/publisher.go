package natsjs

import (
	"errors"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/nats-io/nats.go"
)

const streamName = "BLOCK_BEAST_EVENTS"

type Publisher struct {
	connection *nats.Conn
	jetStream  nats.JetStreamContext
}

func Connect(url string) (*Publisher, error) {
	connection, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}
	jetStream, err := connection.JetStream()
	if err != nil {
		connection.Close()
		return nil, err
	}
	if _, err := jetStream.StreamInfo(streamName); errors.Is(err, nats.ErrStreamNotFound) {
		_, err = jetStream.AddStream(&nats.StreamConfig{
			Name:     streamName,
			Subjects: []string{"game.>", "wallet.>", "chain.>"},
		})
	}
	if err != nil {
		connection.Close()
		return nil, err
	}
	return &Publisher{connection: connection, jetStream: jetStream}, nil
}

func (publisher *Publisher) Publish(event events.Event) error {
	_, err := publisher.jetStream.Publish(event.Type, event.Payload, nats.MsgId(event.ID))
	return err
}

func (publisher *Publisher) Close() {
	publisher.connection.Close()
}

var _ events.Publisher = (*Publisher)(nil)
