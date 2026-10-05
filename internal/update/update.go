// Package update evaluates nixpkgs with nix-eval-jobs and writes packages,
// maintainers, teams and direct dependencies into a SQLite database.
package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	DB        string
	Channel   string // e.g. nixos-unstable; used when Nixpkgs is empty
	System    string
	Workers   int
	MaxMemory int
	Force     bool

	Nixpkgs string // use this nixpkgs source instead of fetching the channel
	Rev     string // revision recorded for Nixpkgs
	Subset  string // Nix function selecting the package set to evaluate

	EvalJobs string // nix-eval-jobs binary
}

func Run(ctx context.Context, o Options) error {
	start := time.Now()
	src, rev := o.Nixpkgs, o.Rev
	if src == "" {
		var err error
		if rev, err = channelRev(ctx, o.Channel); err != nil {
			return err
		}
		log.Printf("channel %s is at %s", o.Channel, rev)
	}
	if old := ReadMeta(o.DB); !o.Force && old != nil &&
		old["rev"] == rev && old["system"] == o.System && old["schema_version"] == SchemaVersion {
		log.Printf("%s is already up to date (%s), nothing to do", o.DB, rev)
		return nil
	}
	if src == "" {
		var err error
		if src, err = fetchNixpkgs(ctx, rev); err != nil {
			return err
		}
		log.Printf("fetched nixpkgs %s to %s", rev, src)
	}

	t := time.Now()
	c := newCollector()
	err := runEval(ctx, evalOptions{
		Nixpkgs:   src,
		System:    o.System,
		Subset:    o.Subset,
		Workers:   o.Workers,
		MaxMemory: o.MaxMemory,
		Binary:    o.EvalJobs,
	}, c.add)
	if err != nil {
		return fmt.Errorf("evaluation failed: %w", err)
	}
	evalSeconds := time.Since(t).Seconds()
	log.Printf("eval: %d derivations, %d errors, %d unsupported on %s in %.0fs", len(c.byDrv), len(c.errors), c.unsupported, o.System, evalSeconds)
	if len(c.byDrv) == 0 {
		return fmt.Errorf("evaluation produced no derivations, keeping the existing database")
	}

	t = time.Now()
	g := c.build()
	log.Printf("graph: %d packages, %d edges in %.1fs", len(g.Pkgs), g.Edges, time.Since(t).Seconds())

	t = time.Now()
	meta := map[string]string{
		"schema_version":   SchemaVersion,
		"rev":              rev,
		"channel":          o.Channel,
		"system":           o.System,
		"generated_at":     time.Now().UTC().Format(time.RFC3339),
		"eval_seconds":     strconv.Itoa(int(evalSeconds)),
		"package_count":    strconv.Itoa(len(g.Pkgs)),
		"edge_count":       strconv.Itoa(g.Edges),
		"eval_error_count": strconv.Itoa(len(g.Errors)),
	}
	if o.Nixpkgs != "" {
		meta["channel"] = ""
	}
	if err := writeDB(o.DB, g, meta, src); err != nil {
		return fmt.Errorf("writing database: %w", err)
	}
	log.Printf("wrote %s in %.1fs (total %.0fs)", o.DB, time.Since(t).Seconds(), time.Since(start).Seconds())
	return nil
}

func channelRev(ctx context.Context, channel string) (string, error) {
	url := "https://channels.nixos.org/" + channel + "/git-revision"
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK || len(rev) != 40 {
		return "", fmt.Errorf("GET %s: %s %q", url, resp.Status, rev)
	}
	return rev, nil
}

// fetchNixpkgs downloads the nixpkgs source for rev into the Nix store and
// returns its store path.
func fetchNixpkgs(ctx context.Context, rev string) (string, error) {
	url := "https://github.com/NixOS/nixpkgs/archive/" + rev + ".tar.gz"
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "nix-prefetch-url", "--unpack", "--print-path", url)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("nix-prefetch-url %s: %w: %s", url, err, stderr.String())
	}
	// Output: hash on the first line, store path on the second.
	lines := strings.Fields(stdout.String())
	if len(lines) != 2 {
		return "", fmt.Errorf("unexpected nix-prefetch-url output: %q", stdout.String())
	}
	return lines[1], nil
}
