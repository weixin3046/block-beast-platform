package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/currency"
)

type CurrencyService interface {
	List(context.Context, bool) ([]currency.Currency, error)
	Create(context.Context, currency.Currency) (currency.Currency, error)
	Update(context.Context, string, currency.Update) (currency.Currency, error)
}

func WithCurrencies(s CurrencyService) Option { return func(server *Server) { server.currencies = s } }
func (server *Server) listCurrencies(w http.ResponseWriter, r *http.Request) {
	server.currencyList(w, r, true)
}
func (server *Server) adminCurrencies(w http.ResponseWriter, r *http.Request) {
	server.currencyList(w, r, false)
}
func (server *Server) currencyList(w http.ResponseWriter, r *http.Request, enabledOnly bool) {
	if server.currencies == nil {
		writeJSON(w, 503, map[string]string{"error": "currency service is unavailable"})
		return
	}
	items, err := server.currencies.List(r.Context(), enabledOnly)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "unable to list currencies"})
		return
	}
	writeJSON(w, 200, items)
}
func (server *Server) createCurrency(w http.ResponseWriter, r *http.Request) {
	if server.currencies == nil {
		writeJSON(w, 503, map[string]string{"error": "currency service is unavailable"})
		return
	}
	var body struct {
		Code                 string `json:"code"`
		Name                 string `json:"name"`
		Decimals             *int   `json:"decimals"`
		Category             string `json:"category"`
		Enabled              *bool  `json:"enabled"`
		CreateOnRegistration *bool  `json:"create_on_registration"`
		SortOrder            *int   `json:"sort_order"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF || body.Decimals == nil || body.Enabled == nil || body.CreateOnRegistration == nil || body.SortOrder == nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	out, err := server.currencies.Create(r.Context(), currency.Currency{Code: body.Code, Name: body.Name, Decimals: *body.Decimals, Category: body.Category, Enabled: *body.Enabled, CreateOnRegistration: *body.CreateOnRegistration, SortOrder: *body.SortOrder})
	server.currencyResult(w, r, out, err, "currency.create", http.StatusCreated)
}
func (server *Server) updateCurrency(w http.ResponseWriter, r *http.Request) {
	if server.currencies == nil {
		writeJSON(w, 503, map[string]string{"error": "currency service is unavailable"})
		return
	}
	var body struct {
		Name                 string `json:"name"`
		Enabled              *bool  `json:"enabled"`
		CreateOnRegistration *bool  `json:"create_on_registration"`
		SortOrder            *int   `json:"sort_order"`
		Version              int64  `json:"version"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF || body.Enabled == nil || body.CreateOnRegistration == nil || body.SortOrder == nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	out, err := server.currencies.Update(r.Context(), r.PathValue("code"), currency.Update{Name: body.Name, Enabled: *body.Enabled, CreateOnRegistration: *body.CreateOnRegistration, SortOrder: *body.SortOrder, Version: body.Version})
	server.currencyResult(w, r, out, err, "currency.update", http.StatusOK)
}
func (server *Server) currencyResult(w http.ResponseWriter, r *http.Request, out currency.Currency, err error, action string, status int) {
	switch {
	case errors.Is(err, currency.ErrInvalid):
		writeJSON(w, 400, map[string]string{"error": err.Error()})
	case errors.Is(err, currency.ErrConflict):
		writeJSON(w, 409, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, 500, map[string]string{"error": "unable to save currency"})
	default:
		claims, _ := ClaimsFromContext(r.Context())
		server.recordAudit(r.Context(), audit.Entry{ActorUserID: claims.Subject, Action: action, TargetType: "currency", TargetID: out.Code, Payload: map[string]any{"version": out.Version}})
		writeJSON(w, status, out)
	}
}
