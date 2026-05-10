package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/silverbaq/mylogger42/internal/domain"
)

// maxBatchSize caps the number of entries POST /logs/batch will accept.
const maxBatchSize = 500

// handleIngestSingle implements POST /api/v1/logs.
//
// Auth is enforced by the requireAPIKey middleware before this handler
// runs, so we focus on shape validation, default-application and store
// write here.
func (s *Server) handleIngestSingle(w http.ResponseWriter, r *http.Request) {
	var payload domain.IngestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if err := payload.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// TODO(v2): projectID will be supplied by requireProjectKey middleware.
	entry, err := payload.ToEntry(uuid.NewString(), "", time.Now().UTC())
	if err != nil {
		// Should not happen — Validate already exercised the same parse.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Insert(r.Context(), entry); err != nil {
		s.logger.Printf("ingest: insert failed: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	// Best-effort fan-out to live-tail subscribers. Hub.Publish is non-
	// blocking; a saturated buffer drops the entry rather than slowing
	// the ingest path.
	if s.hub != nil {
		s.hub.Publish(entry)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": entry.ID})
}

// batchError is one element of the 400 response body for /logs/batch.
type batchError struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// handleIngestBatch implements POST /api/v1/logs/batch.
//
// The endpoint is fully atomic: either every supplied entry is written
// or none are. Validation runs across the entire array up-front and any
// failure produces a 400 with the per-index error list — no DB writes
// happen in that case.
func (s *Server) handleIngestBatch(w http.ResponseWriter, r *http.Request) {
	var payloads []domain.IngestPayload
	if err := json.NewDecoder(r.Body).Decode(&payloads); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if len(payloads) == 0 {
		writeError(w, http.StatusBadRequest, "batch must contain at least one entry")
		return
	}
	if len(payloads) > maxBatchSize {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch size %d exceeds max %d", len(payloads), maxBatchSize))
		return
	}

	var errs []batchError
	entries := make([]domain.LogEntry, 0, len(payloads))
	now := time.Now().UTC()

	for i, p := range payloads {
		if err := p.Validate(); err != nil {
			errs = append(errs, batchError{Index: i, Error: err.Error()})
			continue
		}
		// Allocate a UUID per entry up-front so we can return ids on
		// success without an extra pass.
		// TODO(v2): projectID will be supplied by requireProjectKey middleware.
		entry, err := p.ToEntry(uuid.NewString(), "", now)
		if err != nil {
			errs = append(errs, batchError{Index: i, Error: err.Error()})
			continue
		}
		entries = append(entries, entry)
	}
	if len(errs) > 0 {
		// Atomicity: do not persist anything if any payload failed.
		writeJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
		return
	}

	if err := s.store.InsertBatch(r.Context(), entries); err != nil {
		s.logger.Printf("ingest: batch insert failed: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if s.hub != nil {
		for _, e := range entries {
			s.hub.Publish(e)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]int{"accepted": len(entries)})
}

// writeJSON serialises payload as JSON with the given status. Failures
// to encode are logged but not surfaced — there's no useful recovery.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// Connection likely already closed; nothing actionable.
		_ = err
	}
}

// writeError emits a uniform JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

