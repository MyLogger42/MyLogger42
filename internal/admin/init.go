package admin

import (
	"context"
	"encoding/hex"
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

// runInit is the friendly first-run bootstrapper.
//
//	$ mylogger42 admin init
//	   → reads ./.env (or starts blank)
//	   → fills in KEY_PEPPER, SESSION_SECRET, ADMIN_USERNAME,
//	     ADMIN_PASSWORD_HASH if any are missing
//	   → optionally creates a first project + initial key
//	   → writes .env (0600)
//	   → prints what to do next
//
// Idempotent on already-set values: existing KEY_PEPPER /
// SESSION_SECRET / admin user are preserved by default. --force
// re-rolls the secrets and re-prompts the admin password (still
// preserves any unrelated keys in .env).
//
// --print writes the resulting .env to stdout instead of touching
// disk, so non-interactive flows (CI, Dockerfile RUN) can capture the
// output.
//
// --no-project skips the optional project-creation step.
//
// Flags are parsed positionally to keep this self-contained — the
// stdlib `flag` package would also work but adds noise.
func runInit(ctx context.Context, in io.Reader, out, errw io.Writer, dbCfg Config, args []string) error {
	force, args := hasFlag(args, "force")
	printOnly, args := hasFlag(args, "print")
	noProject, args := hasFlag(args, "no-project")
	envPath, args := parseFlag(args, "env")
	if envPath == "" {
		envPath = ".env"
	}
	_ = args // remaining args reserved for future use

	dotenv, err := parseDotenv(envPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", envPath, err)
	}

	// One shared prompter wraps stdin so subsequent reads see what
	// previous reads left behind. Passing in independently buffered
	// readers across helpers swallows lines unpredictably (the bug
	// caught the first end-to-end smoke).
	pp := newPrompter(in)

	// --- secrets ------------------------------------------------------
	//
	// Pepper + session secret are 32 random bytes each, hex-encoded.
	// Existing values are preserved unless --force or the current value
	// isn't a valid hex secret (e.g. a placeholder from .env.example).
	if force || !dotenv.Has("KEY_PEPPER") || !isValidHexSecret(dotenv.Get("KEY_PEPPER")) {
		v, err := auth.GenerateSecret(32)
		if err != nil {
			return fmt.Errorf("generate KEY_PEPPER: %w", err)
		}
		dotenv.Set("KEY_PEPPER", v)
		fmt.Fprintln(errw, "init: generated KEY_PEPPER")
	} else {
		fmt.Fprintln(errw, "init: KEY_PEPPER already set, preserving")
	}

	if force || !dotenv.Has("SESSION_SECRET") || !isValidHexSecret(dotenv.Get("SESSION_SECRET")) {
		v, err := auth.GenerateSecret(32)
		if err != nil {
			return fmt.Errorf("generate SESSION_SECRET: %w", err)
		}
		dotenv.Set("SESSION_SECRET", v)
		fmt.Fprintln(errw, "init: generated SESSION_SECRET")
	} else {
		fmt.Fprintln(errw, "init: SESSION_SECRET already set, preserving")
	}

	// Defaults that are safe to seed when missing — the operator can
	// still hand-edit .env after.
	dotenv.SetIfMissing("DB_DRIVER", "sqlite")
	dotenv.SetIfMissing("DB_DSN", "./data/mylogger42.db")
	dotenv.SetIfMissing("HTTP_PORT", "8080")
	dotenv.SetIfMissing("RETENTION_DAYS", "30")
	dotenv.SetIfMissing("LOG_LEVEL", "info")
	dotenv.SetIfMissing("TRUST_FORWARDED_PROTO", "false")

	// --- admin user ---------------------------------------------------
	//
	// If both ADMIN_USERNAME and ADMIN_PASSWORD_HASH are set we leave
	// them alone (operator already configured). Otherwise prompt
	// interactively and bcrypt the password. On --print we don't write
	// to .env so the password (a hash, but still) doesn't bleed into a
	// terminal capture by accident — but we DO go through the prompt
	// so the operator can see the resulting hash and copy it manually.
	if force || !dotenv.Has("ADMIN_USERNAME") || !dotenv.Has("ADMIN_PASSWORD_HASH") {
		username, hash, err := promptAdminUser(pp, errw, dotenv.Get("ADMIN_USERNAME"))
		if err != nil {
			return err
		}
		dotenv.Set("ADMIN_USERNAME", username)
		dotenv.Set("ADMIN_PASSWORD_HASH", hash)
		fmt.Fprintln(errw, "init: admin user configured")
	} else {
		fmt.Fprintln(errw, "init: ADMIN_USERNAME + ADMIN_PASSWORD_HASH already set, preserving")
	}

	// --- write or print -----------------------------------------------
	if printOnly {
		_, err := out.Write(dotenv.Render())
		if err != nil {
			return fmt.Errorf("print: %w", err)
		}
		fmt.Fprintln(errw, "init: --print mode (no files written)")
		return nil
	}
	if err := dotenv.Save(envPath); err != nil {
		return fmt.Errorf("write %s: %w", envPath, err)
	}
	fmt.Fprintf(errw, "init: wrote %s\n", envPath)

	// --- optional first project --------------------------------------
	//
	// We need the freshly-generated KEY_PEPPER to issue a key, so this
	// only runs after the .env is settled. Skipped on --no-project or
	// --print (the latter has no DB access yet anyway since we don't
	// know if the operator's about to start the server).
	if noProject || printOnly {
		printNextSteps(errw, false)
		return nil
	}

	wantProject, err := promptYesNo(pp, errw, "Create a first project now? [y/N] ", false)
	if err != nil {
		return err
	}
	if !wantProject {
		printNextSteps(errw, false)
		return nil
	}

	pepperHex := dotenv.Get("KEY_PEPPER")
	pepper, err := hexDecode(pepperHex)
	if err != nil {
		return fmt.Errorf("decode KEY_PEPPER: %w", err)
	}
	keyValue, err := createFirstProject(ctx, pp, errw, Config{
		DBDriver:  dotenv.Get("DB_DRIVER"),
		DBDSN:     dotenv.Get("DB_DSN"),
		KeyPepper: pepper,
	})
	if err != nil {
		return fmt.Errorf("create first project: %w", err)
	}

	fmt.Fprintln(errw)
	fmt.Fprintln(errw, "Your initial ingest key — copy it now, it won't be shown again:")
	fmt.Fprintln(errw)
	fmt.Fprintln(out, keyValue)
	fmt.Fprintln(errw)
	printNextSteps(errw, true)

	// dbCfg is unused but kept on the signature so the dispatch shape
	// matches the other DB-touching subcommands.
	_ = dbCfg
	return nil
}

// promptAdminUser collects username + password (no-echo when stdin is
// a tty) and returns the bcrypt hash. Username defaults to the
// existing value if one was already set, so re-running init doesn't
// silently rename the admin.
func promptAdminUser(pp *prompter, errw io.Writer, existingUsername string) (username, hash string, err error) {
	prompt := "admin username"
	if existingUsername != "" {
		prompt += fmt.Sprintf(" [%s]", existingUsername)
	}
	prompt += ": "
	username, err = pp.promptLine(errw, prompt)
	if err != nil {
		return "", "", err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = existingUsername
	}
	if username == "" {
		return "", "", errors.New("username must not be empty")
	}

	pw, err := pp.promptPassword(errw, "admin password: ")
	if err != nil {
		return "", "", err
	}
	if pw == "" {
		return "", "", errors.New("password must not be empty")
	}
	confirm, err := pp.promptPassword(errw, "confirm:        ")
	if err != nil {
		return "", "", err
	}
	if confirm != pw {
		return "", "", errors.New("passwords do not match")
	}
	hash, err = auth.HashPassword(pw)
	if err != nil {
		return "", "", fmt.Errorf("hash password: %w", err)
	}
	return username, hash, nil
}

// promptYesNo reads a y/n answer with a default. Defaults are picked
// for the friendly path: "Create a first project now?" → yes, since
// the operator running `admin init` almost certainly wants one.
func promptYesNo(pp *prompter, errw io.Writer, prompt string, defaultYes bool) (bool, error) {
	line, err := pp.promptLine(errw, prompt)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("answer must be y or n")
	}
}

// createFirstProject prompts for slug + name, opens the store, creates
// the project + an "initial" key. Returns the plaintext key value.
//
// Slug is validated up-front so a typo doesn't produce a confusing DB
// error. Retention is offered as an optional override.
func createFirstProject(ctx context.Context, pp *prompter, errw io.Writer, cfg Config) (string, error) {
	slug, err := pp.promptLine(errw, "project slug (e.g. auth-service): ")
	if err != nil {
		return "", err
	}
	slug = strings.TrimSpace(slug)
	if err := domain.ValidateSlug(slug); err != nil {
		return "", err
	}

	name, err := pp.promptLine(errw, "project name (e.g. \"Auth Service\"): ")
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name must not be empty")
	}

	retentionStr, err := pp.promptLine(errw, "retention days (blank = inherit global default): ")
	if err != nil {
		return "", err
	}
	var retention *int
	if r := strings.TrimSpace(retentionStr); r != "" {
		n, err := strconv.Atoi(r)
		if err != nil {
			return "", fmt.Errorf("retention: %w", err)
		}
		if n < 0 {
			return "", errors.New("retention must be >= 0")
		}
		retention = &n
	}

	stores, err := openStores(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer stores.Logs.Close()

	project := domain.Project{
		ID:            uuid.NewString(),
		Slug:          slug,
		Name:          name,
		RetentionDays: retention,
		CreatedAt:     time.Now().UTC(),
	}
	if err := stores.Projects.Create(ctx, project); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return "", fmt.Errorf("slug %q already in use", slug)
		}
		return "", fmt.Errorf("create project: %w", err)
	}

	issued, err := auth.GenerateKey()
	if err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	rec := domain.ProjectKey{
		ID:        uuid.NewString(),
		ProjectID: project.ID,
		Prefix:    issued.Prefix,
		Label:     "initial",
		CreatedAt: time.Now().UTC(),
	}
	if err := stores.Keys.Create(ctx, rec, auth.ComputeHMAC(cfg.KeyPepper, issued.Secret)); err != nil {
		return "", fmt.Errorf("issue key: %w", err)
	}
	return issued.Value, nil
}

// printNextSteps tells the operator what to do once init is done. The
// hint changes slightly when a project was created vs not.
func printNextSteps(errw io.Writer, hadProject bool) {
	fmt.Fprintln(errw)
	fmt.Fprintln(errw, "Next steps:")
	fmt.Fprintln(errw, "  1. Source the env and start the server:")
	fmt.Fprintln(errw, "       set -a; source .env; set +a")
	fmt.Fprintln(errw, "       ./bin/mylogger42")
	fmt.Fprintln(errw, "  2. Open http://localhost:8080/ and log in.")
	if hadProject {
		fmt.Fprintln(errw, "  3. Send a test event with the key above:")
		fmt.Fprintln(errw, "       curl -X POST http://localhost:8080/api/v1/logs \\")
		fmt.Fprintln(errw, "         -H \"X-API-Key: <paste-key-here>\" \\")
		fmt.Fprintln(errw, "         -H \"Content-Type: application/json\" \\")
		fmt.Fprintln(errw, "         -d '{\"service\":\"demo\",\"level\":\"Info\",\"message\":\"hi\"}'")
	} else {
		fmt.Fprintln(errw, "  3. Create a project + key when ready:")
		fmt.Fprintln(errw, "       ./bin/mylogger42 admin create-project <slug> <name>")
	}
}

// hexDecode is a small wrapper around encoding/hex.DecodeString — kept
// here so the createFirstProject path doesn't need yet another
// auth-package helper for one call site.
func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// isValidHexSecret reports whether s is a valid hex-encoded secret of
// at least 32 bytes. Used to detect placeholder values from .env.example
// (e.g. "replace-me-with-64-hex-chars") so init regenerates them instead
// of silently preserving an unusable value.
func isValidHexSecret(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) >= 32
}
