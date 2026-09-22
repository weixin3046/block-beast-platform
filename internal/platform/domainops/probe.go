package domainops

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type ProbeResult struct {
	HTTP      string `json:"http"`
	HTTPS     string `json:"https"`
	WebSocket string `json:"websocket"`
}

func probeClient(address string, roots *x509.CertPool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(target)
		if err != nil {
			return nil, err
		}
		fixed := address
		if net.ParseIP(address) != nil {
			fixed = net.JoinHostPort(address, port)
		}
		host, _, err := net.SplitHostPort(fixed)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return nil, fmt.Errorf("验收仅连接当前服务器回环地址")
		}
		return dialer.DialContext(ctx, network, fixed)
	}}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func probeOnce(ctx context.Context, d Domain, client *http.Client, token string) (ProbeResult, error) {
	result := ProbeResult{HTTPS: "not-configured", WebSocket: "not-run"}
	schemes := []string{"http"}
	if d.CertPath != "" {
		schemes = append(schemes, "https")
	}
	for _, scheme := range schemes {
		for _, check := range []struct{ path, status string }{{"/healthz", "ok"}, {"/readyz", "ready"}} {
			req, err := http.NewRequestWithContext(ctx, "GET", scheme+"://"+d.Name+check.path, nil)
			if err != nil {
				return result, err
			}
			resp, err := client.Do(req)
			if err != nil {
				return result, fmt.Errorf("%s %s 检查失败", scheme, check.path)
			}
			b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
			resp.Body.Close()
			var body struct {
				Status string `json:"status"`
			}
			if err != nil || len(b) > 4096 || resp.StatusCode != 200 || json.Unmarshal(b, &body) != nil || body.Status != check.status {
				return result, fmt.Errorf("%s %s 响应不正确", scheme, check.path)
			}
		}
		if scheme == "http" {
			result.HTTP = "ok"
		} else {
			result.HTTPS = "ok"
		}
		for _, origin := range d.Origins {
			req, _ := http.NewRequestWithContext(ctx, "OPTIONS", scheme+"://"+d.Name+"/v1/auth/login", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			resp, err := client.Do(req)
			if err != nil {
				return result, fmt.Errorf("Origin 预检失败")
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != origin {
				return result, fmt.Errorf("允许来源尚未生效")
			}
		}
		if len(d.Origins) > 0 {
			rejected := fmt.Sprintf("https://domain-check-%d.invalid", time.Now().UnixNano())
			req, _ := http.NewRequestWithContext(ctx, "OPTIONS", scheme+"://"+d.Name+"/v1/auth/login", nil)
			req.Header.Set("Origin", rejected)
			req.Header.Set("Access-Control-Request-Method", "POST")
			resp, err := client.Do(req)
			if err != nil {
				return result, fmt.Errorf("拒绝来源预检失败")
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				return result, fmt.Errorf("未知来源未被拒绝；请检查既有通配白名单")
			}
			if token != "" {
				wsURL := strings.Replace(scheme, "http", "ws", 1) + "://" + d.Name + "/v1/ws"
				conn, resp, dialErr := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"bearer." + token}, HTTPHeader: http.Header{"Origin": []string{rejected}}})
				if conn != nil {
					conn.CloseNow()
				}
				if resp != nil {
					resp.Body.Close()
				}
				if dialErr == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
					return result, fmt.Errorf("WebSocket 未正确拒绝未知来源")
				}
			}
		}
		if token == "" {
			req, _ := http.NewRequestWithContext(ctx, "GET", scheme+"://"+d.Name+"/v1/ws", nil)
			resp, err := client.Do(req)
			if err != nil {
				return result, fmt.Errorf("WebSocket 路由检查失败")
			}
			resp.Body.Close()
			if resp.StatusCode != 401 {
				return result, fmt.Errorf("WebSocket 未认证路由应返回 401")
			}
			result.WebSocket = "route-only"
		} else {
			url := strings.Replace(scheme, "http", "ws", 1) + "://" + d.Name + "/v1/ws"
			conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"bearer." + token}, HTTPHeader: probeOriginHeader(d, scheme)})
			if err != nil {
				if resp != nil {
					resp.Body.Close()
				}
				return result, fmt.Errorf("WebSocket 认证握手失败")
			}
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, b, err := conn.Read(readCtx)
			cancel()
			conn.CloseNow()
			var msg struct {
				Type string `json:"type"`
			}
			if err != nil || json.Unmarshal(b, &msg) != nil || msg.Type != "hello" {
				return result, fmt.Errorf("WebSocket 未返回 hello")
			}
			result.WebSocket = "authenticated"
		}
	}
	return result, nil
}
func Probe(ctx context.Context, address string, d Domain) (ProbeResult, error) {
	return ProbeWithSession(ctx, address, d, "")
}
func ProbeWithSession(ctx context.Context, address string, d Domain, token string) (ProbeResult, error) {
	client := probeClient(address, nil)
	defer client.CloseIdleConnections()
	var result ProbeResult
	var err error
	for i := 0; i < 3; i++ {
		result, err = probeOnce(ctx, d, client, token)
		if err == nil {
			return result, nil
		}
		if i < 2 {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	return result, err
}

func probeOriginHeader(d Domain, scheme string) http.Header {
	h := http.Header{}
	origin := scheme + "://" + d.Name
	if len(d.Origins) > 0 {
		origin = d.Origins[0]
	}
	h.Set("Origin", origin)
	return h
}
