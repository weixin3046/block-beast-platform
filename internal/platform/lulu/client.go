// Package lulu implements the legacy Lulu encrypted protocol without importing
// any credentials, database, or runtime state from the reference project.
package lulu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	app "github.com/block-beast/platform/internal/application/lulu"
)

var ErrProvider = errors.New("Lulu provider unavailable or response invalid")

type Client struct {
	token          string
	base, receiver string
	enc, mac       []byte
	http           *http.Client
}

func newClient(base, receiver, sharedBase64 string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !app.ValidUID(receiver) {
		return nil, ErrProvider
	}
	shared, err := base64.StdEncoding.DecodeString(sharedBase64)
	if err != nil || len(shared) < 32 {
		return nil, ErrProvider
	}
	derive := func(label string) []byte {
		h := hmac.New(sha256.New, shared)
		h.Write([]byte(label))
		return h.Sum(nil)
	}
	return &Client{base: strings.TrimRight(base, "/"), receiver: receiver, enc: derive("simplecrypt/v1 enc"), mac: derive("simplecrypt/v1 mac"), http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// NewCredentialClient uses a decrypted in-memory token; it never writes a token file.
func NewCredentialClient(base, receiver, token, shared string) (*Client, error) {
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, ErrProvider
	}
	c, e := newClient(base, receiver, shared)
	if e != nil {
		return nil, e
	}
	c.token = token
	return c, nil
}
func (c *Client) encrypt(plain []byte) ([]byte, error) {
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(bytes.Clone(plain), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, 17+len(plain))
	out[0] = 1
	if _, err := rand.Read(out[1:17]); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(c.enc)
	if err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(block, out[1:17]).CryptBlocks(out[17:], plain)
	h := hmac.New(sha256.New, c.mac)
	h.Write(out)
	out = append(out, h.Sum(nil)...)
	return []byte(base64.StdEncoding.EncodeToString(out)), nil
}
func (c *Client) decrypt(data []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(data)))
	if err != nil || len(raw) < 65 || raw[0] != 1 || (len(raw)-49)%16 != 0 {
		return nil, ErrProvider
	}
	h := hmac.New(sha256.New, c.mac)
	h.Write(raw[:len(raw)-32])
	if !hmac.Equal(h.Sum(nil), raw[len(raw)-32:]) {
		return nil, ErrProvider
	}
	block, err := aes.NewCipher(c.enc)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(raw)-49)
	cipher.NewCBCDecrypter(block, raw[1:17]).CryptBlocks(plain, raw[17:len(raw)-32])
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > 16 || !bytes.Equal(plain[len(plain)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, ErrProvider
	}
	return plain[:len(plain)-pad], nil
}
func (c *Client) call(ctx context.Context, method, path string, payload any) (map[string]any, error) {
	// The UID is pinned; replacing a token for another collector account must fail closed.
	var err error
	token := c.token
	loginRequest := path == "/account/send_phone_code" || path == "/account/phone_login"
	if loginRequest {
		token = ""
	}
	if token == "" && !loginRequest {
		return nil, ErrProvider
	}
	var body []byte
	if payload != nil {
		body, err = json.Marshal(payload)
		if err == nil {
			body, err = c.encrypt(body)
		}
		if err != nil {
			return nil, ErrProvider
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrProvider
	}
	if !loginRequest {
		req.Header.Set("token", token)
	}
	req.Header.Set("User-Agent", "BestHTTP")
	req.Header.Set("Accept-Encoding", "identity")
	if payload != nil {
		req.Header.Set("Content-Type", "text/plain")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ErrProvider
	}
	defer resp.Body.Close()
	if !loginRequest && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		return nil, app.ErrTokenInvalid
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrProvider
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return nil, ErrProvider
	}
	if !json.Valid(data) {
		data, err = c.decrypt(data)
		if err != nil {
			return nil, ErrProvider
		}
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&out) != nil || out == nil {
		return nil, ErrProvider
	}
	return out, nil
}
func str(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case json.Number:
		return string(n)
	}
	return ""
}
func integer(v any) (int64, error)  { return strconv.ParseInt(str(v), 10, 64) }
func success(v map[string]any) bool { return str(v["code"]) == "0" }

func (c *Client) Transfer(ctx context.Context, o app.Order) app.TransferResult {
	if o.ReceiverUID != c.receiver || !app.ValidUID(o.LuluUID) {
		return app.TransferFailed
	}
	amount, err := app.ParseAmount(o.Amount)
	if err != nil {
		return app.TransferFailed
	}
	// Perform recipient lookup before sending. Any uncertain transfer response is held for review.
	obj, err := c.call(ctx, "GET", "/account/social?user_id="+url.QueryEscape(o.LuluUID), nil)
	if err != nil || !success(obj) || obj["data"] == nil {
		return app.TransferFailed
	}
	obj, err = c.call(ctx, "POST", "/player/transfer", map[string]any{"toUserId": json.Number(o.LuluUID), "item_id": 102201, "item_num": amount})
	if err == nil && success(obj) {
		return app.TransferConfirmed
	}
	// No documented failure-code guarantee or upstream idempotency key exists in the reference.
	return app.TransferUnknown
}

func (c *Client) Receipts(ctx context.Context, since time.Time, save func(app.Receipt) error) error {
	seenPages := map[string]bool{}
	for page := 1; page <= 1000; page++ {
		obj, err := c.call(ctx, "GET", fmt.Sprintf("/player/transfer/log?page=%d&size=100&type=0", page), nil)
		if err != nil || !success(obj) {
			return ErrProvider
		}
		data, ok := obj["data"].(map[string]any)
		if !ok {
			return ErrProvider
		}
		list, ok := data["list"].([]any)
		if !ok {
			return ErrProvider
		}
		if len(list) == 0 {
			return nil
		}
		encoded, _ := json.Marshal(list)
		hash := sha256.Sum256(encoded)
		pageKey := hex.EncodeToString(hash[:])
		if seenPages[pageKey] {
			return ErrProvider
		}
		seenPages[pageKey] = true
		allOlder := true
		for _, v := range list {
			row, ok := v.(map[string]any)
			if !ok {
				return ErrProvider
			}
			ms, err := integer(row["time"])
			if err != nil || ms <= 0 {
				return ErrProvider
			}
			at := time.UnixMilli(ms).UTC()
			if at.Before(since) {
				continue
			}
			allOlder = false
			item, err := integer(row["item_id"])
			if err != nil {
				return ErrProvider
			}
			if item != 102201 {
				continue
			}
			amount, err := integer(row["item_num"])
			if err != nil {
				return ErrProvider
			}
			if amount <= 0 {
				continue
			}
			sender := str(row["user_id"])
			if !app.ValidUID(sender) {
				return ErrProvider
			}
			if sender == c.receiver {
				continue
			}
			// The reference has no confirmed provider transaction ID. This composite
			// conservatively deduplicates indistinguishable observations; collisions
			// require manual reconciliation and must never create an extra credit.
			// Exclude mutable nicknames; include collector account to isolate token/account rotation.
			identity := fmt.Sprintf("%s|%s|%d|%d|102201", c.receiver, sender, ms, amount)
			sum := sha256.Sum256([]byte(identity))
			receipt := app.Receipt{ID: "observed:" + hex.EncodeToString(sum[:]), ReceiverUID: c.receiver, SenderUID: sender, Amount: amount, OccurredAt: at}
			if err = save(receipt); err != nil {
				return err
			}
		}
		if allOlder || len(list) < 100 {
			return nil
		}
	}
	return ErrProvider // Do not advance the watermark when the history was truncated.
}

func NewLoginClient(base, key string) (*Client, error) { return newClient(base, "100", key) }
func (c *Client) SendCode(ctx context.Context, phone string) error {
	o, e := c.call(ctx, "POST", "/account/send_phone_code", map[string]any{"tel": phone})
	if e != nil || !success(o) {
		return ErrProvider
	}
	return nil
}
func (c *Client) PhoneLogin(ctx context.Context, phone, code string) (string, string, error) {
	o, e := c.call(ctx, "POST", "/account/phone_login", map[string]any{"tel": phone, "code": code, "invite_code": 0})
	if e != nil || !success(o) {
		return "", "", ErrProvider
	}
	var find func(any, []string) string
	find = func(v any, keys []string) string {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range keys {
				if value := str(x[k]); value != "" {
					return value
				}
			}
			for _, value := range x {
				if found := find(value, keys); found != "" {
					return found
				}
			}
		case []any:
			for _, value := range x {
				if found := find(value, keys); found != "" {
					return found
				}
			}
		}
		return ""
	}
	token := find(o, []string{"token", "access_token", "accessToken", "Authorization"})
	uid := find(o, []string{"user_id", "uid", "userId"})
	if !app.ValidUID(uid) || token == "" || strings.ContainsAny(token, "\r\n") || len(token) > 16384 {
		return "", "", ErrProvider
	}
	return uid, token, nil
}
