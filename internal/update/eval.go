package update

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"time"
)

//go:embed apply.nix
var applyExpr string

type person struct {
	Name     string `json:"name"`
	GitHub   string `json:"github"`
	GitHubID int64  `json:"githubId"`
	Email    string `json:"email"`
	Matrix   string `json:"matrix"`
}

type team struct {
	ShortName string   `json:"shortName"`
	GitHub    string   `json:"github"`
	Scope     string   `json:"scope"`
	Members   []person `json:"members"`
}

type jobMeta struct {
	Description        string    `json:"description"`
	LongDescription    string    `json:"longDescription"`
	Homepage           string    `json:"homepage"`
	Licenses           []string  `json:"licenses"`
	MainProgram        string    `json:"mainProgram"`
	Position           string    `json:"position"`
	Broken             bool      `json:"broken"`
	Insecure           bool      `json:"insecure"`
	Unfree             bool      `json:"unfree"`
	Maintainers        []person  `json:"maintainers"`
	NonTeamMaintainers *[]person `json:"nonTeamMaintainers"`
	Teams              []team    `json:"teams"`
}

// job is one line of nix-eval-jobs output.
type job struct {
	Attr      string              `json:"attr"`
	AttrPath  []string            `json:"attrPath"`
	Name      string              `json:"name"`
	DrvPath   string              `json:"drvPath"`
	Error     string              `json:"error"`
	InputDrvs map[string][]string `json:"inputDrvs"`
	Extra     struct {
		Pname     *string             `json:"pname"`
		SetupHook bool                `json:"setupHook"`
		Version   *string             `json:"version"`
		Meta      *jobMeta            `json:"meta"`
		Deps      map[string][]string `json:"deps"`
	} `json:"extraValue"`
}

type evalOptions struct {
	Nixpkgs   string // path to the nixpkgs source tree
	System    string
	Subset    string // Nix function applied to the package set
	Workers   int
	MaxMemory int // MiB per worker
	Binary    string
}

// evalExpr builds the root expression. Aliases are disabled so that every
// package is reached under its real name only (like Hydra). Broken and
// insecure packages are allowed so that they show up in the database.
// Broken packages are let through by dropping nixpkgs' default "broken =
// error" problem matcher: allowBroken would also reset meta.broken to
// false, and a matcher of our own cannot lower the default's severity.
func evalExpr(o evalOptions) string {
	return fmt.Sprintf(`let
  nixpkgs = %s;
  lib = import (nixpkgs + "/lib");
in
(%s) (import nixpkgs {
  system = %s;
  overlays = [ ];
  config = {
    allowUnfree = true;
    allowInsecurePredicate = _: true;
    allowAliases = false;
    problems.matchers = lib.mkForce [ { kind = "removal"; handler = "warn"; } ];
  };
})`, strconv.Quote(o.Nixpkgs), o.Subset, strconv.Quote(o.System))
}

// runEval streams nix-eval-jobs output into fn. Lines are handled in the
// order they arrive; parse failures abort the run.
func runEval(ctx context.Context, o evalOptions, fn func(*job)) error {
	cmd := exec.CommandContext(ctx, o.Binary,
		"--impure",
		"--show-input-drvs",
		"--workers", strconv.Itoa(o.Workers),
		"--max-memory-size", strconv.Itoa(o.MaxMemory),
		"--apply", applyExpr,
		"--expr", evalExpr(o),
	)
	// Evaluation errors are reported per job on stdout as well; stderr only
	// repeats them (megabytes per run), so keep just its tail for failures.
	stderr := &tailBuffer{max: 16 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", o.Binary, err)
	}

	r := bufio.NewReaderSize(stdout, 1<<20)
	n, last := 0, time.Now()
	var parseErr error
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && parseErr == nil {
			var j job
			if e := json.Unmarshal(line, &j); e != nil {
				parseErr = fmt.Errorf("parsing nix-eval-jobs line %d: %w", n+1, e)
				_ = cmd.Process.Kill()
			} else {
				fn(&j)
				n++
				if time.Since(last) > 30*time.Second {
					log.Printf("eval: %d jobs so far", n)
					last = time.Now()
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if waitErr != nil {
		return fmt.Errorf("%s: %w\n%s", o.Binary, waitErr, stderr.buf)
	}
	return nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}
