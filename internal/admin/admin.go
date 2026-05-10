// Package admin implements the `mylogger42 admin <subcommand>` CLI
// surface. Subcommands are dispatched from cmd/server/main.go when
// os.Args[1] == "admin".
//
// The CLI shares the v2 store + auth packages — there is no separate
// data path to keep in sync. Output convention:
//
//   - stdout: machine-readable values (one per line where applicable)
//   - stderr: human-facing logs and prompts
//   - non-zero exit on error
//
// Subcommands that don't touch the DB (generate-secrets, hash-password)
// don't read .env at all. Subcommands that touch the DB load config
// the same way the server does — KEY_PEPPER and SESSION_SECRET must be
// set even though most CLI flows don't actively use them, because we
// share loadConfig with the server to keep the env-validation surface
// consistent.
package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Run dispatches the subcommand named by args[0]. argv mirrors what
// cmd/server/main.go passes when it sees os.Args[1] == "admin": the
// caller has already trimmed off the program name + "admin" so args[0]
// is the subcommand itself.
//
// cfg is the loaded server config — provides KEY_PEPPER, DB_DSN, etc.
// to subcommands that talk to the DB.
func Run(args []string, cfg Config) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return errors.New("admin: subcommand required")
	}
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "init":
		return runInit(context.Background(), os.Stdin, os.Stdout, os.Stderr, cfg, rest)
	case "generate-secrets":
		return runGenerateSecrets(os.Stdout)
	case "hash-password":
		return runHashPassword(os.Stdin, os.Stdout)
	case "create-user":
		return runCreateUser(context.Background(), os.Stdin, os.Stdout, os.Stderr, cfg)
	case "set-password":
		return runSetPassword(context.Background(), os.Stdin, os.Stderr, cfg, rest)
	case "create-project":
		return runCreateProject(context.Background(), os.Stdout, cfg, rest)
	case "list-projects":
		return runListProjects(context.Background(), os.Stdout, cfg)
	case "issue-key":
		return runIssueKey(context.Background(), os.Stdout, cfg, rest)
	case "revoke-key":
		return runRevokeKey(context.Background(), os.Stderr, cfg, rest)
	case "delete-project":
		return runDeleteProject(context.Background(), os.Stderr, cfg, rest)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("admin: unknown subcommand %q", cmd)
	}
}

// Config is the projection of the server's config that the admin
// subcommands need. Defining it here (rather than importing the
// unexported main package config) keeps cmd/server/main.go free to
// evolve its loadConfig without breaking us.
type Config struct {
	DBDriver  string
	DBDSN     string
	KeyPepper []byte
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, strings.TrimSpace(`
mylogger42 admin <subcommand>

Subcommands:
  init [--force] [--print] [--no-project] [--env=PATH]
                                 friendly first-run bootstrap: writes .env,
                                 prompts for admin user, optionally creates
                                 a first project + key
  generate-secrets               print fresh KEY_PEPPER + SESSION_SECRET
  hash-password                  read a password on stdin, print bcrypt hash on stdout
  create-user                    interactive: prompts for username + password
  set-password <username>        interactive: prompts for new password
  create-project <slug> <name> [--retention=N]
  list-projects
  issue-key <slug> [--label=...] print the freshly-issued key value once
  revoke-key <key-id>
  delete-project <slug> --force  hard delete (NOT exposed via API)

The DB-touching subcommands open the same database the server uses
(DB_DSN, DB_DRIVER). KEY_PEPPER and SESSION_SECRET must be set just
as for the server, so subcommands run with the same env block.
`))
}
