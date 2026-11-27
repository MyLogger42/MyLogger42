package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mylogger42/mylogger42/internal/auth"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

func TestRequireReadToken(t *testing.T) {
	ctx := context.Background()
	stores, err := store.OpenSQLiteStores(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stores.Logs.Close() })
	pepper := []byte("0123456789abcdef0123456789abcdef")

	issue := func(id string) string {
		tok, _ := auth.GenerateReadToken()
		if err := stores.Tokens.Create(ctx, domain.AccessToken{
			ID: id, Prefix: tok.Prefix, Label: id, CreatedAt: time.Now(),
		}, auth.ComputeHMAC(pepper, tok.Secret)); err != nil {
			t.Fatal(err)
		}
		return tok.Value
	}
	valid := issue("live")
	revoked := issue("dead")
	if err := stores.Tokens.Revoke(ctx, "dead"); err != nil {
		t.Fatal(err)
	}
	ingest, _ := auth.GenerateKey()
	wrongSecret := valid[:len(valid)-1] + map[bool]string{true: "b", false: "a"}[valid[len(valid)-1] == 'a']

	h := requireReadToken(stores.Tokens, pepper)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for name, tc := range map[string]struct {
		header string
		want   int
	}{
		"missing":      {"", http.StatusUnauthorized},
		"not bearer":   {"Basic " + valid, http.StatusUnauthorized},
		"ingest key":   {"Bearer " + ingest.Value, http.StatusUnauthorized},
		"revoked":      {"Bearer " + revoked, http.StatusUnauthorized},
		"wrong secret": {"Bearer " + wrongSecret, http.StatusUnauthorized},
		"valid":        {"Bearer " + valid, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Errorf("%s: got %d, want %d", name, rr.Code, tc.want)
		}
		if tc.want == http.StatusUnauthorized && rr.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate", name)
		}
	}
}
