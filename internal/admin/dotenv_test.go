package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDotenvMissingFile(t *testing.T) {
	dir := t.TempDir()
	d, err := parseDotenv(filepath.Join(dir, "no-such.env"))
	if err != nil {
		t.Fatalf("parseDotenv on missing file should succeed empty, got %v", err)
	}
	if d.Has("ANYTHING") {
		t.Fatal("empty file shouldn't have keys")
	}
}

func TestParseAndPreserveLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	original := `# top comment
DB_DRIVER=sqlite
# another comment

KEY_PEPPER=existing-pepper
HTTP_PORT=8080
`
	if err := writeFile(path, original); err != nil {
		t.Fatal(err)
	}

	d, err := parseDotenv(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Get("DB_DRIVER") != "sqlite" || d.Get("KEY_PEPPER") != "existing-pepper" {
		t.Fatalf("missing values: %+v", d)
	}
	if !d.SetIfMissing("SESSION_SECRET", "fresh-secret") {
		t.Fatal("SetIfMissing on absent key should write")
	}
	if d.SetIfMissing("DB_DRIVER", "postgres") {
		t.Fatal("SetIfMissing on existing key should not write")
	}
	d.Set("KEY_PEPPER", "rotated-pepper")

	out := string(d.Render())
	for _, must := range []string{
		"# top comment",
		"# another comment",
		"DB_DRIVER=sqlite",         // kept
		"KEY_PEPPER=rotated-pepper", // replaced in place
		"HTTP_PORT=8080",
		"SESSION_SECRET=fresh-secret", // appended
	} {
		if !strings.Contains(out, must) {
			t.Errorf("rendered output missing %q\n---\n%s", must, out)
		}
	}
	// Existing comment lines stay near where they were — replacement
	// shouldn't reorder.
	if strings.Index(out, "DB_DRIVER=") > strings.Index(out, "KEY_PEPPER=") {
		t.Error("Set replaced KEY_PEPPER in the wrong position")
	}
}

func TestSplitDotenvAssignment(t *testing.T) {
	cases := []struct {
		in   string
		k, v string
		ok   bool
	}{
		{"FOO=bar", "FOO", "bar", true},
		{"  FOO=bar  ", "FOO", "bar", true},
		{`FOO="quoted"`, "FOO", "quoted", true},
		{"# comment", "", "", false},
		{"", "", "", false},
		{"=novalue", "", "", false},
		{"123=invalid", "", "", false},
		{"FOO BAR=x", "", "", false},
		{"export FOO=bar", "", "", false}, // we don't support export prefix
	}
	for _, tc := range cases {
		k, v, ok := splitDotenvAssignment(tc.in)
		if k != tc.k || v != tc.v || ok != tc.ok {
			t.Errorf("split(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, k, v, ok, tc.k, tc.v, tc.ok)
		}
	}
}

func TestQuotingRoundtripsBcryptHash(t *testing.T) {
	// bcrypt hashes start with $2a$ (or $2b$/$2y$). Without single
	// quotes around the value, `set -a; source .env` would let the
	// shell interpret $2a as positional-parameter expansion and
	// corrupt the value. The dotenv writer must quote any value
	// containing $ (and a few other shell-meta chars).
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	d, err := parseDotenv(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := `$2a$12$tSrBE39QbFS4Gqlm0QAASeLtuPuJiMPfpj6w.4CAYSJEAwoAE8z0a`
	d.Set("ADMIN_PASSWORD_HASH", hash)
	if err := d.Save(path); err != nil {
		t.Fatal(err)
	}

	// 1. The on-disk line must be single-quoted so a shell `source`
	//    leaves it intact.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `ADMIN_PASSWORD_HASH='` + hash + `'`
	if !strings.Contains(string(body), want) {
		t.Fatalf("on-disk line missing single quotes:\n%s", body)
	}

	// 2. Round-trip through parseDotenv unwraps the quotes back to the
	//    raw hash, so re-running init reads the existing value
	//    correctly.
	d2, err := parseDotenv(path)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Get("ADMIN_PASSWORD_HASH") != hash {
		t.Fatalf("roundtrip mismatch:\nstored: %q\nwant:   %q", d2.Get("ADMIN_PASSWORD_HASH"), hash)
	}
}

func TestQuotingPreservesPlainValues(t *testing.T) {
	// Plain alphanumeric values shouldn't pick up gratuitous quotes.
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	d, _ := parseDotenv(path)
	d.Set("DB_DRIVER", "sqlite")
	d.Set("HTTP_PORT", "8080")
	d.Set("KEY_PEPPER", "deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567")
	if err := d.Save(path); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for _, line := range []string{
		"DB_DRIVER=sqlite",
		"HTTP_PORT=8080",
		"KEY_PEPPER=deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
	} {
		if !strings.Contains(string(body), line) {
			t.Errorf("missing unquoted line %q\n---\n%s", line, body)
		}
	}
	if strings.Contains(string(body), `'`) {
		t.Errorf("plain values shouldn't be quoted:\n%s", body)
	}
}

// writeFile is a tiny helper that avoids spreading os.WriteFile calls
// across the test cases.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
