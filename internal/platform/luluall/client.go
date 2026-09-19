// Package luluall reads the separately deployed collector's bounded history API.
package luluall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/platform/luludraw"
)

var ErrConfig = errors.New("LuluAll base URL must be an HTTP(S) URL without credentials, query or fragment")
var ErrResponse = errors.New("invalid LuluAll history response")

type Client struct {
	base string
	http *http.Client
}

func NewClient(base string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, ErrConfig
	}
	return &Client{base: strings.TrimRight(u.String(), "/"), http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) History(ctx context.Context, game string) ([]luludraw.Event, error) {
	remote := map[string]string{"lh": "cock", "xdy": "steal", "race": "race"}[game]
	if remote == "" {
		return nil, errors.New("unsupported LuluAll game")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/games/"+remote+"/history?limit=500", nil)
	if err != nil {
		return nil, ErrConfig
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("LuluAll history request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LuluAll history HTTP status %d", response.StatusCode)
	}
	const maxBody = 2 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, ErrResponse
	}
	var payload struct {
		Code *int `json:"code"`
		Data struct {
			Game    string `json:"game"`
			History []struct {
				RoundID int64 `json:"roundId"`
				Winner  int   `json:"winner"`
				Items   []int `json:"items"`
			} `json:"history"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Code == nil || *payload.Code != 0 || payload.Data.Game != remote || payload.Data.History == nil || len(payload.Data.History) > 500 {
		return nil, ErrResponse
	}
	events := make([]luludraw.Event, 0, len(payload.Data.History))
	seen := map[int64]string{}
	for _, record := range payload.Data.History {
		if record.RoundID <= 0 {
			return nil, ErrResponse
		}
		values := []int{record.Winner}
		max, count := 6, 1
		if game == "lh" {
			max = 2
		}
		if game == "xdy" {
			values = record.Items
			max, count = 8, 7
		}
		if len(values) == 0 || len(values) > count {
			return nil, ErrResponse
		}
		sort.Ints(values)
		result := make([]string, len(values))
		for i, value := range values {
			if value < 1 || value > max || (i > 0 && value == values[i-1]) {
				return nil, ErrResponse
			}
			result[i] = strconv.Itoa(value)
		}
		signature := strings.Join(result, ",")
		if previous, ok := seen[record.RoundID]; ok {
			if previous != signature {
				return nil, ErrResponse
			}
			continue
		}
		seen[record.RoundID] = signature
		// drawTimeMs is collector reception time, not an authoritative close time.
		events = append(events, luludraw.Event{Game: game, Round: strconv.FormatInt(record.RoundID, 10), Kind: "luluall_history", ResultField: "items", Result: result})
	}
	sort.Slice(events, func(i, j int) bool {
		a, _ := strconv.ParseInt(events[i].Round, 10, 64)
		b, _ := strconv.ParseInt(events[j].Round, 10, 64)
		return a < b
	})
	return events, nil
}
