// Command nixmaint extracts packages, maintainers and dependencies from nixpkgs
// into SQLite (nixmaint update) and serves a web view of it (nixmaint serve).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/pinpox/nixpkgs-maintenance/internal/update"
	"github.com/pinpox/nixpkgs-maintenance/internal/web"
)

const usage = `usage:
  nixmaint update [flags]   evaluate nixpkgs and (re)generate the database
  nixmaint serve [flags]    serve the web interface

Run "nixmaint <command> -h" for the flags of a command.
`

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "update":
		err = runUpdate(ctx, os.Args[2:])
	case "serve":
		err = runServe(ctx, os.Args[2:])
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

func runUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	var o update.Options
	fs.StringVar(&o.DB, "db", "nixpkgs.sqlite", "database file to (re)generate")
	fs.StringVar(&o.Channel, "channel", "nixos-unstable", "channel whose current revision is evaluated")
	fs.StringVar(&o.System, "system", "x86_64-linux", "system to evaluate packages for")
	fs.IntVar(&o.Workers, "workers", min(runtime.NumCPU(), 8), "nix-eval-jobs workers")
	fs.IntVar(&o.MaxMemory, "max-memory", 4096, "memory per nix-eval-jobs worker in MiB")
	fs.BoolVar(&o.Force, "force", false, "regenerate even if the database is already at the channel revision")
	fs.StringVar(&o.Nixpkgs, "nixpkgs", "", "evaluate this nixpkgs checkout instead of fetching the channel (requires -rev)")
	fs.StringVar(&o.Rev, "rev", "", "revision to record for -nixpkgs")
	fs.StringVar(&o.Subset, "subset", "pkgs: pkgs", "Nix function selecting the attribute set to evaluate (for testing)")
	fs.StringVar(&o.EvalJobs, "nix-eval-jobs", "nix-eval-jobs", "nix-eval-jobs binary")
	_ = fs.Parse(args)
	if (o.Nixpkgs == "") != (o.Rev == "") {
		return fmt.Errorf("-nixpkgs and -rev must be given together")
	}
	return update.Run(ctx, o)
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	db := fs.String("db", "nixpkgs.sqlite", "database file written by nixmaint update")
	listen := fs.String("listen", "127.0.0.1:8787", "address to listen on")
	_ = fs.Parse(args)
	return web.Serve(ctx, *listen, *db)
}
