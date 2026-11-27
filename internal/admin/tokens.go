package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mylogger42/mylogger42/internal/auth"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// runIssueToken creates a read-only MCP access token and prints the
// plaintext value once (pipe-safe, single line on stdout).
func runIssueToken(ctx context.Context, out io.Writer, cfg Config, args []string) error {
	label, _ := parseFlag(args, "label")
	if label == "" {
		label = "cli"
	}
	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	issued, err := auth.GenerateReadToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	tok := domain.AccessToken{
		ID:        uuid.NewString(),
		Prefix:    issued.Prefix,
		Label:     label,
		CreatedAt: time.Now().UTC(),
	}
	if err := stores.Tokens.Create(ctx, tok, auth.ComputeHMAC(cfg.KeyPepper, issued.Secret)); err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	fmt.Fprintln(out, issued.Value)
	return nil
}

// runListTokens prints one token per line:
//
//	<id>\t<prefix>\t<label>\t<active|revoked>\t<created>
func runListTokens(ctx context.Context, out io.Writer, cfg Config) error {
	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	toks, err := stores.Tokens.List(ctx, true)
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	for _, t := range toks {
		state := "active"
		if t.RevokedAt != nil {
			state = "revoked"
		}
		fmt.Fprintf(out, "%s\tmlr_%s\t%s\t%s\t%s\n",
			t.ID, t.Prefix, t.Label, state, t.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

// runRevokeToken revokes an MCP token. Idempotent; takes effect on the
// server's next MCP request (token auth is not cached).
func runRevokeToken(ctx context.Context, errw io.Writer, cfg Config, args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("usage: revoke-token <token-id>")
	}
	id := strings.TrimSpace(args[0])
	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	if err := stores.Tokens.Revoke(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("token %q not found", id)
		}
		return fmt.Errorf("revoke token: %w", err)
	}
	fmt.Fprintf(errw, "admin: token %s revoked\n", id)
	return nil
}
