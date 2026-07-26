package realtime

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const ProtocolVersion = 1

var errInvalidCommand = errors.New("invalid realtime command")

type clientCommand struct {
	Version   int      `json:"v"`
	Type      string   `json:"type"`
	Topics    []string `json:"topics,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

type serverMessage struct {
	Version    int             `json:"v"`
	Type       string          `json:"type"`
	RequestID  string          `json:"request_id,omitempty"`
	Subject    string          `json:"subject,omitempty"`
	Topics     []string        `json:"topics,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      string          `json:"error,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}

func decodeCommand(payload []byte) (clientCommand, error) {
	var command clientCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return clientCommand{}, errInvalidCommand
	}
	if command.Version != ProtocolVersion || (command.Type != "subscribe" && command.Type != "unsubscribe" && command.Type != "ping") {
		return clientCommand{}, errInvalidCommand
	}
	if command.Type != "ping" && len(command.Topics) == 0 {
		return clientCommand{}, errInvalidCommand
	}
	for _, topic := range command.Topics {
		if !validTopic(topic) {
			return clientCommand{}, errInvalidCommand
		}
	}
	return command, nil
}

func validTopic(topic string) bool {
	if topic == "game" || topic == "chat" {
		return true
	}
	if !strings.HasPrefix(topic, "round:") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(topic, "round:"))
	return err == nil
}

func encodeMessage(message serverMessage) []byte {
	message.Version = ProtocolVersion
	message.OccurredAt = time.Now().UTC()
	payload, _ := json.Marshal(message)
	return payload
}

func eventTopics(subject string, data []byte) []string {
	if strings.HasPrefix(subject, "chat.") {
		return []string{"chat"}
	}
	if !strings.HasPrefix(subject, "game.") {
		return nil
	}
	topics := []string{"game"}
	var payload struct {
		RoundID string `json:"round_id"`
	}
	if json.Unmarshal(data, &payload) == nil {
		if id, err := uuid.Parse(payload.RoundID); err == nil {
			topics = append(topics, "round:"+id.String())
		}
	}
	return topics
}
