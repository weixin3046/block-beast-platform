package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/block-beast/platform/internal/application/operations"
)

type PhraseService interface {
	ListPhrases(context.Context, string, operations.PhraseFilter) (operations.PhrasePage, error)
	SavePhrase(context.Context, string, int64, string, operations.PhraseInput) (operations.Phrase, error)
	DeletePhrase(context.Context, string, int64) error
	SetPhraseEnabled(context.Context, string, int64, bool) (operations.Phrase, error)
	ReorderPhrases(context.Context, string, []int64) ([]operations.Phrase, error)
}

func WithPhrases(service PhraseService) Option { return func(s *Server) { s.phrases = service } }
func phraseError(w http.ResponseWriter, err error) {
	status, message := 500, "话术服务异常"
	switch {
	case errors.Is(err, operations.ErrPhraseInvalid):
		status = 400
	case errors.Is(err, operations.ErrPhraseNotFound):
		status = 404
	case errors.Is(err, operations.ErrPhraseForbidden):
		status = 403
	case errors.Is(err, operations.ErrPhraseConflict):
		status = 409
	}
	if status != 500 {
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}
func (s *Server) phraseActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.phrases == nil {
		writeJSON(w, 503, map[string]string{"error": "话术服务不可用"})
		return "", false
	}
	c, _ := ClaimsFromContext(r.Context())
	return c.Subject, true
}
func phraseBody(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		phraseError(w, operations.ErrPhraseInvalid)
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		phraseError(w, operations.ErrPhraseInvalid)
		return false
	}
	return true
}
func phraseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("phraseID"), 10, 64)
	if err != nil || id <= 0 {
		phraseError(w, operations.ErrPhraseInvalid)
		return 0, false
	}
	return id, true
}
func (s *Server) listPhrases(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.phraseActor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := operations.PhraseFilter{Category: q.Get("category"), Limit: 20}
	var err error
	if q.Has("page") {
		f.Page, err = strconv.ParseInt(q.Get("page"), 10, 64)
		if err != nil || f.Page < 0 {
			phraseError(w, operations.ErrPhraseInvalid)
			return
		}
	}
	if q.Has("limit") {
		f.Limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			phraseError(w, operations.ErrPhraseInvalid)
			return
		}
	}
	if q.Has("enabled") {
		v := q.Get("enabled")
		if v != "true" && v != "false" {
			phraseError(w, operations.ErrPhraseInvalid)
			return
		}
		b := v == "true"
		f.Enabled = &b
	}
	result, err := s.phrases.ListPhrases(r.Context(), actor, f)
	if err != nil {
		phraseError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) createPhrase(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.phraseActor(w, r)
	if !ok {
		return
	}
	var in struct {
		operations.PhraseInput
		RequestID string `json:"request_id"`
	}
	if !phraseBody(w, r, &in) {
		return
	}
	result, err := s.phrases.SavePhrase(r.Context(), actor, 0, in.RequestID, in.PhraseInput)
	if err != nil {
		phraseError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) updatePhrase(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.phraseActor(w, r)
	if !ok {
		return
	}
	id, ok := phraseID(w, r)
	if !ok {
		return
	}
	var in operations.PhraseInput
	if !phraseBody(w, r, &in) {
		return
	}
	result, err := s.phrases.SavePhrase(r.Context(), actor, id, "", in)
	if err != nil {
		phraseError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) deletePhrase(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.phraseActor(w, r)
	if !ok {
		return
	}
	id, ok := phraseID(w, r)
	if !ok {
		return
	}
	if err := s.phrases.DeletePhrase(r.Context(), actor, id); err != nil {
		phraseError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) setPhraseEnabled(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.phraseActor(w, r)
	if !ok {
		return
	}
	id, ok := phraseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !phraseBody(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		phraseError(w, operations.ErrPhraseInvalid)
		return
	}
	result, err := s.phrases.SetPhraseEnabled(r.Context(), actor, id, *in.Enabled)
	if err != nil {
		phraseError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) reorderPhrases(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.phraseActor(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !phraseBody(w, r, &in) {
		return
	}
	result, err := s.phrases.ReorderPhrases(r.Context(), actor, in.IDs)
	if err != nil {
		phraseError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": result})
}
