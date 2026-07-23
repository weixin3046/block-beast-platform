package pqpa

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client implements the PQPA HTTPS API transport. Business services should
// depend on this small interface rather than on provider-specific payloads.
type Client struct {
	baseURL string
	apiKey  string
	secret  []byte
	http    *http.Client
	clock   func() time.Time
	nonce   func() string
}

func NewClient(baseURL, apiKey, secret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		secret:  []byte(secret),
		http:    httpClient,
		clock:   time.Now,
		nonce:   randomNonce,
	}
}

type APIError struct {
	Code    int
	Message string
}

func (err *APIError) Error() string {
	return fmt.Sprintf("PQPA returned code %d: %s", err.Code, err.Message)
}

func randomNonce() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(value[:])
}

// Signature follows PQPA's four-header HMAC-SHA256 convention.
func (client *Client) Signature(method, path, timestamp, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	message := strings.Join([]string{strings.ToUpper(method), path, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	mac := hmac.New(sha256.New, client.secret)
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func (client *Client) DoJSON(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	var body []byte
	var err error
	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal PQPA request: %w", err)
		}
	}
	timestamp := strconv.FormatInt(client.clock().UTC().UnixMilli(), 10)
	nonce := client.nonce()
	req, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PQPA request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", client.apiKey)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", client.Signature(method, path, timestamp, nonce, body))
	resp, err := client.http.Do(req)
	if err != nil {
		return fmt.Errorf("call PQPA: %w", err)
	}
	defer resp.Body.Close()
	response, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("PQPA returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(response)))
	}
	if responseBody != nil && len(response) != 0 {
		var envelope struct {
			Code int             `json:"code"`
			Msg  string          `json:"msg"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil {
			return fmt.Errorf("decode PQPA response: %w", err)
		}
		if envelope.Code != 0 {
			return &APIError{Code: envelope.Code, Message: envelope.Msg}
		}
		if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
			if err := json.Unmarshal(envelope.Data, responseBody); err != nil {
				return fmt.Errorf("decode PQPA response data: %w", err)
			}
		}
	}
	return nil
}
