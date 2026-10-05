# nixpkgs-maintenance

Find unmaintained packages in nixpkgs.

`nixmaint update` evaluates the current `nixos-unstable` with
[nix-eval-jobs](https://github.com/nix-community/nix-eval-jobs) and stores all
packages, their maintainers, teams and dependencies in SQLite.
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
| `NIXMAINT_CHANNEL`    | `nixos-unstable`          |
| `NIXMAINT_SYSTEM`     | `x86_64-linux`            |
| `NIXMAINT_WORKERS`    | number of CPUs, at most 8 |
| `NIXMAINT_MAX_MEMORY` | `4096` (MiB per worker)   |
| `NIXMAINT_FORCE`      | `false`                   |

## NixOS module

```nix
{
  inputs.nixpkgs-maintenance.url = "github:pinpox/nixpkgs-maintenance";

  imports = [ inputs.nixpkgs-maintenance.nixosModules.default ];
  services.nixpkgs-maintenance = {
    enable = true;
    listen = "127.0.0.1:8787";
    update.frequency = "daily"; # systemd calendar expression
  };
}
```

The database is updated daily and 5 minutes after boot, and only when the
channel has moved.

