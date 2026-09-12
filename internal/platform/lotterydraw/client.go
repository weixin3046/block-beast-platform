// Package lotterydraw reads and validates encrypted draw histories from the
// external provider. It contains no settlement or persistence behavior.
package lotterydraw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	GameStarSea       = "star_sea"
	GameAngryFeather  = "angry_feather"
	maximumResponse   = 1 << 20
	providerSecretKey = "272fe2cc435c5a73a4a1ac24e91c9213"
)

var (
	ErrInvalidBaseURL      = errors.New("invalid lottery draw upstream URL")
	ErrUnknownGame         = errors.New("unknown lottery draw game")
	ErrInvalidRequest      = errors.New("invalid lottery draw request")
	ErrProviderUnavailable = errors.New("lottery draw provider unavailable")
	ErrInvalidProviderData = errors.New("invalid lottery draw provider data")
)

type Record struct {
	Issue string `json:"issue"`
	Room  []int  `json:"room"`
}

type Reader interface {
	History(ctx context.Context, game string, count int) ([]Record, error)
}

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func New(baseURL string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, ErrInvalidBaseURL
	}
	return &Client{
		baseURL: parsed,
		http: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (client *Client) History(ctx context.Context, game string, count int) ([]Record, error) {
	path, maximumRoom, ok := gameEndpoint(game)
	if !ok {
		return nil, ErrUnknownGame
	}
	if count < 1 {
		return nil, ErrInvalidRequest
	}
	endpoint := *client.baseURL
	endpoint.Path = path
	endpoint.RawPath = ""
	endpoint.RawQuery = url.Values{"count": []string{strconv.Itoa(count)}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create draw history request: %w", err)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrProviderUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumResponse+1))
	if err != nil || len(body) > maximumResponse {
		return nil, ErrInvalidProviderData
	}
	var envelope struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Code != 0 || envelope.Data == "" {
		return nil, ErrInvalidProviderData
	}
	var records []Record
	if err := json.Unmarshal([]byte(decrypt(envelope.Data)), &records); err != nil || !validRecords(records, maximumRoom) {
		return nil, ErrInvalidProviderData
	}
	return records, nil
}

func gameEndpoint(game string) (string, int, bool) {
	switch game {
	case GameStarSea:
		return "/api/lottery/history", 8, true
	case GameAngryFeather:
		return "/api/crow/history", 2, true
	default:
		return "", 0, false
	}
}

func decrypt(value string) string {
	output := make([]byte, len(value))
	for index := range value {
		output[index] = value[index] ^ providerSecretKey[index%len(providerSecretKey)]
	}
	return string(output)
}

func validRecords(records []Record, maximumRoom int) bool {
	for _, record := range records {
		if strings.TrimSpace(record.Issue) == "" || len(record.Room) == 0 {
			return false
		}
		for _, room := range record.Room {
			if room < 1 || room > maximumRoom {
				return false
			}
		}
	}
	return true
}
