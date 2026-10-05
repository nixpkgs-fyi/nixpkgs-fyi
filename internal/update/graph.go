package update

import (
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Dependency kinds, in order of precedence when a derivation appears in
// several input lists.
var depKinds = []string{"propagated", "host", "native", "check"}

const depKindOther = "other"

type pkg struct {
	ID        int
	Attr      string   // canonical attribute path
	Aliases   []string // other attribute paths evaluating to the same drv
	Group     string
	Name      string
	Pname     string
	SetupHook bool
	Version   string
	DrvPath   string
	Meta      jobMeta
	inputDrvs map[string][]string
	depKinds  map[string]string // drv path -> kind

	Deps            []dep
	RDeps           int
	RDepsTransitive int
}

type dep struct {
	To   int
	Kind string
}

type evalError struct {
	Attr  string
	Error string
}

type graph struct {
	Pkgs   []*pkg
	Errors []evalError
	Edges  int
}

// collector accumulates jobs; several attribute paths may evaluate to the
// same derivation (e.g. python3Packages.foo and python313Packages.foo).
type collector struct {
	byDrv       map[string]*pkg
	errors      []evalError
	unsupported int // jobs skipped because the package does not support the system
}

func newCollector() *collector {
	return &collector{byDrv: map[string]*pkg{}}
}

// errorMessage strips the evaluation trace from a Nix error, keeping the
// final "error: ..." message.
func errorMessage(s string) string {
	if i := strings.LastIndex(s, "error: "); i >= 0 {
		s = s[i:]
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "       ")
	}
	s = strings.Join(lines, "\n")
	if len(s) > 2000 {
		s = strings.ToValidUTF8(s[:2000], "") + "…"
	}
	return s
}

func (c *collector) add(j *job) {
	attr := strings.Join(j.AttrPath, ".")
	if attr == "" {
		attr = j.Attr
	}
	if j.Error != "" || j.DrvPath == "" {
		// Packages that do not support the evaluated system are expected
		// to fail (Hydra skips them too); they are not errors.
		if strings.Contains(j.Error, "is not available on the requested hostPlatform") {
			c.unsupported++
			return
		}
		c.errors = append(c.errors, evalError{Attr: attr, Error: errorMessage(j.Error)})
		return
	}
	if p, ok := c.byDrv[j.DrvPath]; ok {
		p.Aliases = append(p.Aliases, attr)
		return
	}
	p := &pkg{
		Attr:      attr,
		Name:      j.Name,
		DrvPath:   j.DrvPath,
		inputDrvs: j.InputDrvs,
		depKinds:  map[string]string{},
	}
	p.SetupHook = j.Extra.SetupHook
	p.Pname, p.Version = parseDrvName(j.Name)
	if j.Extra.Pname != nil && j.Extra.Version != nil {
		p.Pname, p.Version = *j.Extra.Pname, *j.Extra.Version
	}
	if j.Extra.Meta != nil {
		p.Meta = *j.Extra.Meta
	}
	// Iterate in reverse precedence so higher-precedence kinds win.
	for i := len(depKinds) - 1; i >= 0; i-- {
		for _, d := range j.Extra.Deps[depKinds[i]] {
			p.depKinds[d] = depKinds[i]
		}
	}
	c.byDrv[j.DrvPath] = p
}

// attrLess orders attribute paths by preference for the canonical name:
// fewest dots, then shortest, then lexicographic.
func attrLess(a, b string) bool {
	da, db := strings.Count(a, "."), strings.Count(b, ".")
	if da != db {
		return da < db
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// parseDrvName splits a derivation name like builtins.parseDrvName: the
// version starts after the first dash that is not followed by a letter.
func parseDrvName(name string) (pname, version string) {
	for i := 0; i+1 < len(name); i++ {
		if name[i] == '-' {
			c := name[i+1]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				return name[:i], name[i+1:]
			}
		}
	}
	return name, ""
}

func groupOf(attr string) string {
	if i := strings.IndexByte(attr, '.'); i >= 0 {
		return attr[:i]
	}
	return "top-level"
}

func (c *collector) build() *graph {
	g := &graph{Errors: c.errors}
	for _, p := range c.byDrv {
		all := append(p.Aliases, p.Attr)
		sort.Slice(all, func(i, j int) bool { return attrLess(all[i], all[j]) })
		p.Attr, p.Aliases = all[0], all[1:]
		p.Group = groupOf(p.Attr)
		g.Pkgs = append(g.Pkgs, p)
	}
	sort.Slice(g.Pkgs, func(i, j int) bool { return g.Pkgs[i].Attr < g.Pkgs[j].Attr })
	for i, p := range g.Pkgs {
		p.ID = i
	}
	sort.Slice(g.Errors, func(i, j int) bool { return g.Errors[i].Attr < g.Errors[j].Attr })

	// Direct edges: input derivations that are packages themselves.
	for _, p := range g.Pkgs {
		for drv := range p.inputDrvs {
			q, ok := c.byDrv[drv]
			if !ok || q == p {
				continue
			}
			kind, ok := p.depKinds[drv]
			if !ok {
				kind = depKindOther
			}
			p.Deps = append(p.Deps, dep{To: q.ID, Kind: kind})
			q.RDeps++
		}
		sort.Slice(p.Deps, func(i, j int) bool { return p.Deps[i].To < p.Deps[j].To })
		g.Edges += len(p.Deps)
		p.inputDrvs, p.depKinds = nil, nil
	}
	g.computeTransitive()
	return g
}

// computeTransitive counts, for every package, how many packages depend on
// it directly or indirectly (BFS over reverse edges, parallel per source).
func (g *graph) computeTransitive() {
	n := len(g.Pkgs)
	rev := make([][]int32, n)
	for _, p := range g.Pkgs {
		for _, d := range p.Deps {
			rev[d.To] = append(rev[d.To], int32(p.ID))
		}
	}

	next := make(chan int, 1024)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen := make([]int32, n) // stamp = source id + 1
			var queue []int32
			for src := range next {
				if len(rev[src]) == 0 {
					continue
				}
				stamp := int32(src + 1)
				seen[src] = stamp
				queue = append(queue[:0], int32(src))
				count := 0
				for len(queue) > 0 {
					v := queue[len(queue)-1]
					queue = queue[:len(queue)-1]
					for _, u := range rev[v] {
						if seen[u] != stamp {
							seen[u] = stamp
							count++
							queue = append(queue, u)
						}
					}
				}
				g.Pkgs[src].RDepsTransitive = count
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}
