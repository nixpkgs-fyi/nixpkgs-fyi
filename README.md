# nixpkgs-maintenance

Puts every nixpkgs package, with its meta information, maintainers, teams and
direct dependencies, into a SQLite database. A small web app on top of it
shows which packages are unmaintained and how much depends on them.

- `nixmaint update` evaluates the current `nixos-unstable` revision with
  [nix-eval-jobs](https://github.com/nix-community/nix-eval-jobs) and writes
  a new database. It does nothing if the database is already at that
  revision.
- `nixmaint serve` serves the web interface from that database. When `nixmaint update`
  replaces the file, the server switches to the new one without a restart.

## Usage

```sh
nix build
./result/bin/nixmaint update -db nixpkgs.sqlite      # full eval: ~3.5 min with 12 workers on 24 cores, ~100 MB database
./result/bin/nixmaint serve -db nixpkgs.sqlite       # http://127.0.0.1:8787
```

`nixmaint update` flags:

| flag | default | |
|---|---|---|
| `-db` | `nixpkgs.sqlite` | database file. It is written as `<db>.tmp` and atomically renamed, so a failed run leaves the old database in place |
| `-channel` | `nixos-unstable` | channel whose revision is evaluated (from `channels.nixos.org`) |
| `-system` | `x86_64-linux` | system to evaluate for |
| `-workers` / `-max-memory` | min(CPUs, 8) / 4096 | nix-eval-jobs workers and MiB per worker |
| `-force` | | regenerate even if the revision did not change |
| `-nixpkgs PATH -rev REV` | | evaluate a local nixpkgs checkout instead |
| `-subset 'p: { inherit (p) hello; }'` | `pkgs: pkgs` | evaluate only part of the package set (testing) |

### What is evaluated

The same package set as Hydra: everything reachable through
`recurseForDerivations` (for example `python3Packages`, `haskellPackages`),
with aliases disabled. Unfree, insecure and broken packages are included
and flagged. Broken packages are let through by replacing nixpkgs' default
`problems.matchers`, because `allowBroken` would reset `meta.broken`; this
needs a nixpkgs with the `problems` config (2026 or later). Packages that do
not support the system are skipped. Packages that fail to evaluate are
listed in `eval_errors`.

If several attribute paths evaluate to the same derivation (for example
`python3Packages.foo` and `python314Packages.foo`), they become one package.
The package gets the shortest name, and the other paths are stored in
`aliases`.

## NixOS module

```nix
{
  inputs.nixpkgs-maintenance.url = "github:pinpox/nixpkgs-maintenance";

  # in your NixOS configuration:
  imports = [ inputs.nixpkgs-maintenance.nixosModules.default ];
  services.nixpkgs-maintenance = {
    enable = true;
    listen = "127.0.0.1:8787";          # put a reverse proxy in front
    update = {
      frequency = "daily";              # systemd calendar expression, e.g. "*-*-* 03:00:00"
      workers = 4;
      maxMemory = 4096;                 # MiB per worker
    };
  };
}
```

This sets up:

- `nixpkgs-maintenance-update.service` and `.timer`. The service runs daily (`update.frequency`)
  and also 5 minutes after boot. It only talks to the nix-daemon to
  instantiate derivations and builds nothing.
- `nixpkgs-maintenance.service`, the web interface.

Data lives in `/var/lib/nixpkgs-maintenance/nixpkgs.sqlite`.

## Web interface

- **Colors** follow the browser's light/dark preference. The switch in the
  header cycles auto → light → dark and is remembered per browser.
- **Start page** (`/`): unmaintained `top-level` packages, the most
  depended-on first.
- **Packages**: filter by status, package set, broken flag, maintainer or
  team. Sort by name, maintainers (alphabetically or by count), number of
  dependencies, or by direct or transitive dependents. The cards at the top
  link to the unmaintained, single-maintainer and team-only packages, sorted
  by how many packages depend on them.
- **Package**: meta information, maintainers (listed directly or through a
  team), dependencies and dependents, with the status of each.
- **Maintainers**: the number of packages per maintainer, including
  *sole*, the packages that become unmaintained if this person leaves.
- **Teams**: members and package counts. Teams without members are
  highlighted.

Status definitions:

| status | meaning |
|---|---|
| unmaintained | no maintainers and no teams |
| single | exactly one maintainer and no team |
| team-only | no maintainer listed on the package itself, only teams |
| maintained | everything else |

Setup hooks such as `ensureNewerSourcesForZipFilesHook` or `makeWrapper`
are build helpers, not software. They are recognised by being built with
`makeSetupHook` (column `setup_hook`). They are hidden from the package list,
the counts and the `unmaintained_packages` view by default. Use the "Setup
hooks" filter to show them.

## Database

The schema is in [`internal/update/schema.sql`](internal/update/schema.sql).
Dependencies are the *direct* input derivations of a package that are
packages themselves. Each one has a kind: `propagated`
(`propagatedBuildInputs`), `host` (`buildInputs`), `native`
(`nativeBuildInputs`), `check` (`*CheckInputs`), or `other` (for example
`stdenv`).

```sql
-- most depended-on unmaintained packages
SELECT attr, rdep_transitive_count FROM unmaintained_packages
ORDER BY rdep_transitive_count DESC LIMIT 20;

-- packages of a maintainer
SELECT p.attr FROM maintainers m
JOIN package_maintainers pm ON pm.maintainer_id = m.id
JOIN packages p ON p.id = pm.package_id
WHERE m.github = 'pinpox';

-- unmaintained direct dependencies of a package
SELECT d.attr FROM dependencies x
JOIN packages p ON p.id = x.package_id
JOIN unmaintained_packages d ON d.id = x.dep_id
WHERE p.attr = 'firefox';
```

## Development

```sh
nix develop
go build -o nixmaint . && ./nixmaint update -db dev.sqlite -subset 'p: { inherit (p) hello curl; }'
./nixmaint serve -db dev.sqlite
nix flake check   # NixOS VM test: update service + web interface
```
