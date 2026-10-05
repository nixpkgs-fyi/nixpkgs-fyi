self:
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.nixpkgs-maintenance;
  stateDir = "/var/lib/nixpkgs-maintenance";
  db = "${stateDir}/nixpkgs.sqlite";
  exe = lib.getExe cfg.package;

  hardening = {
    User = "nixpkgs-maintenance";
    Group = "nixpkgs-maintenance";
    StateDirectory = "nixpkgs-maintenance";
    ProtectSystem = "strict";
    ProtectHome = true;
    PrivateTmp = true;
    PrivateDevices = true;
    NoNewPrivileges = true;
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectControlGroups = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
  };
in
{
  options.services.nixpkgs-maintenance = {
    enable = lib.mkEnableOption "the nixpkgs maintenance database and web interface";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "nixpkgs-maintenance.packages.\${system}.default";
      description = "The nixpkgs-maintenance package.";
    };

    listen = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8787";
      description = "Address the web interface listens on. Put a reverse proxy in front for public access.";
    };

    banner = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Show the \"Built with ❤️ by pinpox\" footer linking to the source code.";
    };

    update = {
      frequency = lib.mkOption {
        type = lib.types.str;
        default = "daily";
        example = "*-*-* 03:00:00";
        description = ''
          How often to check the channel and regenerate the database, as a
          systemd calendar expression (e.g. "hourly", "daily", "weekly",
          "*-*-* 03:00:00"); see {manpage}`systemd.time(7)`. Nothing is done
          if the channel revision did not change.
        '';
      };
      channel = lib.mkOption {
        type = lib.types.str;
        default = "nixos-unstable";
        description = "Channel whose current revision is evaluated.";
      };
      system = lib.mkOption {
        type = lib.types.str;
        default = "x86_64-linux";
        description = "System to evaluate packages for.";
      };
      workers = lib.mkOption {
        type = lib.types.ints.positive;
        default = 4;
        description = "Number of parallel nix-eval-jobs workers.";
      };
      maxMemory = lib.mkOption {
        type = lib.types.ints.positive;
        default = 4096;
        description = "Memory per nix-eval-jobs worker in MiB; workers × maxMemory is the evaluation's memory budget.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.nixpkgs-maintenance = {
      isSystemUser = true;
      group = "nixpkgs-maintenance";
      home = stateDir;
    };
    users.groups.nixpkgs-maintenance = { };

    systemd.services.nixpkgs-maintenance-update = {
      description = "Regenerate the nixpkgs maintenance database";
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      path = [ config.nix.package ];
      environment = {
        HOME = stateDir;
        NIXMAINT_DB = db;
        NIXMAINT_CHANNEL = cfg.update.channel;
        NIXMAINT_SYSTEM = cfg.update.system;
        NIXMAINT_WORKERS = toString cfg.update.workers;
        NIXMAINT_MAX_MEMORY = toString cfg.update.maxMemory;
      };
      serviceConfig = hardening // {
        Type = "oneshot";
        ExecStart = "${exe} update";
        Nice = 19;
        IOSchedulingClass = "idle";
        TimeoutStartSec = "6h";
      };
    };

    systemd.timers.nixpkgs-maintenance-update = {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnCalendar = cfg.update.frequency;
        # Also check shortly after boot, so a fresh install gets data
        # without waiting for the next run; unchanged revisions are skipped.
        OnBootSec = "5min";
        Persistent = true;
        RandomizedDelaySec = "15min";
      };
    };

    systemd.services.nixpkgs-maintenance = {
      description = "nixpkgs maintenance web interface";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];
      environment = {
        NIXMAINT_DB = db;
        NIXMAINT_LISTEN = cfg.listen;
        NIXMAINT_BANNER = lib.boolToString cfg.banner;
      };
      serviceConfig = hardening // {
        ExecStart = "${exe} serve";
        Restart = "on-failure";
      };
    };
  };
}
