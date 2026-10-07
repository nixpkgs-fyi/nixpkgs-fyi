package update

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Repology (https://repology.org) compares package versions across
// distributions. Only projects that are outdated in the nixpkgs repository
// are fetched: the API asks not to be used for bulk downloads, and those
// ~18k projects (~90 requests) already carry every actionable result.
const (
	repologyUserAgent = "nixmaint (+https://github.com/nixpkgs-fyi/nixpkgs-fyi)"
	repologyInterval  = time.Second // API rule: at most one request per second
)

// repologyPackage is one nixpkgs package of an outdated Repology project.
type repologyPackage struct {
	Attr        string // srcname: the attribute path
	Project     string
	Status      string // Repology status of the package, e.g. outdated, legacy, newest
	Version     string // version Repology saw in nixpkgs, normalised
	OrigVersion string // ... as written in nixpkgs, if different
	Newest      string // newest version of the project known to Repology
}

type repologyAPIPackage struct {
	Repo        string  `json:"repo"`
	SrcName     string  `json:"srcname"`
	Version     string  `json:"version"`
	OrigVersion *string `json:"origversion"`
	Status      string  `json:"status"`
}

var stableChannel = regexp.MustCompile(`^nix(?:os|pkgs)-(\d\d)\.(\d\d)(?:-|$)`)

// repologyRepo returns the Repology repository tracking a channel:
// nix_unstable for nixos-unstable and nixpkgs-unstable(-small),
// nix_stable_YY_MM for nixos-YY.MM and friends.
func repologyRepo(channel string) (string, error) {
	if strings.HasPrefix(channel, "nixos-unstable") || strings.HasPrefix(channel, "nixpkgs-unstable") {
		return "nix_unstable", nil
	}
	if m := stableChannel.FindStringSubmatch(channel); m != nil {
		return "nix_stable_" + m[1] + "_" + m[2], nil
	}
	return "", fmt.Errorf("no Repology repository known for channel %q, set NIXMAINT_REPOLOGY_REPO", channel)
}

// fetchRepology returns the packages of repo in all projects Repology
// considers outdated in repo.
func fetchRepology(ctx context.Context, apiURL, repo string) ([]repologyPackage, error) {
	var out []repologyPackage
	last := ""
	for requests := 0; ; requests++ {
		if requests > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(repologyInterval):
			}
		}
		// Pages hold up to 200 projects starting at the given one (inclusive).
		u := strings.TrimSuffix(apiURL, "/") + "/projects/"
		if last != "" {
			u += url.PathEscape(last) + "/"
		}
		u += "?" + url.Values{"inrepo": {repo}, "outdated": {"1"}}.Encode()
		page, err := getRepologyPage(ctx, u)
		if err != nil {
			return nil, err
		}
		next := last
		for project, pkgs := range page {
			if project <= last {
				continue
			}
			next = max(next, project)
			out = append(out, projectPackages(project, repo, pkgs)...)
		}
		if next == last {
			log.Printf("repology: %d %s packages in outdated projects (%d requests)", len(out), repo, requests+1)
			return out, nil
		}
		last = next
	}
}

func getRepologyPage(ctx context.Context, u string) (map[string][]repologyAPIPackage, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", repologyUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	var page map[string][]repologyAPIPackage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	return page, nil
}

// projectPackages picks the packages of repo out of one project. The newest
// version is the one most packages with status "newest" agree on (spellings
// like 1.2 and 1.2.0 compare equal in Repology).
func projectPackages(project, repo string, pkgs []repologyAPIPackage) []repologyPackage {
	votes := map[string]int{}
	newest := ""
	for _, p := range pkgs {
		if p.Status != "newest" {
			continue
		}
		votes[p.Version]++
		if n := votes[p.Version]; n > votes[newest] || n == votes[newest] && p.Version > newest {
			newest = p.Version
		}
	}
	var out []repologyPackage
	for _, p := range pkgs {
		if p.Repo != repo || p.SrcName == "" {
			continue
		}
		rp := repologyPackage{Attr: p.SrcName, Project: project, Status: p.Status, Version: p.Version, Newest: newest}
		if p.OrigVersion != nil {
			rp.OrigVersion = *p.OrigVersion
		}
		out = append(out, rp)
	}
	return out
}

// insertRepology matches Repology packages to packages (by attribute or
// alias) and records their status. Repology may have seen a different
// nixpkgs revision: a status is only trusted if the versions agree, and a
// package already at the newest version counts as newest.
func insertRepology(tx *sql.Tx, rps []repologyPackage) (matched int, err error) {
	lookup, err := tx.Prepare(`SELECT id, version FROM packages WHERE attr = ?
		UNION ALL SELECT p.id, p.version FROM aliases a JOIN packages p ON p.id = a.package_id WHERE a.attr = ? LIMIT 1`)
	if err != nil {
		return 0, err
	}
	defer lookup.Close()
	insert, err := tx.Prepare(`INSERT OR IGNORE INTO repology (package_id, project, status, newest) VALUES (?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer insert.Close()
	for _, rp := range rps {
		var id int
		var version string
		switch err := lookup.QueryRow(rp.Attr, rp.Attr).Scan(&id, &version); err {
		case nil:
		case sql.ErrNoRows:
			continue
		default:
			return 0, err
		}
		var status string
		switch {
		case version == "":
			continue
		case version == rp.Version || version == rp.OrigVersion:
			status = rp.Status
		case version == rp.Newest:
			status = "newest"
		default:
			continue
		}
		res, err := insert.Exec(id, rp.Project, status, rp.Newest)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			matched++
		}
	}
	return matched, nil
}

// repologyMeta returns the meta entries describing fetched Repology data.
func repologyMeta(repo string, fetched time.Time) map[string]string {
	return map[string]string{
		"repology_repo":       repo,
		"repology_fetched_at": fetched.UTC().Format(time.RFC3339),
	}
}

// refreshRepology replaces the Repology data of an existing database. Like
// writeDB it works on a copy that atomically replaces path, so readers never
// see a partial update.
func refreshRepology(path string, rps []repologyPackage, meta map[string]string) (err error) {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	src, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	_, err = src.Exec(`VACUUM INTO ?`, tmp)
	src.Close()
	if err != nil {
		return fmt.Errorf("copying %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", "file:"+tmp+"?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM repology`); err != nil {
		return err
	}
	matched, err := insertRepology(tx, rps)
	if err != nil {
		return err
	}
	for k, v := range meta {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES (?,?)`, k, v); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`ANALYZE repology; PRAGMA journal_mode=DELETE;`); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	log.Printf("repology: matched %d packages", matched)
	return os.Rename(tmp, path)
}
