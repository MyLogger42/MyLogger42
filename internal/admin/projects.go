package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silverbaq/mylogger42/internal/auth"
	"github.com/silverbaq/mylogger42/internal/domain"
	"github.com/silverbaq/mylogger42/internal/store"
)

// openStores is the small adapter from admin.Config to store.OpenStores.
// All DB-touching subcommands route through it.
func openStores(ctx context.Context, cfg Config) (*store.Stores, error) {
	stores, err := store.OpenStores(ctx, store.Driver(cfg.DBDriver), cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return stores, nil
}

// parseFlag extracts a `--name=value` flag from args. Returns the value
// (or empty string if absent) and the args with the flag removed. Used
// by the per-command argv parsers to avoid pulling in the `flag` package
// for one or two flags per subcommand.
func parseFlag(args []string, name string) (string, []string) {
	prefix := "--" + name + "="
	out := make([]string, 0, len(args))
	value := ""
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			value = strings.TrimPrefix(a, prefix)
			continue
		}
		out = append(out, a)
	}
	return value, out
}

// hasFlag reports whether `--name` (no value) appears in args, returning
// the args with that token removed.
func hasFlag(args []string, name string) (bool, []string) {
	tok := "--" + name
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == tok {
			found = true
			continue
		}
		out = append(out, a)
	}
	return found, out
}

// runCreateProject creates a project + its first ingest key, prints the
// key value to stdout (machine-readable, single line), and a human
// summary on stderr.
//
//	$ mylogger42 admin create-project auth-svc "Auth Service" --retention=14
//	mlk_<prefix>_<secret>
//	admin: created project "auth-svc" with initial key prefix=...
func runCreateProject(ctx context.Context, out io.Writer, cfg Config, args []string) error {
	retentionStr, args := parseFlag(args, "retention")
	if len(args) < 2 {
		return errors.New("usage: create-project <slug> <name> [--retention=N]")
	}
	slug := strings.TrimSpace(args[0])
	name := strings.TrimSpace(strings.Join(args[1:], " "))

	if err := domain.ValidateSlug(slug); err != nil {
		return err
	}
	if name == "" {
		return errors.New("name must not be empty")
	}
	var retentionDays *int
	if retentionStr != "" {
		n, err := strconv.Atoi(retentionStr)
		if err != nil {
			return fmt.Errorf("--retention: %w", err)
		}
		if n < 0 {
			return errors.New("--retention must be >= 0")
		}
		retentionDays = &n
	}

	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	project := domain.Project{
		ID:            uuid.NewString(),
		Slug:          slug,
		Name:          name,
		RetentionDays: retentionDays,
		CreatedAt:     time.Now().UTC(),
	}
	if err := stores.Projects.Create(ctx, project); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return fmt.Errorf("slug %q already in use", slug)
		}
		return fmt.Errorf("create project: %w", err)
	}

	issued, err := auth.GenerateKey()
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	key := domain.ProjectKey{
		ID:        uuid.NewString(),
		ProjectID: project.ID,
		Prefix:    issued.Prefix,
		Label:     "initial",
		CreatedAt: time.Now().UTC(),
	}
	if err := stores.Keys.Create(ctx, key, auth.ComputeHMAC(cfg.KeyPepper, issued.Secret)); err != nil {
		// Project exists but key issuance failed. Surface the failure;
		// the operator can retry with `issue-key` against the new project.
		return fmt.Errorf("issue first key (project created without key, run `admin issue-key %s` to retry): %w", slug, err)
	}

	// stdout: just the key value (pipe-safe).
	fmt.Fprintln(out, issued.Value)
	return nil
}

// runListProjects prints one project per line in the form:
//   <slug>\t<name>\t<retention>\t<archived?>\t<created>
// Tabs make it pipeable through `cut`/`awk`.
func runListProjects(ctx context.Context, out io.Writer, cfg Config) error {
	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	projects, err := stores.Projects.List(ctx, true /* includeArchived */)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	for _, p := range projects {
		retention := "inherit"
		if p.RetentionDays != nil {
			retention = strconv.Itoa(*p.RetentionDays)
		}
		archived := "active"
		if p.ArchivedAt != nil {
			archived = "archived"
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n",
			p.Slug, p.Name, retention, archived, p.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

// runIssueKey adds a fresh key to an existing project and prints the
// plaintext value (pipe-safe, single line on stdout).
func runIssueKey(ctx context.Context, out io.Writer, cfg Config, args []string) error {
	label, args := parseFlag(args, "label")
	if len(args) < 1 {
		return errors.New("usage: issue-key <slug> [--label=<text>]")
	}
	slug := strings.TrimSpace(args[0])
	if err := domain.ValidateSlug(slug); err != nil {
		return err
	}
	if label == "" {
		label = "cli"
	}

	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	project, err := stores.Projects.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("project %q not found", slug)
		}
		return fmt.Errorf("look up project: %w", err)
	}

	issued, err := auth.GenerateKey()
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	key := domain.ProjectKey{
		ID:        uuid.NewString(),
		ProjectID: project.ID,
		Prefix:    issued.Prefix,
		Label:     label,
		CreatedAt: time.Now().UTC(),
	}
	if err := stores.Keys.Create(ctx, key, auth.ComputeHMAC(cfg.KeyPepper, issued.Secret)); err != nil {
		return fmt.Errorf("create key: %w", err)
	}
	fmt.Fprintln(out, issued.Value)
	return nil
}

// runRevokeKey marks a key revoked. Idempotent.
func runRevokeKey(ctx context.Context, errw io.Writer, cfg Config, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: revoke-key <key-id>")
	}
	keyID := strings.TrimSpace(args[0])
	if keyID == "" {
		return errors.New("key-id must not be empty")
	}

	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	if err := stores.Keys.Revoke(ctx, keyID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("key %q not found", keyID)
		}
		return fmt.Errorf("revoke key: %w", err)
	}
	fmt.Fprintf(errw, "admin: key %s revoked\n", keyID)
	return nil
}

// runDeleteProject hard-deletes a project. Per spec this is the only
// hard-delete path — the API only soft-deletes via archive. ON DELETE
// CASCADE on project_keys takes care of the keys; log_entries are not
// FK-referenced and would be orphaned, so we delete them explicitly
// via DeleteOlderThanForProject(distantFuture) before the project row
// itself.
//
// Requires --force to make the destructive intent explicit.
func runDeleteProject(ctx context.Context, errw io.Writer, cfg Config, args []string) error {
	force, args := hasFlag(args, "force")
	if !force {
		return errors.New("delete-project requires --force")
	}
	if len(args) < 1 {
		return errors.New("usage: delete-project <slug> --force")
	}
	slug := strings.TrimSpace(args[0])
	if err := domain.ValidateSlug(slug); err != nil {
		return err
	}

	stores, err := openStores(ctx, cfg)
	if err != nil {
		return err
	}
	defer stores.Logs.Close()

	project, err := stores.Projects.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("project %q not found", slug)
		}
		return fmt.Errorf("look up project: %w", err)
	}

	// Sweep all log_entries for this project. "Distant future" cutoff
	// means everything qualifies as old enough to delete.
	cutoff := time.Now().UTC().Add(24 * time.Hour) // anything received_at < tomorrow
	if _, err := stores.Logs.DeleteOlderThanForProject(ctx, project.ID, cutoff); err != nil {
		return fmt.Errorf("delete log entries: %w", err)
	}
	if err := stores.Projects.Delete(ctx, slug); err != nil {
		return fmt.Errorf("delete project row: %w", err)
	}
	fmt.Fprintf(errw, "admin: project %s deleted (id=%s)\n", slug, project.ID)
	return nil
}
