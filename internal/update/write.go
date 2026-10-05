package update

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// SchemaVersion is bumped whenever schema.sql or the meaning of a column
// changes; a database with a different version is always regenerated.
const SchemaVersion = "2"

//go:embed schema.sql
var schemaSQL string

const indexSQL = `
CREATE INDEX packages_package_set ON packages(package_set);
CREATE INDEX packages_maintainer_count ON packages(maintainer_count, team_count);
CREATE INDEX packages_rdep_count ON packages(rdep_count);
CREATE INDEX packages_rdep_transitive_count ON packages(rdep_transitive_count);
CREATE INDEX aliases_package ON aliases(package_id);
CREATE INDEX maintainers_github ON maintainers(github COLLATE NOCASE);
CREATE INDEX package_maintainers_maintainer ON package_maintainers(maintainer_id, package_id);
CREATE INDEX team_members_maintainer ON team_members(maintainer_id);
CREATE INDEX package_teams_team ON package_teams(team_id, package_id);
CREATE INDEX dependencies_dep ON dependencies(dep_id, package_id);
`

// ReadMeta returns the meta table of an existing database, or nil if the
// database does not exist or cannot be read.
func ReadMeta(path string) map[string]string {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			m[k] = v
		}
	}
	return m
}

// writeDB writes the graph to path+".tmp" and atomically renames it to path.
func writeDB(path string, g *graph, meta map[string]string, sourceDir string) (err error) {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	db, err := sql.Open("sqlite", "file:"+tmp+"?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertAll(tx, g, meta, sourceDir); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(indexSQL); err != nil {
		return fmt.Errorf("creating indexes: %w", err)
	}
	if _, err := db.Exec(`ANALYZE; PRAGMA journal_mode=DELETE;`); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type inserter struct {
	tx    *sql.Tx
	stmts map[string]*sql.Stmt
	err   error
}

func (in *inserter) exec(query string, args ...any) {
	if in.err != nil {
		return
	}
	st, ok := in.stmts[query]
	if !ok {
		st, in.err = in.tx.Prepare(query)
		if in.err != nil {
			return
		}
		in.stmts[query] = st
	}
	if _, err := st.Exec(args...); err != nil {
		in.err = fmt.Errorf("%.60s: %w", strings.Join(strings.Fields(query), " "), err)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func maintainerKey(p person) string {
	switch {
	case p.GitHubID != 0:
		return "id:" + strconv.FormatInt(p.GitHubID, 10)
	case p.GitHub != "":
		return "gh:" + strings.ToLower(p.GitHub)
	default:
		return "name:" + p.Name
	}
}

func handle(p person) string {
	if p.GitHub != "" {
		return p.GitHub
	}
	return p.Name
}

func insertAll(tx *sql.Tx, g *graph, meta map[string]string, sourceDir string) error {
	in := &inserter{tx: tx, stmts: map[string]*sql.Stmt{}}
	defer func() {
		for _, st := range in.stmts {
			st.Close()
		}
	}()

	maintainerIDs := map[string]int{}
	maintainerID := func(p person) (int, bool) {
		if p.GitHub == "" && p.Name == "" {
			return 0, false
		}
		key := maintainerKey(p)
		if id, ok := maintainerIDs[key]; ok {
			return id, true
		}
		id := len(maintainerIDs)
		maintainerIDs[key] = id
		in.exec(`INSERT INTO maintainers (id, key, handle, name, github, github_id, email, matrix) VALUES (?,?,?,?,?,?,?,?)`,
			id, key, handle(p), nullable(p.Name), nullable(p.GitHub), nullableInt(p.GitHubID), nullable(p.Email), nullable(p.Matrix))
		return id, true
	}
	teamIDs := map[string]int{}
	teamID := func(t team) (int, bool) {
		if t.ShortName == "" {
			return 0, false
		}
		if id, ok := teamIDs[t.ShortName]; ok {
			return id, true
		}
		id := len(teamIDs)
		teamIDs[t.ShortName] = id
		in.exec(`INSERT INTO teams (id, short_name, github, scope) VALUES (?,?,?,?)`,
			id, t.ShortName, nullable(t.GitHub), nullable(t.Scope))
		for _, m := range t.Members {
			if mid, ok := maintainerID(m); ok {
				in.exec(`INSERT OR IGNORE INTO team_members (team_id, maintainer_id) VALUES (?,?)`, id, mid)
			}
		}
		return id, true
	}

	prefix := strings.TrimSuffix(sourceDir, "/") + "/"
	for _, p := range g.Pkgs {
		m := p.Meta

		// Effective maintainers: everyone in meta.maintainers (which
		// includes team members on current nixpkgs); "direct" ones are
		// listed on the package itself.
		direct := map[string]bool{}
		if m.NonTeamMaintainers != nil {
			for _, d := range *m.NonTeamMaintainers {
				direct[maintainerKey(d)] = true
			}
		}
		type pm struct {
			id     int
			direct bool
			handle string
		}
		pms := map[int]pm{}
		for _, mt := range m.Maintainers {
			id, ok := maintainerID(mt)
			if !ok {
				continue
			}
			isDirect := m.NonTeamMaintainers == nil || direct[maintainerKey(mt)]
			if old, ok := pms[id]; ok {
				isDirect = isDirect || old.direct
			}
			pms[id] = pm{id, isDirect, handle(mt)}
		}
		teams := map[int]bool{}
		for _, t := range m.Teams {
			if id, ok := teamID(t); ok {
				teams[id] = true
			}
		}

		handles := make([]string, 0, len(pms))
		directCount := 0
		for _, x := range pms {
			handles = append(handles, x.handle)
			if x.direct {
				directCount++
			}
		}
		sort.Slice(handles, func(i, j int) bool { return strings.ToLower(handles[i]) < strings.ToLower(handles[j]) })

		in.exec(`INSERT INTO packages (id, attr, package_set, name, pname, version, drv_path,
			description, long_description, homepage, license, main_program, position,
			broken, insecure, unfree, setup_hook,
			maintainer_count, direct_maintainer_count, team_count, maintainers,
			dep_count, rdep_count, rdep_transitive_count)
			VALUES (?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?, ?,?,?,?, ?,?,?)`,
			p.ID, p.Attr, p.Group, p.Name, p.Pname, p.Version, p.DrvPath,
			nullable(m.Description), nullable(m.LongDescription), nullable(m.Homepage),
			nullable(strings.Join(m.Licenses, ", ")), nullable(m.MainProgram),
			nullable(strings.TrimPrefix(m.Position, prefix)),
			boolInt(m.Broken), boolInt(m.Insecure), boolInt(m.Unfree), boolInt(p.SetupHook),
			len(pms), directCount, len(teams), strings.Join(handles, ", "),
			len(p.Deps), p.RDeps, p.RDepsTransitive)

		for _, x := range pms {
			in.exec(`INSERT INTO package_maintainers (package_id, maintainer_id, direct) VALUES (?,?,?)`, p.ID, x.id, boolInt(x.direct))
		}
		for id := range teams {
			in.exec(`INSERT INTO package_teams (package_id, team_id) VALUES (?,?)`, p.ID, id)
		}
		for _, a := range p.Aliases {
			in.exec(`INSERT INTO aliases (attr, package_id) VALUES (?,?)`, a, p.ID)
		}
		for _, d := range p.Deps {
			in.exec(`INSERT INTO dependencies (package_id, dep_id, kind) VALUES (?,?,?)`, p.ID, d.To, d.Kind)
		}
		if in.err != nil {
			return fmt.Errorf("package %s: %w", p.Attr, in.err)
		}
	}
	for _, e := range g.Errors {
		in.exec(`INSERT OR IGNORE INTO eval_errors (attr, error) VALUES (?,?)`, e.Attr, e.Error)
	}
	for k, v := range meta {
		in.exec(`INSERT INTO meta (key, value) VALUES (?,?)`, k, v)
	}
	return in.err
}

func nullableInt(i int64) any {
	if i == 0 {
		return nil
	}
	return i
}
