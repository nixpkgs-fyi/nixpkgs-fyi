// Command nixmaint extracts packages, maintainers and dependencies from nixpkgs
// into SQLite (nixmaint update) and serves a web view of it (nixmaint serve).
// It is configured through NIXMAINT_* environment variables.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"

	"github.com/nixpkgs-fyi/nixpkgs-fyi/internal/update"
	"github.com/nixpkgs-fyi/nixpkgs-fyi/internal/web"
)

var defaultWorkers = strconv.Itoa(min(runtime.NumCPU(), 8))

const usage = `usage:
  nixmaint update   evaluate nixpkgs and (re)generate the database
  nixmaint serve    serve the web interface

Configuration (environment variables):
  NIXMAINT_DB              database file (default: nixpkgs.sqlite)

  serve:
  NIXMAINT_LISTEN          address to listen on (default: 127.0.0.1:8787)
  NIXMAINT_BANNER          "0" or "false" hides the "built with ❤️ by pinpox" footer (default: true)

  update:
  NIXMAINT_CHANNEL         channel whose current revision is evaluated (default: nixos-unstable)
  NIXMAINT_SYSTEM          system to evaluate packages for (default: x86_64-linux)
  NIXMAINT_WORKERS         nix-eval-jobs workers (default: number of CPUs, at most 8)
  NIXMAINT_MAX_MEMORY      memory per nix-eval-jobs worker in MiB (default: 4096)
  NIXMAINT_FORCE           "1" or "true": regenerate even if the database is at the channel revision
  NIXMAINT_NIXPKGS         evaluate this nixpkgs checkout instead of fetching the channel
                           (requires NIXMAINT_REV)
  NIXMAINT_REV             revision to record for NIXMAINT_NIXPKGS
  NIXMAINT_SUBSET          Nix function selecting the attribute set to evaluate, for testing
                           (default: pkgs: pkgs)
  NIXMAINT_NIX_EVAL_JOBS   nix-eval-jobs binary (default: nix-eval-jobs)
`

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if len(os.Args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "update":
		err = runUpdate(ctx)
	case "serve":
		err = runServe(ctx)
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func runUpdate(ctx context.Context) error {
	o := update.Options{
		DB:       env("NIXMAINT_DB", "nixpkgs.sqlite"),
		Channel:  env("NIXMAINT_CHANNEL", "nixos-unstable"),
		System:   env("NIXMAINT_SYSTEM", "x86_64-linux"),
		Nixpkgs:  env("NIXMAINT_NIXPKGS", ""),
		Rev:      env("NIXMAINT_REV", ""),
		Subset:   env("NIXMAINT_SUBSET", "pkgs: pkgs"),
		EvalJobs: env("NIXMAINT_NIX_EVAL_JOBS", "nix-eval-jobs"),
	}
	var err error
	if o.Workers, err = envInt("NIXMAINT_WORKERS", defaultWorkers); err != nil {
		return err
	}
	if o.MaxMemory, err = envInt("NIXMAINT_MAX_MEMORY", "4096"); err != nil {
		return err
	}
	if o.Force, err = envBool("NIXMAINT_FORCE", "false"); err != nil {
		return err
	}
	if (o.Nixpkgs == "") != (o.Rev == "") {
		return fmt.Errorf("NIXMAINT_NIXPKGS and NIXMAINT_REV must be set together")
	}
	return update.Run(ctx, o)
}

func runServe(ctx context.Context) error {
	banner, err := envBool("NIXMAINT_BANNER", "true")
	if err != nil {
		return err
	}
	return web.Serve(ctx, web.Options{
		Listen: env("NIXMAINT_LISTEN", "127.0.0.1:8787"),
		DB:     env("NIXMAINT_DB", "nixpkgs.sqlite"),
		Banner: banner,
	})
}

// env returns the variable's value, or def if it is unset or empty.
func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name, def string) (int, error) {
	v := env(name, def)
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s=%q: want a positive integer", name, v)
	}
	return n, nil
}

func envBool(name, def string) (bool, error) {
	v := env(name, def)
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s=%q: want true or false", name, v)
	}
	return b, nil
}
