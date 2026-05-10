package admin

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/silverbaq/mylogger42/internal/auth"
)

// runGenerateSecrets prints fresh hex values for KEY_PEPPER and
// SESSION_SECRET (32 random bytes each). The shape is suitable for
// pasting straight into .env — `KEY_PEPPER=<hex>` and
// `SESSION_SECRET=<hex>` lines are emitted.
func runGenerateSecrets(out io.Writer) error {
	pepper, err := auth.GenerateSecret(32)
	if err != nil {
		return fmt.Errorf("generate KEY_PEPPER: %w", err)
	}
	session, err := auth.GenerateSecret(32)
	if err != nil {
		return fmt.Errorf("generate SESSION_SECRET: %w", err)
	}
	fmt.Fprintf(out, "KEY_PEPPER=%s\nSESSION_SECRET=%s\n", pepper, session)
	return nil
}

// runHashPassword reads a password from stdin (one line, terminating
// newline trimmed) and prints its bcrypt hash on stdout. Suitable for
//
//   echo -n 'hunter2' | mylogger42 admin hash-password
//
// or interactive use. We don't try to read terminal-without-echo here
// because the typical use-case is piped — non-interactive setups read
// the password from a secrets manager / CI.
//
// The interactive create-user / set-password subcommands DO use
// no-echo terminal reads (golang.org/x/term).
func runHashPassword(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		return fmt.Errorf("read password: empty input")
	}
	pw := strings.TrimRight(scanner.Text(), "\r\n")
	if pw == "" {
		return fmt.Errorf("password must not be empty")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	fmt.Fprintln(out, hash)
	return nil
}
