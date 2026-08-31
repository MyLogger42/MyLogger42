package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// resolveProjectFromQuery is the shared lookup helper for read
// endpoints. The v2 spec requires every list/read endpoint to scope by
// project via a ?project=<slug> query param — missing or unknown slugs
// produce 400 / 404 respectively. Slug case-sensitivity follows the
// store: uppercase slugs simply won't match, returning 404.
//
// On success, the caller has a Project they can scope the store call to.
// Errors are written directly to w; the caller checks the bool return.
func (s *Server) resolveProjectFromQuery(w http.ResponseWriter, r *http.Request) (*domain.Project, bool) {
	slug := strings.TrimSpace(r.URL.Query().Get("project"))
	if slug == "" {
		writeError(w, http.StatusBadRequest, "project is required (set ?project=<slug>)")
		return nil, false
	}
	if err := domain.ValidateSlug(slug); err != nil {
		// Malformed slugs are 400, not 404 — a 404 would be misleading
		// since "no such slug shape" isn't the same as "no project at
		// that slug."
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	project, err := s.projects.GetBySlug(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	if err != nil {
		s.logger.Printf("project lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return nil, false
	}
	return project, true
}

// handleQuery implements GET /api/v1/logs.
//
// Required query params: project=<slug>. All other QueryFilter params
// (service, level, from, to, search, limit, offset, sort) are optional
// and parsed by parseQueryFilter.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromQuery(w, r)
	if !ok {
		return
	}
	filter, err := parseQueryFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, total, err := s.store.Query(r.Context(), project.ID, filter)
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

// handleGetByID implements GET /api/v1/logs/{id}.
//
// Required query param: project=<slug>. An entry that exists but
// belongs to a different project returns 404 — matching the v2 spec's
// forward-compatible isolation rule (multi-user v3 will gate read
// access by project membership; v2's single admin always sees their
// own project's entries).
func (s *Server) handleGetByID(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	project, ok := s.resolveProjectFromQuery(w, r)
	if !ok {
		return
	}
	entry, err := s.store.Get(r.Context(), project.ID, id)
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
//
// Required query param: project=<slug>. The response is the
// derived-projection list of services scoped to that project.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromQuery(w, r)
	if !ok {
		return
	}
	srcs, err := s.store.Sources(r.Context(), project.ID)
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

// handleStats implements GET /api/v1/stats.
//
// Same params as GET /api/v1/logs (project is required, every filter
// optional) plus &bucket=<seconds> from domain.StatsBuckets. limit/offset
// are ignored — stats always describe the whole filtered range, not the
// page the user happens to be on.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromQuery(w, r)
	if !ok {
		return
	}
	filter, err := parseQueryFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bucket := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("bucket")); raw != "" {
		bucket, err = strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bucket: "+err.Error())
			return
		}
	}
	if err := domain.ValidateBucketSeconds(bucket); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stats, err := s.store.Stats(r.Context(), project.ID, filter, bucket)
	if err != nil {
		s.logger.Printf("stats: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	writeJSON(w, http.StatusOK, stats)
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
