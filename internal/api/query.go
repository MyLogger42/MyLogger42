package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store"
)

// handleQuery implements GET /api/v1/logs.
//
// Path-vs-list dispatch is handled by the router (logsHandler); this
// method assumes the caller already verified the request is a list query
// (no trailing :id segment).
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, total, err := s.store.Query(r.Context(), filter)
	if err != nil {
		s.logger.Printf("query: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  effectiveLimit(filter.Limit),
		"offset": filter.Offset,
	})
}

// handleGetByID implements GET /api/v1/logs/:id.
func (s *Server) handleGetByID(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	entry, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.logger.Printf("get: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleSources implements GET /api/v1/sources.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.store.Sources(r.Context())
	if err != nil {
		s.logger.Printf("sources: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if srcs == nil {
		// Stable empty array, never null on the wire.
		srcs = []domain.Source{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": srcs})
}

// parseQueryFilter pulls the documented query string params into a
// QueryFilter and applies validation. Unknown levels and malformed
// timestamps both produce 400.
func parseQueryFilter(r *http.Request) (domain.QueryFilter, error) {
	q := r.URL.Query()
	f := domain.QueryFilter{
		Service: strings.TrimSpace(q.Get("service")),
		Search:  strings.TrimSpace(q.Get("search")),
		Sort:    domain.SortDesc, // default per spec
	}
	if levelStr := q.Get("level"); levelStr != "" {
		lvl, err := domain.ParseLogLevel(levelStr)
		if err != nil {
			return f, err
		}
		f.Level = lvl
	}
	if fromStr := q.Get("from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return f, errors.New("from: " + err.Error())
		}
		f.From = &t
	}
	if toStr := q.Get("to"); toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return f, errors.New("to: " + err.Error())
		}
		f.To = &t
	}
	if limStr := q.Get("limit"); limStr != "" {
		n, err := strconv.Atoi(limStr)
		if err != nil {
			return f, errors.New("limit: " + err.Error())
		}
		f.Limit = n
	}
	if offStr := q.Get("offset"); offStr != "" {
		n, err := strconv.Atoi(offStr)
		if err != nil {
			return f, errors.New("offset: " + err.Error())
		}
		f.Offset = n
	}
	if sortStr := q.Get("sort"); sortStr != "" {
		f.Sort = domain.SortOrder(strings.ToLower(sortStr))
	}
	if err := f.Validate(); err != nil {
		return f, err
	}
	return f, nil
}

// effectiveLimit echoes the limit the store actually used so clients
// don't have to re-derive defaults.
func effectiveLimit(req int) int {
	if req <= 0 {
		return 50
	}
	return req
}
