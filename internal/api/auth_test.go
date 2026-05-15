package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silverbaq/mylogger42/internal/auth"
	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store"
)

// fakeUserStore implements store.UserStore for the auth tests. Only the
// methods login uses are real; others are stubs.
type fakeUserStore struct {
	byUsername map[string]*domain.User
	loginNotes []string
}

func (f *fakeUserStore) Create(context.Context, domain.User) error { return store.ErrNotFound }
func (f *fakeUserStore) GetByUsername(_ context.Context, u string) (*domain.User, error) {
	user, ok := f.byUsername[u]
	if !ok {
		return nil, store.ErrNotFound
	}
	return user, nil
}
func (f *fakeUserStore) UpdatePassword(context.Context, string, string) error {
	return store.ErrNotFound
}
func (f *fakeUserStore) NoteLogin(_ context.Context, id string, _ time.Time) error {
	f.loginNotes = append(f.loginNotes, id)
	return nil
}
func (f *fakeUserStore) Count(context.Context) (int, error) { return len(f.byUsername), nil }

// authTestServer builds a minimal Server with a fake user store and a
// signer using a known secret, so tests can mint and verify their own
// tokens for the require-session paths.
func authTestServer(t *testing.T, password string) (*Server, *fakeUserStore) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeUserStore{
		byUsername: map[string]*domain.User{
			"admin": {ID: "u-1", Username: "admin", PasswordHash: hash, CreatedAt: time.Now().UTC()},
		},
	}
	signer := auth.NewSessionSigner([]byte("test-secret"))
	return &Server{
		users:        users,
		signer:       signer,
		cookieSecure: func(*http.Request) bool { return false },
		loginLimiter: newLoginRateLimiter(),
		logger:       log.New(io.Discard, "", 0),
	}, users
}

func TestLoginHappyPath(t *testing.T) {
	s, users := authTestServer(t, "hunter2")
	body := strings.NewReader(`{"username":"admin","password":"hunter2"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()
	s.handleLogin(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	var got *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			got = c
			break
		}
	}
	if got == nil {
		t.Fatalf("session cookie not set; got cookies=%+v", cookies)
	}
	if got.Value == "" || !got.HttpOnly || got.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie missing required attrs: %+v", got)
	}
	if len(users.loginNotes) != 1 || users.loginNotes[0] != "u-1" {
		t.Fatalf("NoteLogin not called: %v", users.loginNotes)
	}

	// Verify the token actually validates under the same signer.
	if _, err := s.signer.Verify(got.Value, time.Now().UTC()); err != nil {
		t.Fatalf("issued token didn't verify: %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	s, _ := authTestServer(t, "hunter2")
	body := strings.NewReader(`{"username":"admin","password":"wrong"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()
	s.handleLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestLoginUnknownUser(t *testing.T) {
	s, _ := authTestServer(t, "hunter2")
	body := strings.NewReader(`{"username":"ghost","password":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()
	s.handleLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestLoginRejectsExtraFields(t *testing.T) {
	s, _ := authTestServer(t, "hunter2")
	body := strings.NewReader(`{"username":"admin","password":"hunter2","admin":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()
	s.handleLogin(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (strict decoder rejects unknown fields)", rr.Code)
	}
}

func TestLoginRejectsEmptyFields(t *testing.T) {
	s, _ := authTestServer(t, "hunter2")
	body := strings.NewReader(`{"username":"","password":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()
	s.handleLogin(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	s, _ := authTestServer(t, "hunter2")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rr := httptest.NewRecorder()
	s.handleLogout(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	got := rr.Result().Cookies()
	if len(got) != 1 || got[0].Name != sessionCookieName || got[0].MaxAge != -1 {
		t.Fatalf("expected single MaxAge=-1 session cookie, got %+v", got)
	}
}

func TestMeReturnsClaimsFromContext(t *testing.T) {
	s, _ := authTestServer(t, "hunter2")
	claims := &auth.Claims{Subject: "u-1", Username: "admin"}
	ctx := context.WithValue(context.Background(), ctxKeySession, claims)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	s.handleMe(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"username":"admin"`) {
		t.Fatalf("body missing username: %s", rr.Body.String())
	}
}
