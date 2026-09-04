package httpapi

import (
	"net/http"
)

// Preserve ServeMux matching, redirects and Allow semantics while replacing its
// otherwise English 404/405 bodies. Matched handlers still use ServeHTTP so path
// parameters are populated by ServeMux, rather than invoking Handler directly.
func chineseRoutingErrors(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(&routingErrorWriter{ResponseWriter: w}, r)
	})
}

type routingErrorWriter struct {
	http.ResponseWriter
	replaced bool
}

func (w *routingErrorWriter) WriteHeader(status int) {
	if w.replaced {
		return
	}
	message := ""
	switch status {
	case http.StatusNotFound:
		message = "interface not found"
	case http.StatusMethodNotAllowed:
		message = "request method is not allowed"
	}
	if message != "" {
		w.replaced = true
		w.Header().Del("Content-Length")
		writeJSON(w.ResponseWriter, status, map[string]string{"error": message})
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *routingErrorWriter) Write(p []byte) (int, error) {
	if w.replaced {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
