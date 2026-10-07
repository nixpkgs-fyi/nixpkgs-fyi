-- nixpkgs-maintenance database. One snapshot of one nixpkgs revision for one
-- system; regenerated from scratch on every update.

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- keys: schema_version, rev, channel, system, generated_at (RFC 3339 UTC),
--       eval_seconds, package_count, edge_count, eval_error_count,
--       repology_repo, repology_fetched_at (RFC 3339 UTC; only if Repology data was fetched)

CREATE TABLE packages (
  id                      INTEGER PRIMARY KEY,
  attr                    TEXT NOT NULL UNIQUE,  -- canonical attribute path
  package_set             TEXT NOT NULL,         -- first attr path component, or 'top-level'
  name                    TEXT NOT NULL,         -- derivation name, e.g. hello-2.12.3
  pname                   TEXT NOT NULL,
  version                 TEXT NOT NULL,         -- '' if none
  drv_path                TEXT NOT NULL UNIQUE,
  description             TEXT,
  long_description        TEXT,
  homepage                TEXT,
  license                 TEXT,                  -- SPDX ids joined with ', '
  main_program            TEXT,
  position                TEXT,                  -- relative, e.g. pkgs/by-name/he/hello/package.nix:58
  broken                  INTEGER NOT NULL,
  insecure                INTEGER NOT NULL,
  unfree                  INTEGER NOT NULL,
  setup_hook              INTEGER NOT NULL,      -- built with makeSetupHook (a build helper, not software)
  maintainer_count        INTEGER NOT NULL,      -- all maintainers, including team members
  direct_maintainer_count INTEGER NOT NULL,      -- maintainers listed on the package itself
  team_count              INTEGER NOT NULL,
  maintainers             TEXT NOT NULL,         -- sorted handles joined with ', ' ('' if none)
  dep_count               INTEGER NOT NULL,      -- direct dependencies
  rdep_count              INTEGER NOT NULL,      -- packages depending on this one directly
  rdep_transitive_count   INTEGER NOT NULL       -- ... directly or indirectly
);

CREATE TABLE aliases (
  attr       TEXT PRIMARY KEY,
  package_id INTEGER NOT NULL REFERENCES packages(id)
);

CREATE TABLE maintainers (
  id        INTEGER PRIMARY KEY,
  key       TEXT NOT NULL UNIQUE,  -- 'id:<githubId>', 'gh:<github>' or 'name:<name>'
  handle    TEXT NOT NULL,         -- github handle, else name
  name      TEXT,
  github    TEXT,
  github_id INTEGER,
  email     TEXT,
  matrix    TEXT
);

CREATE TABLE package_maintainers (
  package_id    INTEGER NOT NULL REFERENCES packages(id),
  maintainer_id INTEGER NOT NULL REFERENCES maintainers(id),
  direct        INTEGER NOT NULL,  -- 0 if only maintaining through a team
  PRIMARY KEY (package_id, maintainer_id)
) WITHOUT ROWID;

CREATE TABLE teams (
  id         INTEGER PRIMARY KEY,
  short_name TEXT NOT NULL UNIQUE,
  github     TEXT,
  scope      TEXT
);

CREATE TABLE team_members (
  team_id       INTEGER NOT NULL REFERENCES teams(id),
  maintainer_id INTEGER NOT NULL REFERENCES maintainers(id),
  PRIMARY KEY (team_id, maintainer_id)
) WITHOUT ROWID;

CREATE TABLE package_teams (
  package_id INTEGER NOT NULL REFERENCES packages(id),
  team_id    INTEGER NOT NULL REFERENCES teams(id),
  PRIMARY KEY (package_id, team_id)
) WITHOUT ROWID;

-- package_id depends directly on dep_id.
-- kind: propagated (propagatedBuildInputs), host (buildInputs),
--       native (nativeBuildInputs, ...), check (*CheckInputs),
--       other (any other input derivation, e.g. stdenv)
CREATE TABLE dependencies (
  package_id INTEGER NOT NULL REFERENCES packages(id),
  dep_id     INTEGER NOT NULL REFERENCES packages(id),
  kind       TEXT NOT NULL,
  PRIMARY KEY (package_id, dep_id)
) WITHOUT ROWID;

CREATE TABLE eval_errors (
  attr  TEXT PRIMARY KEY,
  error TEXT NOT NULL
);

-- Software without maintainers and teams; setup hooks are left out.
CREATE VIEW unmaintained_packages AS
  SELECT * FROM packages WHERE maintainer_count = 0 AND team_count = 0 AND setup_hook = 0;

-- Repology (https://repology.org) status of packages in projects Repology
-- considers outdated in nixpkgs. Packages without a row are up to date or not
-- tracked. Refreshed on every update, also when the revision did not change.
CREATE TABLE repology (
  package_id INTEGER PRIMARY KEY REFERENCES packages(id),
  project    TEXT NOT NULL,  -- Repology project name
  status     TEXT NOT NULL,  -- outdated, newest, legacy, ... (Repology package status)
  newest     TEXT NOT NULL   -- newest version of the project known to Repology
);
