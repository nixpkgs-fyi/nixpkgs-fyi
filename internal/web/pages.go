package web

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	packagesPerPage    = 100
	maintainersPerPage = 200
)

// Package list --------------------------------------------------------------

type pkgRow struct {
	Attr            string
	Version         string
	Description     string
	License         string
	Maintainers     []string
	Teams           []string
	MaintainerCount int
	DirectCount     int
	TeamCount       int
	DepCount        int
	RDepCount       int
	RDepTransitive  int
	Broken          bool
	Insecure        bool
	Unfree          bool
	SetupHook       bool
	Newest          string // newest upstream version if Repology reports the package outdated
	Kind            string // dependency kind, on the package page only
}

// Status classifies how well a package is maintained.
func (p pkgRow) Status() string {
	switch {
	case p.MaintainerCount == 0 && p.TeamCount == 0:
		return "unmaintained"
	case p.DirectCount == 0:
		return "team-only"
	case p.MaintainerCount == 1 && p.TeamCount == 0:
		return "single"
	}
	return "maintained"
}

const pkgColumns = `p.attr, p.version, COALESCE(p.description, ''), COALESCE(p.license, ''),
	p.maintainers,
	COALESCE((SELECT group_concat(t.short_name, ', ') FROM package_teams pt JOIN teams t ON t.id = pt.team_id WHERE pt.package_id = p.id), ''),
	p.maintainer_count, p.direct_maintainer_count, p.team_count,
	p.dep_count, p.rdep_count, p.rdep_transitive_count,
	p.broken, p.insecure, p.unfree, p.setup_hook,
	COALESCE((SELECT r.newest FROM repology r WHERE r.package_id = p.id AND r.status = 'outdated'), '')`

func scanPkg(rows *sql.Rows, extra ...any) (pkgRow, error) {
	var p pkgRow
	var maintainers, teams string
	dest := append([]any{&p.Attr, &p.Version, &p.Description, &p.License, &maintainers, &teams,
		&p.MaintainerCount, &p.DirectCount, &p.TeamCount,
		&p.DepCount, &p.RDepCount, &p.RDepTransitive,
		&p.Broken, &p.Insecure, &p.Unfree, &p.SetupHook, &p.Newest}, extra...)
	err := rows.Scan(dest...)
	p.Maintainers = splitList(maintainers)
	p.Teams = splitList(teams)
	return p, err
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ", ")
}

func queryPkgs(db *sql.DB, query string, args ...any) ([]pkgRow, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pkgRow
	for rows.Next() {
		p, err := scanPkg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Sort keys of the package list: SQL for ascending and descending order.
var pkgSorts = map[string][2]string{
	"attr":             {"p.attr ASC", "p.attr DESC"},
	"version":          {"p.version ASC", "p.version DESC"},
	"maintainers":      {"p.maintainers = '' ASC, p.maintainers COLLATE NOCASE ASC", "p.maintainers = '' ASC, p.maintainers COLLATE NOCASE DESC"},
	"maintainer_count": {"p.maintainer_count ASC, p.team_count ASC", "p.maintainer_count DESC, p.team_count DESC"},
	"deps":             {"p.dep_count ASC", "p.dep_count DESC"},
	"rdeps":            {"p.rdep_count ASC", "p.rdep_count DESC"},
	"rdeps_transitive": {"p.rdep_transitive_count ASC", "p.rdep_transitive_count DESC"},
}

var statusFilters = map[string]string{
	"unmaintained": "p.maintainer_count = 0 AND p.team_count = 0",
	"team-only":    "p.direct_maintainer_count = 0 AND p.team_count > 0",
	"single":       "p.maintainer_count = 1 AND p.team_count = 0",
	"maintained":   "(p.maintainer_count > 0 OR p.team_count > 0)",
}

type packagesData struct {
	Rows       []pkgRow
	Total      int
	Pager      pager
	Sort, Dir  string
	Maintainer *maintainerRow // set when filtering by one maintainer
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

func packagesPage(r *http.Request, snap *snapshot, p *page) (string, error) {
	q := r.URL.Query()
	p.Title, p.Nav = "Packages", "packages"
	d := &packagesData{Sort: q.Get("sort"), Dir: q.Get("dir")}
	if _, ok := pkgSorts[d.Sort]; !ok {
		d.Sort = "attr"
	}
	if d.Dir != "desc" {
		d.Dir = "asc"
	}

	var where []string
	var args []any
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		where = append(where, `(p.attr LIKE ? ESCAPE '\' OR p.description LIKE ? ESCAPE '\')`)
		args = append(args, likePattern(s), likePattern(s))
	}
	if f, ok := statusFilters[q.Get("status")]; ok {
		where = append(where, f)
	}
	if m := q.Get("maintainer"); m != "" {
		where = append(where, `p.id IN (SELECT pm.package_id FROM package_maintainers pm
			JOIN maintainers m ON m.id = pm.maintainer_id WHERE m.handle = ? COLLATE NOCASE)`)
		args = append(args, m)
		ms, err := queryMaintainers(snap.DB, `WHERE m.handle = ? COLLATE NOCASE`, "m.handle", 1, 0, m)
		if err != nil {
			return "", err
		}
		if len(ms) > 0 {
			d.Maintainer = &ms[0]
			p.Title = "Packages of " + ms[0].Handle
		}
	}
	if t := q.Get("team"); t != "" {
		where = append(where, `p.id IN (SELECT pt.package_id FROM package_teams pt
			JOIN teams t ON t.id = pt.team_id WHERE t.short_name = ?)`)
		args = append(args, t)
		p.Title = "Packages of team " + t
	}
	if s := q.Get("set"); s != "" {
		where = append(where, `p.package_set = ?`)
		args = append(args, s)
	}
	if q.Get("repology") == "outdated" {
		where = append(where, `p.id IN (SELECT package_id FROM repology WHERE status = 'outdated')`)
	}
	switch q.Get("broken") {
	case "1":
		where = append(where, `p.broken = 1`)
	case "0":
		where = append(where, `p.broken = 0`)
	}
	// Setup hooks (makeSetupHook) are build helpers, not software; hidden
	// unless asked for.
	switch q.Get("hooks") {
	case "show":
	case "only":
		where = append(where, `p.setup_hook = 1`)
	default:
		where = append(where, `p.setup_hook = 0`)
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}

	if err := snap.DB.QueryRow(`SELECT COUNT(*) FROM packages p `+cond, args...).Scan(&d.Total); err != nil {
		return "", err
	}
	d.Pager = newPager(q, d.Total, packagesPerPage)
	order := pkgSorts[d.Sort][0]
	if d.Dir == "desc" {
		order = pkgSorts[d.Sort][1]
	}
	rows, err := queryPkgs(snap.DB, `SELECT `+pkgColumns+` FROM packages p `+cond+
		` ORDER BY `+order+`, p.attr LIMIT ? OFFSET ?`,
		append(args, packagesPerPage, d.Pager.Offset)...)
	if err != nil {
		return "", err
	}
	d.Rows = rows
	p.Data = d
	return "packages", nil
}

// Package detail ------------------------------------------------------------

type packageData struct {
	pkgRow
	RequestedAttr   string // alias the package was requested by, if any
	Name            string
	Set             string
	LongDescription string
	Homepage        string
	MainProgram     string
	Position        string
	DrvPath         string
	RepologyProject string // '' if Repology reported nothing for the package
	RepologyStatus  string
	Aliases         []string
	People          []personRow
	TeamRows        []teamRow
	Deps            []pkgRow
	RDeps           []pkgRow
	RDepsShown      int
}

type personRow struct {
	Handle, Name, GitHub, Email, Matrix string
	Direct                              bool
	ViaTeams                            string
}

const rdepsLimit = 1000

func packagePage(r *http.Request, snap *snapshot, p *page) (string, error) {
	db := snap.DB
	attr := r.PathValue("attr")
	var id int
	err := db.QueryRow(`SELECT id FROM packages WHERE attr = ?
		UNION ALL SELECT package_id FROM aliases WHERE attr = ? LIMIT 1`, attr, attr).Scan(&id)
	if err == sql.ErrNoRows {
		return "", errNotFound
	} else if err != nil {
		return "", err
	}

	d := &packageData{}
	rows, err := db.Query(`SELECT `+pkgColumns+`, p.name, p.package_set, COALESCE(p.long_description, ''),
		COALESCE(p.homepage, ''), COALESCE(p.main_program, ''), COALESCE(p.position, ''), p.drv_path
		FROM packages p WHERE p.id = ?`, id)
	if err != nil {
		return "", err
	}
	if rows.Next() {
		d.pkgRow, err = scanPkg(rows, &d.Name, &d.Set, &d.LongDescription, &d.Homepage, &d.MainProgram, &d.Position, &d.DrvPath)
	}
	rows.Close()
	if err != nil {
		return "", err
	}
	if attr != d.Attr {
		d.RequestedAttr = attr
	}
	p.Title, p.Nav = d.Attr, "packages"

	if err := queryList(db, &d.Aliases, `SELECT attr FROM aliases WHERE package_id = ? ORDER BY attr`, id); err != nil {
		return "", err
	}

	rows, err = db.Query(`SELECT m.handle, COALESCE(m.name, ''), COALESCE(m.github, ''), COALESCE(m.email, ''), COALESCE(m.matrix, ''), pm.direct,
		COALESCE((SELECT group_concat(t.short_name, ', ') FROM package_teams pt
			JOIN team_members tm ON tm.team_id = pt.team_id AND tm.maintainer_id = m.id
			JOIN teams t ON t.id = pt.team_id WHERE pt.package_id = pm.package_id), '')
		FROM package_maintainers pm JOIN maintainers m ON m.id = pm.maintainer_id
		WHERE pm.package_id = ? ORDER BY pm.direct DESC, m.handle COLLATE NOCASE`, id)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var x personRow
		if err := rows.Scan(&x.Handle, &x.Name, &x.GitHub, &x.Email, &x.Matrix, &x.Direct, &x.ViaTeams); err != nil {
			rows.Close()
			return "", err
		}
		d.People = append(d.People, x)
	}
	rows.Close()

	if d.TeamRows, err = queryTeams(db, `WHERE t.id IN (SELECT team_id FROM package_teams WHERE package_id = ?)`, id); err != nil {
		return "", err
	}

	if d.Deps, err = queryKinded(db, `SELECT `+pkgColumns+`, d.kind FROM dependencies d JOIN packages p ON p.id = d.dep_id
		WHERE d.package_id = ? ORDER BY CASE d.kind WHEN 'propagated' THEN 0 WHEN 'host' THEN 1 WHEN 'native' THEN 2 WHEN 'check' THEN 3 ELSE 4 END, p.attr`, id); err != nil {
		return "", err
	}
	if d.RDeps, err = queryKinded(db, `SELECT `+pkgColumns+`, d.kind FROM dependencies d JOIN packages p ON p.id = d.package_id
		WHERE d.dep_id = ? ORDER BY p.rdep_transitive_count DESC, p.attr LIMIT ?`, id, rdepsLimit); err != nil {
		return "", err
	}
	err = db.QueryRow(`SELECT project, status FROM repology WHERE package_id = ?`, id).Scan(&d.RepologyProject, &d.RepologyStatus)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	d.RDepsShown = len(d.RDeps)
	p.Data = d
	return "package", nil
}

func queryKinded(db *sql.DB, query string, args ...any) ([]pkgRow, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pkgRow
	for rows.Next() {
		var kind string
		p, err := scanPkg(rows, &kind)
		if err != nil {
			return nil, err
		}
		p.Kind = kind
		out = append(out, p)
	}
	return out, rows.Err()
}

func queryList(db *sql.DB, out *[]string, query string, args ...any) error {
	rows, err := db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		*out = append(*out, s)
	}
	return rows.Err()
}

// Maintainers ---------------------------------------------------------------

type maintainerRow struct {
	Handle, Name, GitHub string
	Packages             int // all packages, including through teams
	Direct               int // packages listing the maintainer directly
	Sole                 int // packages with this as the only maintainer and no team
	Teams                int
}

var maintainerSorts = map[string][2]string{
	"handle":   {"m.handle COLLATE NOCASE ASC", "m.handle COLLATE NOCASE DESC"},
	"packages": {"packages ASC", "packages DESC"},
	"direct":   {"direct ASC", "direct DESC"},
	"sole":     {"sole ASC", "sole DESC"},
	"teams":    {"teams ASC", "teams DESC"},
}

func queryMaintainers(db *sql.DB, cond, order string, limit, offset int, args ...any) ([]maintainerRow, error) {
	rows, err := db.Query(`SELECT m.handle, COALESCE(m.name, ''), COALESCE(m.github, ''),
		COALESCE(SUM(p.setup_hook = 0), 0) AS packages,
		COALESCE(SUM(pm.direct AND p.setup_hook = 0), 0) AS direct,
		COALESCE(SUM(p.maintainer_count = 1 AND p.team_count = 0 AND p.setup_hook = 0), 0) AS sole,
		(SELECT COUNT(*) FROM team_members tm WHERE tm.maintainer_id = m.id) AS teams
		FROM maintainers m
		LEFT JOIN package_maintainers pm ON pm.maintainer_id = m.id
		LEFT JOIN packages p ON p.id = pm.package_id
		`+cond+`
		GROUP BY m.id ORDER BY `+order+`, m.handle COLLATE NOCASE LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []maintainerRow
	for rows.Next() {
		var m maintainerRow
		if err := rows.Scan(&m.Handle, &m.Name, &m.GitHub, &m.Packages, &m.Direct, &m.Sole, &m.Teams); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type maintainersData struct {
	Rows      []maintainerRow
	Total     int
	Pager     pager
	Sort, Dir string
}

func maintainersPage(r *http.Request, snap *snapshot, p *page) (string, error) {
	q := r.URL.Query()
	p.Title, p.Nav = "Maintainers", "maintainers"
	d := &maintainersData{Sort: q.Get("sort"), Dir: q.Get("dir")}
	if _, ok := maintainerSorts[d.Sort]; !ok {
		d.Sort, d.Dir = "packages", "desc"
	}
	if d.Dir != "asc" {
		d.Dir = "desc"
	}
	cond, args := "", []any{}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		cond = `WHERE (m.handle LIKE ? ESCAPE '\' OR m.name LIKE ? ESCAPE '\')`
		args = append(args, likePattern(s), likePattern(s))
	}
	if err := snap.DB.QueryRow(`SELECT COUNT(*) FROM maintainers m `+cond, args...).Scan(&d.Total); err != nil {
		return "", err
	}
	d.Pager = newPager(q, d.Total, maintainersPerPage)
	order := maintainerSorts[d.Sort][0]
	if d.Dir == "desc" {
		order = maintainerSorts[d.Sort][1]
	}
	var err error
	if d.Rows, err = queryMaintainers(snap.DB, cond, order, maintainersPerPage, d.Pager.Offset, args...); err != nil {
		return "", err
	}
	p.Data = d
	return "maintainers", nil
}

// Teams ---------------------------------------------------------------------

type teamRow struct {
	ShortName, GitHub, Scope string
	Members                  []string
	Packages                 int
}

func queryTeams(db *sql.DB, cond string, args ...any) ([]teamRow, error) {
	rows, err := db.Query(`SELECT t.short_name, COALESCE(t.github, ''), COALESCE(t.scope, ''),
		COALESCE((SELECT group_concat(handle, ', ') FROM (SELECT m.handle FROM team_members tm
			JOIN maintainers m ON m.id = tm.maintainer_id WHERE tm.team_id = t.id ORDER BY m.handle COLLATE NOCASE)), ''),
		(SELECT COUNT(*) FROM package_teams pt JOIN packages p ON p.id = pt.package_id
			WHERE pt.team_id = t.id AND p.setup_hook = 0) AS packages
		FROM teams t `+cond+` ORDER BY packages DESC, t.short_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []teamRow
	for rows.Next() {
		var t teamRow
		var members string
		if err := rows.Scan(&t.ShortName, &t.GitHub, &t.Scope, &members, &t.Packages); err != nil {
			return nil, err
		}
		t.Members = splitList(members)
		out = append(out, t)
	}
	return out, rows.Err()
}

func teamsPage(r *http.Request, snap *snapshot, p *page) (string, error) {
	p.Title, p.Nav = "Teams", "teams"
	teams, err := queryTeams(snap.DB, "")
	if err != nil {
		return "", err
	}
	p.Data = teams
	return "teams", nil
}

// Helpers -------------------------------------------------------------------

type pager struct {
	Page, Pages, Offset int
	Prev, Next          string // URLs, empty if none
}

func newPager(q url.Values, total, per int) pager {
	pg := pager{Pages: max(1, (total+per-1)/per)}
	pg.Page, _ = strconv.Atoi(q.Get("page"))
	pg.Page = min(max(pg.Page, 1), pg.Pages)
	pg.Offset = (pg.Page - 1) * per
	if pg.Page > 1 {
		pg.Prev = withQuery(q, "page", strconv.Itoa(pg.Page-1))
	}
	if pg.Page < pg.Pages {
		pg.Next = withQuery(q, "page", strconv.Itoa(pg.Page+1))
	}
	return pg
}

// withQuery returns "?query" with the given keys replaced (removed if the
// value is empty). Changing any filter resets pagination.
func withQuery(q url.Values, kv ...string) string {
	out := url.Values{}
	for k, v := range q {
		out[k] = v
	}
	if len(kv) == 0 || kv[0] != "page" {
		out.Del("page")
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			out.Del(kv[i])
		} else {
			out.Set(kv[i], kv[i+1])
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return "?" + out.Encode()
}

// sortLink returns the URL for sorting by key: toggles the direction if
// already sorted by key, else uses the key's natural first direction.
func sortLink(q url.Values, current, dir, key, first string) string {
	next := first
	if current == key {
		next = map[string]string{"asc": "desc", "desc": "asc"}[dir]
	}
	return withQuery(q, "sort", key, "dir", next)
}

func sortMark(current, dir, key string) string {
	if current != key {
		return ""
	}
	if dir == "desc" {
		return " ▼"
	}
	return " ▲"
}

func formatNum(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func percent(n, total int) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}

// sourceURL links a position like "pkgs/foo/default.nix:12" to GitHub.
func sourceURL(rev, position string) string {
	file, line, ok := strings.Cut(position, ":")
	if !ok || strings.HasPrefix(file, "/") {
		return ""
	}
	return "https://github.com/NixOS/nixpkgs/blob/" + url.PathEscape(rev) + "/" + file + "#L" + line
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

var kindLabels = map[string]string{
	"propagated": "propagatedBuildInputs",
	"host":       "buildInputs",
	"native":     "nativeBuildInputs",
	"check":      "checkInputs",
	"other":      "other input",
}

var funcs = template.FuncMap{
	"q":          withQuery,
	"sortLink":   sortLink,
	"sortMark":   sortMark,
	"num":        formatNum,
	"pct":        percent,
	"sourceURL":  sourceURL,
	"shortRev":   shortRev,
	"kindLabel":  func(k string) string { return kindLabels[k] },
	"pathEscape": url.PathEscape,
	"join":       strings.Join,
}
