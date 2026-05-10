package admin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silverbaq/mylogger42/internal/auth"
	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store"
	"golang.org/x/term"
)

// promptLine reads one line from in, trimming trailing newline. Echoes
// stay enabled — used for non-secret prompts like usernames.
func promptLine(in io.Reader, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// promptPassword reads a password without echo if stdin is a terminal,
// or reads a single line normally if it isn't (CI / pipe). Returns an
// empty error for empty input — caller decides how to handle.
func promptPassword(in *os.File, out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)

	// term.ReadPassword needs a file descriptor of a terminal.
	if term.IsTerminal(int(in.Fd())) {
		b, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(out) // newline after the silent input
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(b), nil
	}
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// runCreateUser is the interactive admin-user creation flow.
//
//	$ mylogger42 admin create-user
//	username: admin
//	password: ******
//	confirm:  ******
//	admin: created user "admin" (id=...)
//
// Refuses to create a second user — v2 is single-admin, and a stray
// second row would shadow the first in unpredictable ways. Operators
// who want to rotate the admin should use set-password instead.
func runCreateUser(ctx context.Context, in io.Reader, out, errw io.Writer, cfg Config) error {
	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	count, err := stores.Users.Count(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return errors.New("a user already exists; use `admin set-password` to rotate the admin password")
	}

	username, err := promptLine(in, errw, "username: ")
	if err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username must not be empty")
	}

	stdin, _ := in.(*os.File)
	if stdin == nil {
		stdin = os.Stdin
	}
	pw, err := promptPassword(stdin, errw, "password: ")
	if err != nil {
		return err
	}
	if pw == "" {
		return errors.New("password must not be empty")
	}
	confirm, err := promptPassword(stdin, errw, "confirm:  ")
	if err != nil {
		return err
	}
	if confirm != pw {
		return errors.New("passwords do not match")
	}

	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user := domain.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	if err := stores.Users.Create(ctx, user); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return fmt.Errorf("username %q already in use", username)
		}
		return fmt.Errorf("create user: %w", err)
	}
	fmt.Fprintf(errw, "admin: created user %q (id=%s)\n", user.Username, user.ID)
	fmt.Fprintln(out, user.ID)
	return nil
}

// runSetPassword interactively replaces an existing user's password.
//
//	$ mylogger42 admin set-password admin
//	new password: ******
//	confirm:      ******
//	admin: password updated for "admin"
//
// Reasonable to chain with SESSION_SECRET rotation if rotating because
// of compromise — see the spec note in session.go.
func runSetPassword(ctx context.Context, in io.Reader, errw io.Writer, cfg Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: set-password <username>")
	}
	username := strings.TrimSpace(args[0])
	if username == "" {
		return errors.New("username must not be empty")
	}

	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	if _, err := stores.Users.GetByUsername(ctx, username); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("user %q not found", username)
		}
		return fmt.Errorf("look up user: %w", err)
	}

	stdin, _ := in.(*os.File)
	if stdin == nil {
		stdin = os.Stdin
	}
	pw, err := promptPassword(stdin, errw, "new password: ")
	if err != nil {
		return err
	}
	if pw == "" {
		return errors.New("password must not be empty")
	}
	confirm, err := promptPassword(stdin, errw, "confirm:      ")
	if err != nil {
		return err
	}
	if confirm != pw {
		return errors.New("passwords do not match")
	}

	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := stores.Users.UpdatePassword(ctx, username, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	fmt.Fprintf(errw, "admin: password updated for %q\n", username)
	return nil
}
