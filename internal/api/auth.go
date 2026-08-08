package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/mylogger42/mylogger42/internal/auth"
	"github.com/mylogger42/mylogger42/internal/store"
)

// loginRequest is the body of POST /api/v1/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin verifies the supplied credentials, mints a session JWT,
// and sets it as the canonical session cookie. On any failure it
// produces 401 after a randomized 200ms+jitter delay so an attacker
// can't distinguish "no such user" from "wrong password" by timing.
//
// The flow:
//
//  1. Decode body. Reject 400 on malformed JSON.
//  2. Look up user by username. On miss, run bcrypt against a dummy
//     hash so the timing matches the wrong-password branch.
//  3. bcrypt-compare the supplied password.
//  4. NoteLogin (best-effort) and issue a JWT; set as cookie.
//
// Per the v2 spec there is no rate limiting in v1 of this — it's
// delegated to fail2ban / the reverse proxy. The constant-time delay
// here is purely about removing the timing oracle, not throttling.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.trustProxy)
	if !s.loginLimiter.check(ip) {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	defer constantTimeFloor()()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req loginRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload: "+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	user, err := s.users.GetByUsername(r.Context(), req.Username)
	if errors.Is(err, store.ErrNotFound) {
		// Burn equivalent CPU on the bcrypt branch so timing doesn't
		// reveal "no such user" vs "wrong password".
		auth.VerifyPassword(dummyBcryptHash, req.Password)
		s.loginLimiter.record(ip)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		s.logger.Printf("login: lookup: %v", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}

	if !auth.VerifyPassword(user.PasswordHash, req.Password) {
		s.loginLimiter.record(ip)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Best-effort last-login marker. If it fails we still issue the
	// session — it's diagnostic metadata, not load-bearing.
	_ = s.users.NoteLogin(r.Context(), user.ID, time.Now().UTC())

	tok, err := s.signer.Issue(user.ID, user.Username, time.Now().UTC())
	if err != nil {
		s.logger.Printf("login: issue: %v", err)
		writeError(w, http.StatusInternalServerError, "issue session")
		return
	}
	http.SetCookie(w, buildSessionCookie(tok, s.cookieSecure(r)))
	// 204 — the cookie is the response. The SPA's API client treats any
	// 2xx-without-body as "success, nothing to parse"; sending 200 here
	// previously made fetch.json() throw "Unexpected end of JSON input"
	// in the browser.
	w.WriteHeader(http.StatusNoContent)
}

// handleLogout clears the session cookie. Must run behind
// requireSession so a missing cookie is a 401 before we get here.
//
// We deliberately do NOT verify Origin == Host on logout: SameSite=Lax
// already blocks cross-site form-style POSTs, and the operation is
// safe-by-design (worst case an attacker forces *us* to log out, which
// is the user-action this endpoint exists to perform).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clear := buildSessionCookie("", s.cookieSecure(r))
	clear.MaxAge = -1
	clear.Expires = time.Unix(0, 0)
	http.SetCookie(w, clear)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current session subject so the SPA can decide
// whether to redirect to /login. Requires session — the middleware has
// already validated the cookie before we get here.
//
// If the users table is empty (no admin configured yet, no env-var
// bootstrap done either) the route still requires a session, so
// reaching this handler implies a session exists, which implies a user
// exists. The "no admin configured" 503 is therefore checked at login
// time, not here.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := SessionFromContext(r.Context())
	if !ok {
		// Defense-in-depth — should be impossible behind requireSession.
		writeError(w, http.StatusInternalServerError, "session context missing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"username": claims.Username,
	})
}

// dummyBcryptHash is a real bcrypt(cost=12) hash whose plaintext is
// effectively unknowable. We pass it to VerifyPassword on the
// no-such-user branch of login so the bcrypt CPU cost shows up
// regardless of which path runs. The actual plaintext doesn't matter
// — VerifyPassword will return false either way; what matters is the
// time it takes (~250ms at cost 12).
//
// Generated once via bcrypt of a random string; checked into source
// because it's not a secret and rotating it serves no purpose.
const dummyBcryptHash = "$2a$12$TCvLIiss7dxYMI14Prs2ku4MCeHGKdSArDAOR5CIF2dXXMBKmV4Z2"

// constantTimeFloor returns a deferred function that pads the request's
// total wall time up to a randomized minimum (~200-300ms) before
// returning. Used by login to mask DB / bcrypt timing.
//
// We accept the wall-clock cost because login is rare (admin only).
func constantTimeFloor() func() {
	start := time.Now()
	floor := 200*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond
	return func() {
		if elapsed := time.Since(start); elapsed < floor {
			time.Sleep(floor - elapsed)
		}
	}
}

