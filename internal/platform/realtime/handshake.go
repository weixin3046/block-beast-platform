package realtime

import (
	"io"
	"net/http"
)

// The websocket library uses Unwrap to retain the underlying Hijacker. Only
// unsuccessful handshake bodies are replaced; successful upgrades are untouched.
type handshakeResponseWriter struct {
	http.ResponseWriter
	failed bool
}

func (w *handshakeResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *handshakeResponseWriter) WriteHeader(status int) {
	if w.failed {
		return
	}
	if status >= 400 {
		w.failed = true
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(status)
		message := "实时连接建立失败，请稍后重试"
		switch status {
		case http.StatusBadRequest:
			message = "WebSocket 握手请求格式不正确"
		case http.StatusForbidden:
			message = "当前访问来源不被允许"
		case http.StatusMethodNotAllowed:
			message = "WebSocket 连接必须使用 GET 请求"
		case http.StatusUpgradeRequired:
			message = "请通过 WebSocket 协议建立连接"
		}
		_, _ = io.WriteString(w.ResponseWriter, message+"\n")
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *handshakeResponseWriter) Write(p []byte) (int, error) {
	if w.failed {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
