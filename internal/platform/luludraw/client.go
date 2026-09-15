// Package luludraw normalizes Lulu draw messages without retaining player data.
package luludraw

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
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
	Game        string
	Kind        string
	ResultField string
	Round       string
	CloseAt     *time.Time
	Result      []string
}

var ErrConfig = errors.New("invalid lulu draw configuration")

// luluReadLimit is intentionally bounded, but large enough for the upstream
// xdy snapshot frames that exceed the websocket library's 32KiB default.
const luluReadLimit = 1 << 20

type Client struct {
	token, uid string
	enc, mac   []byte
	games      []string
	onError    func(game, operation string, err error)
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

// WithErrorHandler exposes transport failures to the worker logger without
// including credentials or decrypted upstream payloads.
func (client *Client) WithErrorHandler(handler func(game, operation string, err error)) *Client {
	if client != nil {
		client.onError = handler
	}
	return client
}

func (client *Client) reportError(game, operation string, err error) {
	if err != nil && client != nil && client.onError != nil {
		client.onError(game, operation, err)
	}
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
			connection.SetReadLimit(luluReadLimit)
			backoff = time.Second
			client.subscribe(ctx, connection, game)
			pollCtx, stopPoll := context.WithCancel(ctx)
			go client.poll(pollCtx, connection, game)
			go client.heartbeat(pollCtx, connection)
			lastRound := ""
			for ctx.Err() == nil {
				_, message, readErr := connection.Read(ctx)
				if readErr != nil {
					if ctx.Err() == nil {
						client.reportError(game, "read", readErr)
					}
					break
				}
				for _, plain := range client.decryptFrame(message) {
					if game == "lh" {
						var frame envelope
						if json.Unmarshal(plain, &frame) == nil && frame.Event == "3002" {
							var data map[string]json.RawMessage
							if json.Unmarshal(frame.Data, &data) == nil {
								if round := rawRound(data); round != "" {
									lastRound = round
									id, _ := strconv.ParseInt(round, 10, 64)
									client.reportError(game, "snapshot", client.writeEvent(ctx, connection, "2001", map[string]int64{"round_id": id}))
								}
							}
						}
					}
					for _, event := range parseMessagesWithRound(game, plain, lastRound) {
						if event.Round != "" && event.Kind != "2007" && event.Kind != "2011" {
							lastRound = event.Round
						}
						handle(event)
					}
				}
			}
			stopPoll()
			_ = connection.Close(websocket.StatusNormalClosure, "worker stopped")
		} else if ctx.Err() == nil {
			client.reportError(game, "connect", err)
		}
		if !wait(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 20*time.Second)
	}
}

func (client *Client) subscribe(ctx context.Context, connection *websocket.Conn, game string) {
	if game == "xdy" {
		for _, event := range []string{"2001", "2007", "2006"} {
			if err := client.writeEvent(ctx, connection, event, nil); err != nil {
				client.reportError(game, "subscribe "+event, err)
			}
		}
		return
	}
	if game == "race" {
		if err := client.writeEvent(ctx, connection, "2001", map[string]int{"round_id": 0}); err != nil {
			client.reportError(game, "subscribe 2001", err)
		}
		return
	}
	if game == "lh" {
		client.reportError(game, "subscribe 2013", client.writeEvent(ctx, connection, "2013", nil))
		client.reportError(game, "subscribe 2001", client.writeEvent(ctx, connection, "2001", map[string]int{"round_id": 0}))
		if err := client.writeEvent(ctx, connection, "2011", map[string]int{"round_id": 0}); err != nil {
			client.reportError(game, "subscribe 2011", err)
		}
	}
}

func (client *Client) heartbeat(ctx context.Context, connection *websocket.Conn) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := connection.Ping(pingCtx)
			cancel()
			if err != nil {
				_ = connection.CloseNow()
				return
			}
		}
	}
}

func (client *Client) poll(ctx context.Context, connection *websocket.Conn, game string) {
	var interval time.Duration
	switch game {
	case "xdy":
		interval = time.Second
	case "lh":
		interval = 30 * time.Second
	default:
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	count := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count++
			if game == "xdy" {
				if err := client.writeEvent(ctx, connection, "2001", nil); err != nil {
					client.reportError(game, "poll 2001", err)
				}
				if count%15 == 0 {
					if err := client.writeEvent(ctx, connection, "2007", nil); err != nil {
						client.reportError(game, "poll 2007", err)
					}
				}
			} else {
				if err := client.writeEvent(ctx, connection, "2011", map[string]int{"round_id": 0}); err != nil {
					client.reportError(game, "poll 2011", err)
				}
			}
		}
	}
}

func (client *Client) writeEvent(ctx context.Context, connection *websocket.Conn, event string, data any) error {
	if data == nil {
		data = map[string]any{}
	}
	plain, err := json.Marshal(map[string]any{"e": event, "d": data})
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, []byte(client.encryptFrame(plain)))
}

func (client *Client) encryptFrame(plain []byte) string {
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	iv := make([]byte, aes.BlockSize)
	_, _ = rand.Read(iv)
	block, _ := aes.NewCipher(client.enc)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	raw := append(append([]byte{1}, iv...), ciphertext...)
	h := hmac.New(sha256.New, client.mac)
	_, _ = h.Write(raw)
	return base64.StdEncoding.EncodeToString(append(raw, h.Sum(nil)...))
}

func (client *Client) decryptFrame(message []byte) [][]byte {
	parts := bytes.Split(message, []byte("&"))
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		encoded := string(bytes.TrimSpace(part))
		switch len(encoded) % 4 {
		case 2:
			encoded += "=="
		case 3:
			encoded += "="
		case 1:
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
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
	return parseMessageWithRound(game, raw, "")
}

func parseMessagesWithRound(game string, raw []byte, fallbackRound string) []Event {
	if game == "lh" {
		var frame envelope
		if json.Unmarshal(raw, &frame) == nil && frame.Event == "2011" {
			var data struct {
				Rounds []map[string]json.RawMessage `json:"rounds"`
			}
			if json.Unmarshal(frame.Data, &data) != nil {
				return nil
			}
			var events []Event
			for _, row := range data.Rounds {
				round := rawRound(row)
				winner, ok := rawInt(row, "win_item_id")
				if round != "" && ok && (winner == 1 || winner == 2) {
					events = append(events, Event{Game: game, Kind: "2011", ResultField: "rounds[].win_item_id", Round: round, Result: []string{strconv.FormatInt(winner, 10)}})
				}
			}
			return events
		}
	}
	if game != "xdy" {
		if event, ok := parseMessageWithRound(game, raw, fallbackRound); ok {
			return []Event{event}
		}
		return nil
	}
	var message envelope
	if json.Unmarshal(raw, &message) != nil || message.Event != "2007" {
		if event, ok := parseMessageWithRound(game, raw, fallbackRound); ok {
			return []Event{event}
		}
		return nil
	}
	var data struct {
		Result struct {
			List []struct {
				Round int64   `json:"round"`
				Fail  []int64 `json:"fail"`
			} `json:"list"`
		} `json:"result"`
	}
	if json.Unmarshal(message.Data, &data) != nil {
		return nil
	}
	events := make([]Event, 0, len(data.Result.List))
	for _, item := range data.Result.List {
		if item.Round < 1 {
			continue
		}
		event := Event{Game: game, Kind: message.Event, ResultField: "result.list[].fail", Round: strconv.FormatInt(item.Round, 10)}
		for _, room := range item.Fail {
			if room >= 1 && room <= 8 {
				event.Result = append(event.Result, strconv.FormatInt(room, 10))
			}
		}
		if len(event.Result) > 0 {
			events = append(events, event)
		}
	}
	return events
}

// parseMessageWithRound uses the most recently observed round when an official
// result frame omits its own round_id. The cache is scoped to one game socket.
func parseMessageWithRound(game string, raw []byte, fallbackRound string) (Event, bool) {
	var message envelope
	if json.Unmarshal(raw, &message) != nil {
		return Event{}, false
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(message.Data, &data) != nil {
		return Event{}, false
	}
	if game == "lh" && message.Event == "2001" {
		var snapshot map[string]json.RawMessage
		if json.Unmarshal(data["round"], &snapshot) == nil {
			data = snapshot
		}
	}
	if game == "xdy" && message.Event == "2001" {
		var snapshot map[string]json.RawMessage
		if value, ok := data["result"]; ok && json.Unmarshal(value, &snapshot) == nil {
			data = snapshot
		}
	}
	round := rawRound(data)
	if round == "" {
		round = fallbackRound
	}
	event := Event{Game: game, Kind: message.Event, Round: round}
	if (message.Event == "2001" || message.Event == "3001" || message.Event == "3002" || message.Event == "3006") && round != "" {
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
		if message.Event == "3001" || message.Event == "3004" || message.Event == "3005" {
			if value, ok := rawInt(data, "failedRoomId"); ok && value >= 1 && value <= 8 && round != "" {
				event.ResultField = "failedRoomId"
				event.Result = []string{strconv.FormatInt(value, 10)}
				return event, true
			}
			if value, ok := data["killedRooms"]; ok && round != "" {
				event.ResultField = "killedRooms"
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
	var end string
	if json.Unmarshal(data["end_time"], &end) == nil {
		if at, err := time.Parse(time.RFC3339Nano, end); err == nil {
			return at.UTC(), true
		}
		for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
			if at, err := time.ParseInLocation(layout, end, time.FixedZone("Asia/Shanghai", 8*3600)); err == nil {
				return at.UTC(), true
			}
		}
	}
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
