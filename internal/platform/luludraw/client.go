// Package luludraw normalizes Lulu draw messages without retaining player data.
package luludraw

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	app "github.com/block-beast/platform/internal/application/lulu"
	"github.com/coder/websocket"
)

type Event struct {
	Game    string
	Kind    string
	Round   string
	CloseAt *time.Time
	Result  []string
}

var ErrConfig = errors.New("invalid lulu draw configuration")

type Client struct {
	token, uid string
	enc, mac   []byte
	games      []string
}

func NewClient(token, uid, protocolKey string, games []string) (*Client, error) {
	if strings.TrimSpace(token) == "" || !app.ValidUID(uid) || !app.ValidProtocolKey(protocolKey) || len(games) == 0 {
		return nil, ErrConfig
	}
	shared, err := base64.StdEncoding.DecodeString(protocolKey)
	if err != nil {
		return nil, ErrConfig
	}
	seen := make(map[string]struct{}, len(games))
	selected := make([]string, 0, len(games))
	for _, game := range games {
		if !validGame(game) {
			return nil, ErrConfig
		}
		if _, ok := seen[game]; !ok {
			seen[game] = struct{}{}
			selected = append(selected, game)
		}
	}
	derive := func(label string) []byte {
		h := hmac.New(sha256.New, shared)
		_, _ = h.Write([]byte(label))
		return h.Sum(nil)
	}
	return &Client{token: token, uid: uid, enc: derive("simplecrypt/v1 enc"), mac: derive("simplecrypt/v1 mac"), games: selected}, nil
}

func (client *Client) Run(ctx context.Context, handle func(Event)) error {
	if client == nil || handle == nil {
		return ErrConfig
	}
	var group sync.WaitGroup
	for _, game := range client.games {
		group.Add(1)
		go func(game string) { defer group.Done(); client.runGame(ctx, game, handle) }(game)
	}
	<-ctx.Done()
	group.Wait()
	return nil
}

func (client *Client) runGame(ctx context.Context, game string, handle func(Event)) {
	backoff := time.Second
	for ctx.Err() == nil {
		u := url.URL{Scheme: "wss", Host: game + ".lululu.com.cn", Path: "/ws"}
		q := u.Query()
		q.Set("token", client.token)
		q.Set("userid", client.uid)
		u.RawQuery = q.Encode()
		connection, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{})
		if err == nil {
			backoff = time.Second
			for ctx.Err() == nil {
				_, message, readErr := connection.Read(ctx)
				if readErr != nil {
					break
				}
				for _, plain := range client.decryptFrame(message) {
					if event, ok := parseMessage(game, plain); ok {
						handle(event)
					}
				}
			}
			_ = connection.Close(websocket.StatusNormalClosure, "worker stopped")
		}
		if !wait(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 20*time.Second)
	}
}

func (client *Client) decryptFrame(message []byte) [][]byte {
	parts := bytes.Split(message, []byte("&"))
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(part)))
		if err != nil || len(raw) < 65 || len(raw) > 1<<20 || raw[0] != 1 || (len(raw)-49)%aes.BlockSize != 0 {
			continue
		}
		h := hmac.New(sha256.New, client.mac)
		_, _ = h.Write(raw[:len(raw)-32])
		if !hmac.Equal(h.Sum(nil), raw[len(raw)-32:]) {
			continue
		}
		block, err := aes.NewCipher(client.enc)
		if err != nil {
			continue
		}
		plain := make([]byte, len(raw)-49)
		cipher.NewCBCDecrypter(block, raw[1:17]).CryptBlocks(plain, raw[17:len(raw)-32])
		padding := int(plain[len(plain)-1])
		if padding < 1 || padding > aes.BlockSize || !bytes.Equal(plain[len(plain)-padding:], bytes.Repeat([]byte{byte(padding)}, padding)) {
			continue
		}
		out = append(out, plain[:len(plain)-padding])
	}
	return out
}

func validGame(game string) bool { return game == "lh" || game == "xdy" || game == "race" }

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type envelope struct {
	Event string          `json:"e"`
	Data  json.RawMessage `json:"d"`
}

func parseMessage(game string, raw []byte) (Event, bool) {
	var message envelope
	if json.Unmarshal(raw, &message) != nil {
		return Event{}, false
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(message.Data, &data) != nil {
		return Event{}, false
	}
	round := rawRound(data)
	event := Event{Game: game, Kind: message.Event, Round: round}
	if (message.Event == "3001" || message.Event == "3002" || message.Event == "3006") && round != "" {
		if closeAt, ok := closeTime(data); ok {
			event.CloseAt = &closeAt
		}
	}
	switch game {
	case "lh":
		if message.Event == "3005" {
			if value, ok := rawInt(data, "win_item_id", "winItemId", "winner_camp"); ok && (value == 1 || value == 2) && round != "" {
				event.Result = []string{strconv.FormatInt(value, 10)}
				return event, true
			}
		}
	case "xdy":
		if message.Event == "3004" || message.Event == "3005" {
			if value, ok := rawInt(data, "failedRoomId"); ok && value >= 1 && value <= 8 && round != "" {
				event.Result = []string{strconv.FormatInt(value, 10)}
				return event, true
			}
			if value, ok := data["killedRooms"]; ok && round != "" {
				var rooms []int64
				if json.Unmarshal(value, &rooms) == nil {
					for _, room := range rooms {
						if room >= 1 && room <= 8 {
							event.Result = append(event.Result, strconv.FormatInt(room, 10))
						}
					}
					if len(event.Result) > 0 {
						return event, true
					}
				}
			}
		}
	case "race":
		if (message.Event == "3005" || message.Event == "3006") && round != "" {
			var ranks []struct {
				ItemID int64 `json:"item_id"`
				Rank   int64 `json:"rank"`
			}
			if value, ok := data["race_rank_info"]; ok && json.Unmarshal(value, &ranks) == nil {
				for _, rank := range ranks {
					if rank.Rank == 1 && rank.ItemID >= 1 && rank.ItemID <= 6 {
						event.Result = []string{strconv.FormatInt(rank.ItemID, 10)}
						return event, true
					}
				}
			}
		}
	}
	if event.CloseAt != nil {
		return event, true
	}
	return Event{}, false
}

func closeTime(data map[string]json.RawMessage) (time.Time, bool) {
	if value, ok := rawInt(data, "countdownEndTime", "countdown_end_time"); ok && value > 0 {
		return time.UnixMilli(value).UTC(), true
	}
	if seconds, ok := rawInt(data, "countdown"); ok && seconds > 0 {
		return time.Now().UTC().Add(time.Duration(seconds) * time.Second), true
	}
	return time.Time{}, false
}

func rawRound(data map[string]json.RawMessage) string {
	value, ok := rawInt(data, "round_id", "roundId")
	if !ok || value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func rawInt(data map[string]json.RawMessage, names ...string) (int64, bool) {
	for _, name := range names {
		value, ok := data[name]
		if !ok {
			continue
		}
		var number int64
		if json.Unmarshal(value, &number) == nil {
			return number, true
		}
		var text string
		if json.Unmarshal(value, &text) == nil {
			if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}
