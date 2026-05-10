package admin

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// dotenvFile is a small line-preserving editor for a .env file.
//
// The parser doesn't try to be a full shell-quote interpreter — it
// reads each line verbatim and only understands "<KEY>=<VALUE>" as a
// settable line. Comments, blank lines, and lines that don't match the
// KEY= shape are kept exactly as the operator wrote them (so a hand-
// edited .env doesn't get reformatted by `admin init`).
//
// Set / SetIfMissing replace or append values keyed by name. Render
// returns the resulting bytes; Save writes them to disk.
type dotenvFile struct {
	// lines holds either a setLine (KEY=VALUE) or a rawLine (everything
	// else). Order is preserved on render.
	lines []dotenvLine
	// keyIndex maps KEY → line index for quick replace.
	keyIndex map[string]int
}

type dotenvLine struct {
	// raw is the original textual line, including leading whitespace
	// but without the trailing newline. For setLines it's regenerated
	// at render time from key + value.
	raw     string
	isSet   bool
	key     string
	value   string
	hadComment string // optional trailing comment to preserve on a setLine
}

// parseDotenv reads a .env content into a dotenvFile. A non-existent
// file (path missing entirely) returns an empty file — used by `admin
// init` on a fresh install.
func parseDotenv(path string) (*dotenvFile, error) {
	d := &dotenvFile{keyIndex: map[string]int{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return d, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		k, v, ok := splitDotenvAssignment(line)
		if !ok {
			d.lines = append(d.lines, dotenvLine{raw: line})
			continue
		}
		d.keyIndex[k] = len(d.lines)
		d.lines = append(d.lines, dotenvLine{
			isSet: true,
			key:   k,
			value: v,
		})
	}
	return d, nil
}

// splitDotenvAssignment recognises a KEY=VALUE line. KEY must start
// with a letter or underscore and contain only [A-Za-z0-9_]. Lines
// that don't match (comments, blanks, "export X=" forms, etc.) return
// ok=false and the caller preserves them as-is.
func splitDotenvAssignment(line string) (key, value string, ok bool) {
	trim := strings.TrimSpace(line)
	if trim == "" || strings.HasPrefix(trim, "#") {
		return "", "", false
	}
	eq := strings.IndexByte(trim, '=')
	if eq <= 0 {
		return "", "", false
	}
	k := trim[:eq]
	for i, c := range k {
		if i == 0 && !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return "", "", false
		}
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return "", "", false
		}
	}
	v := trim[eq+1:]
	// Strip a single layer of matching quotes (either ' or "). For
	// single-quoted values the inner '\'' (POSIX escape sequence we
	// emit on write) is decoded back to a literal single quote — same
	// transformation a shell would apply on `source`.
	if len(v) >= 2 {
		if v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		} else if v[0] == '\'' && v[len(v)-1] == '\'' {
			v = strings.ReplaceAll(v[1:len(v)-1], `'\''`, `'`)
		}
	}
	return k, v, true
}

// Has reports whether the file currently sets the given key.
func (d *dotenvFile) Has(key string) bool {
	_, ok := d.keyIndex[key]
	return ok
}

// Get returns the current value for key, or "" if absent.
func (d *dotenvFile) Get(key string) string {
	idx, ok := d.keyIndex[key]
	if !ok {
		return ""
	}
	return d.lines[idx].value
}

// Set assigns key=value, replacing in place if the key already exists.
// Replaces preserve the line's position so a thoughtfully-grouped .env
// stays grouped after edits.
func (d *dotenvFile) Set(key, value string) {
	if idx, ok := d.keyIndex[key]; ok {
		d.lines[idx].value = value
		return
	}
	d.keyIndex[key] = len(d.lines)
	d.lines = append(d.lines, dotenvLine{isSet: true, key: key, value: value})
}

// SetIfMissing sets key only if it's not currently set. Returns true
// when it actually wrote (so the caller knows whether to log "set" or
// "kept existing").
func (d *dotenvFile) SetIfMissing(key, value string) bool {
	if d.Has(key) {
		return false
	}
	d.Set(key, value)
	return true
}

// Render produces the file's textual form, with a trailing newline.
// Set lines use the canonical "KEY=VALUE" shape; non-set lines are
// emitted verbatim. Values that look like they'd be mangled by a
// `set -a; source .env` flow are single-quoted (see needsQuoting).
func (d *dotenvFile) Render() []byte {
	var buf bytes.Buffer
	for i, l := range d.lines {
		if l.isSet {
			fmt.Fprintf(&buf, "%s=%s", l.key, quoteIfNeeded(l.value))
		} else {
			buf.WriteString(l.raw)
		}
		if i < len(d.lines)-1 {
			buf.WriteByte('\n')
		}
	}
	if buf.Len() == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// quoteIfNeeded wraps the value in single quotes when it contains
// characters that bash/zsh would interpret if the operator runs
// `set -a; source .env` (the documented quickstart). bcrypt hashes
// like `$2a$12$...` were getting their `$2a` munged into shell
// positional-parameter expansion otherwise.
//
// Single quotes are POSIX-quote-safe: nothing inside them is
// interpreted, except a literal single quote which we escape via the
// "'" + `\'` + "'" pattern.
func quoteIfNeeded(v string) string {
	if !needsQuoting(v) {
		return v
	}
	// Escape any single quotes inside v.
	escaped := strings.ReplaceAll(v, `'`, `'\''`)
	return "'" + escaped + "'"
}

func needsQuoting(v string) bool {
	if v == "" {
		// Empty values are fine unquoted (KEY=).
		return false
	}
	for _, c := range v {
		switch c {
		case '$', '`', '"', '\\', ' ', '\t', '#', '&', '|', ';', '<', '>', '(', ')':
			return true
		}
	}
	return false
}

// Save writes the rendered file to path with 0600 permissions (only
// the owner can read it — secrets material).
func (d *dotenvFile) Save(path string) error {
	return os.WriteFile(path, d.Render(), 0o600)
}
