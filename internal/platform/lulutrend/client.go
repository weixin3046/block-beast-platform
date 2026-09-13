// Package lulutrend reads the authorized, read-only Lulu history endpoint.
package lulutrend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRecords = 60
const authorizedHost = "159.75.13.86:9082"

type Record struct {
	Round    string
	ClosedAt time.Time
	Result   []string
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "http" || parsed.Host != authorizedHost || parsed.Path != "/api/trend" {
		return nil, fmt.Errorf("invalid Lulu trend URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: parsed.String(), http: client}, nil
}

func (client *Client) History(ctx context.Context, game string) ([]Record, error) {
	if client == nil || (game != "lh" && game != "xdy" && game != "race") {
		return nil, fmt.Errorf("invalid Lulu trend game")
	}
	endpoint, err := url.Parse(client.baseURL)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("game", game)
	query.Set("limit", strconv.Itoa(maxRecords))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Lulu trend: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Lulu trend: unexpected status %d", response.StatusCode)
	}
	var payload struct {
		OK   bool   `json:"ok"`
		Game string `json:"game"`
		List []struct {
			Issue  int64  `json:"i"`
			At     string `json:"t"`
			Result []int  `json:"k"`
		} `json:"list"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Lulu trend: %w", err)
	}
	if !payload.OK || payload.Game != game {
		return nil, fmt.Errorf("invalid Lulu trend response")
	}
	records := make([]Record, 0, len(payload.List))
	for _, item := range payload.List {
		closedAt, err := time.Parse(time.RFC3339Nano, item.At)
		if err != nil || item.Issue <= 0 || len(item.Result) == 0 {
			return nil, fmt.Errorf("invalid Lulu trend record")
		}
		result := make([]string, len(item.Result))
		for index, room := range item.Result {
			if room < 1 || room > 8 {
				return nil, fmt.Errorf("invalid Lulu trend result")
			}
			result[index] = strconv.Itoa(room)
		}
		records = append(records, Record{Round: strconv.FormatInt(item.Issue, 10), ClosedAt: closedAt.UTC(), Result: result})
	}
	return records, nil
}
