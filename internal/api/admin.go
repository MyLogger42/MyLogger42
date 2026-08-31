package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mylogger42/mylogger42/internal/auth"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// --- DTOs -------------------------------------------------------------
//
// We don't JSON-marshal domain.Project / domain.ProjectKey directly
// because (a) field casing in Go is uppercase (CreatedAt) but the wire
// format is lowercase camelCase, and (b) we want a single place to
// audit what fields cross the API boundary. Add fields here intentionally.

type projectDTO struct {
	ID            string     `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	RetentionDays *int       `json:"retentionDays"` // nil → null on the wire (inherit)
	CreatedAt     time.Time  `json:"createdAt"`
	ArchivedAt    *time.Time `json:"archivedAt"` // nil → null on the wire
}

func projectFromDomain(p *domain.Project) projectDTO {
	return projectDTO{
		ID:            p.ID,
		Slug:          p.Slug,
		Name:          p.Name,
		RetentionDays: p.RetentionDays,
		CreatedAt:     p.CreatedAt,
		ArchivedAt:    p.ArchivedAt,
	}
}

// keyDTO is the listing form. The plaintext value is intentionally
// absent; it's only ever returned at issuance time via issuedKeyDTO.
type keyDTO struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

func keyFromDomain(k *domain.ProjectKey) keyDTO {
	return keyDTO{
		ID:         k.ID,
		Prefix:     k.Prefix,
		Label:      k.Label,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
	}
}

// issuedKeyDTO is the create-time response. The Value field is the only
// place the plaintext "mlk_..._..." string is ever returned to anyone.
type issuedKeyDTO struct {
	ID        string    `json:"id"`
	Prefix    string    `json:"prefix"`
	Label     string    `json:"label"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"createdAt"`
}

// --- helpers ----------------------------------------------------------

// resolveProjectFromPath looks up the project named in the {slug} path
// param. Mirrors resolveProjectFromQuery but reads from the 1.22 mux's
// path values rather than the query string.
func (s *Server) resolveProjectFromPath(w http.ResponseWriter, r *http.Request) (*domain.Project, bool) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return nil, false
	}
	if err := domain.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	project, err := s.projects.GetBySlug(r.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	if err != nil {
		s.logger.Printf("admin: project lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return nil, false
	}
	return project, true
}

// strictDecode reads the body, caps it at 64 KiB (admin payloads are
// always tiny), and decodes with DisallowUnknownFields so a typo in the
// JSON keys produces a clear 400 instead of being silently dropped.
func strictDecode(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload: "+err.Error())
		return false
	}
	return true
}

// issueKeyForProject creates a fresh ingest key for the given project,
// stores its (prefix, hmac), and returns the plaintext value once.
// Used by both project-create (initial key) and POST /keys.
func (s *Server) issueKeyForProject(r *http.Request, projectID, label string) (issuedKeyDTO, error) {
	issued, err := auth.GenerateKey()
	if err != nil {
		return issuedKeyDTO{}, err
	}
	rec := domain.ProjectKey{
		ID:        uuid.NewString(),
		ProjectID: projectID,
		Prefix:    issued.Prefix,
		Label:     label,
		CreatedAt: time.Now().UTC(),
	}
	hexHMAC := auth.ComputeHMAC(s.pepper, issued.Secret)
	if err := s.keys.Create(r.Context(), rec, hexHMAC); err != nil {
		return issuedKeyDTO{}, err
	}
	// A cached miss for this prefix would otherwise reject the new key
	// until the TTL lapsed.
	s.authCache.invalidateAll()
	return issuedKeyDTO{
		ID:        rec.ID,
		Prefix:    rec.Prefix,
		Label:     rec.Label,
		Value:     issued.Value,
		CreatedAt: rec.CreatedAt,
	}, nil
}

// --- /admin/projects --------------------------------------------------

type createProjectRequest struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	RetentionDays *int   `json:"retentionDays,omitempty"`
}

type createProjectResponse struct {
	Project projectDTO   `json:"project"`
	Key     issuedKeyDTO `json:"key"`
}

// handleCreateProject implements POST /api/v1/admin/projects.
//
// Atomic-ish: a project is created first, then its first key is issued.
// If key issuance fails we leave the project (and its empty key list)
// in place — the admin can retry the key issuance via POST /keys, and
// archiving the project is the cleanup path. Stricter coordination
// would need a real transaction across the two stores, which v2 doesn't
// have.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if !strictDecode(w, r, &req) {
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	if err := domain.ValidateSlug(req.Slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.RetentionDays != nil && *req.RetentionDays < 0 {
		writeError(w, http.StatusBadRequest, "retentionDays must be >= 0")
		return
	}

	project := domain.Project{
		ID:            uuid.NewString(),
		Slug:          req.Slug,
		Name:          req.Name,
		RetentionDays: req.RetentionDays,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.projects.Create(r.Context(), project); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusBadRequest, "slug already in use")
			return
		}
		s.logger.Printf("admin: create project: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}

	key, err := s.issueKeyForProject(r, project.ID, "initial")
	if err != nil {
		// Project exists but key issuance failed. Log + return 500 so
		// the admin sees the inconsistency. They can retry POST /keys
		// or delete the project via CLI.
		s.logger.Printf("admin: issue first key for %s: %v", project.Slug, err)
		writeError(w, http.StatusInternalServerError, "issue key")
		return
	}

	writeJSON(w, http.StatusCreated, createProjectResponse{
		Project: projectFromDomain(&project),
		Key:     key,
	})
}

// handleListProjects implements GET /api/v1/admin/projects. Includes
// archived projects (admin UI filters client-side).
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	all, err := s.projects.List(r.Context(), true)
	if err != nil {
		s.logger.Printf("admin: list projects: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	out := make([]projectDTO, 0, len(all))
	for i := range all {
		out = append(out, projectFromDomain(&all[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

// handleGetProject implements GET /api/v1/admin/projects/{slug}.
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, projectFromDomain(project))
}

type updateProjectRequest struct {
	Name          *string `json:"name,omitempty"`
	RetentionDays *int    `json:"retentionDays,omitempty"`
}

// handleUpdateProject implements PATCH /api/v1/admin/projects/{slug}.
// Only name + retentionDays are mutable; slug is immutable per spec.
//
// Body fields are pointers so we can distinguish "absent" from "set to
// default". Admin UI sends partial updates; missing fields preserve
// current values.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	current, ok := s.resolveProjectFromPath(w, r)
	if !ok {
		return
	}
	var req updateProjectRequest
	if !strictDecode(w, r, &req) {
		return
	}

	name := current.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
	}
	retention := current.RetentionDays
	if req.RetentionDays != nil {
		// Pointer-to-int with negative value is invalid; nil means
		// "inherit", which is also the absence-of-field above.
		if *req.RetentionDays < 0 {
			writeError(w, http.StatusBadRequest, "retentionDays must be >= 0")
			return
		}
		retention = req.RetentionDays
	}

	updated, err := s.projects.Update(r.Context(), current.Slug, name, retention)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Printf("admin: update project: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	s.authCache.invalidateAll()
	writeJSON(w, http.StatusOK, projectFromDomain(updated))
}

// handleArchiveProject implements POST /api/v1/admin/projects/{slug}/archive.
// Idempotent — archiving an already-archived project returns 200 with
// the same row.
func (s *Server) handleArchiveProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := domain.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.projects.Archive(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Printf("admin: archive: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	// Drop the cached project snapshots so the archived flag is seen on
	// the next ingest call rather than up to authCacheTTL later. This
	// invalidation IS the mechanism — the middleware's archived check
	// reads the cache entry, not the database.
	s.authCache.invalidateAll()
	writeJSON(w, http.StatusOK, projectFromDomain(updated))
}

// handleUnarchiveProject implements POST /api/v1/admin/projects/{slug}/unarchive.
// Idempotent — unarchiving a non-archived project would still be
// represented in the store as a no-op update; v2's impl returns 404 if
// the slug doesn't exist (matches Archive's contract).
func (s *Server) handleUnarchiveProject(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := domain.ValidateSlug(slug); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.projects.Unarchive(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		s.logger.Printf("admin: unarchive: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	s.authCache.invalidateAll()
	writeJSON(w, http.StatusOK, projectFromDomain(updated))
}

// --- /admin/projects/{slug}/keys --------------------------------------

type issueKeyRequest struct {
	Label string `json:"label"`
}

// handleIssueKey implements POST /api/v1/admin/projects/{slug}/keys.
// Returns the plaintext key value once via issuedKeyDTO; subsequent
// listings only ever expose the prefix.
func (s *Server) handleIssueKey(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromPath(w, r)
	if !ok {
		return
	}
	var req issueKeyRequest
	if !strictDecode(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	key, err := s.issueKeyForProject(r, project.ID, req.Label)
	if err != nil {
		s.logger.Printf("admin: issue key: %v", err)
		writeError(w, http.StatusInternalServerError, "issue key")
		return
	}
	writeJSON(w, http.StatusCreated, key)
}

// handleListKeys implements GET /api/v1/admin/projects/{slug}/keys.
// Includes revoked keys (admin UI shows them with strikethrough). The
// plaintext value is never returned — only prefix + metadata.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromPath(w, r)
	if !ok {
		return
	}
	keys, err := s.keys.ListByProject(r.Context(), project.ID, true)
	if err != nil {
		s.logger.Printf("admin: list keys: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	out := make([]keyDTO, 0, len(keys))
	for i := range keys {
		out = append(out, keyFromDomain(&keys[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// handleRevokeKey implements DELETE /api/v1/admin/projects/{slug}/keys/{keyId}.
// Idempotent — revoking an already-revoked key returns 204. Idempotency
// matches the spec ("revoking an already-revoked key is a no-op 204").
//
// We also enforce the slug→key relationship: if {keyId} exists but
// belongs to a different project, return 404. This guards against admin
// UI bugs that might wire the wrong slug to a key id.
func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	project, ok := s.resolveProjectFromPath(w, r)
	if !ok {
		return
	}
	keyID := r.PathValue("keyId")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "keyId is required")
		return
	}

	rec, err := s.keys.GetByID(r.Context(), keyID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err != nil {
		s.logger.Printf("admin: revoke lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if rec.ProjectID != project.ID {
		// Don't leak the existence of the key — pretend it doesn't
		// belong to this project.
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err := s.keys.Revoke(r.Context(), keyID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Race: row vanished between lookup and revoke. Treat as
			// success — the desired state (no active key) holds.
			s.authCache.invalidateAll()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.logger.Printf("admin: revoke: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	// The revoked key must be rejected on the very next ingest call,
	// not once the TTL lapses.
	s.authCache.invalidateAll()
	w.WriteHeader(http.StatusNoContent)
}
