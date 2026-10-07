# nixpkgs-maintenance

Find unmaintained packages in nixpkgs.

`nixmaint update` evaluates the current `nixos-unstable` with
[nix-eval-jobs](https://github.com/nix-community/nix-eval-jobs) and stores all
packages, their maintainers, teams and dependencies in SQLite. It also asks
[Repology](https://repology.org) which packages are outdated, i.e. have a newer
upstream version.
`nixmaint serve` shows them in a web interface.

## Usage

```sh
nix build
./result/bin/nixmaint update   # takes a few minutes
./result/bin/nixmaint serve    # http://127.0.0.1:8787
```

Configuration is done with environment variables (`nixmaint help`):

| variable              | default                   |
|-----------------------|---------------------------|
| `NIXMAINT_DB`         | `nixpkgs.sqlite`          |
| `NIXMAINT_LISTEN`     | `127.0.0.1:8787`          |
| `NIXMAINT_BANNER`     | `true` (credits footer)   |
| `NIXMAINT_CHANNEL`    | `nixos-unstable`          |
| `NIXMAINT_SYSTEM`     | `x86_64-linux`            |
| `NIXMAINT_WORKERS`    | number of CPUs, at most 8 |
| `NIXMAINT_MAX_MEMORY` | `4096` (MiB per worker)   |
| `NIXMAINT_FORCE`      | `false`                   |
| `NIXMAINT_REPOLOGY`   | `true`                    |

## NixOS module

```nix
{
  inputs.nixpkgs-fyi.url = "github:nixpkgs-fyi/nixpkgs-fyi";

  imports = [ inputs.nixpkgs-fyi.nixosModules.default ];
  services.nixpkgs-maintenance = {
    enable = true;
    listen = "127.0.0.1:8787";
    update.frequency = "daily"; # systemd calendar expression
  };
}
```

The database is updated daily and 5 minutes after boot, and only when the
channel has moved. The Repology data is refreshed on every run.

Only projects Repology considers outdated in nixpkgs are fetched (about 90 API
requests, rate limited to one per second), as Repology asks not to download
everything through its API. Packages without Repology data are therefore
either up to date or not tracked by Repology. A status is only shown if
Repology saw the same version as the evaluated revision.

## Deployment

[nixpkgs.fyi](https://nixpkgs.fyi) runs on the [clan](https://clan.lol)
machine `uno` defined in this repository (`flake.nix`, `machines/`, `vars/`,
`secrets/`). It uses the module and package from this repo directly.

```sh
nix develop                 # provides the clan CLI
clan machines update uno
```

