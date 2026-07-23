package httpapi

import "net/http"

func (server *Server) assets(writer http.ResponseWriter, request *http.Request) {
	if server.providerAssets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "provider assets are unavailable"})
		return
	}
	assets, err := server.providerAssets.ListEnabled(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read provider assets"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"provider": "pqpa", "assets": assets})
}
