{
  description = "nixpkgs-maintenance: find unmaintained nixpkgs packages, and the clan running it on nixpkgs.fyi";

  inputs.clan-core.url = "https://git.clan.lol/clan/clan-core/archive/main.tar.gz";
  inputs.nixpkgs.follows = "clan-core/nixpkgs";

  outputs =
    {
      self,
      clan-core,
      nixpkgs,
      ...
    }@inputs:
    let
      lib = nixpkgs.lib;
      appSystems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forSystems = systems: f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # The machine running nixpkgs.fyi; see also machines/, vars/, secrets/.
      # Usage see: https://clan.lol/docs
      clan = clan-core.lib.clan {
        inherit self;
        specialArgs = { inherit inputs; };

        meta.name = "nixpkgs-fyi";
        meta.domain = "nixpkgs.fyi";

        vars.settings.secretStore = "age";
        vars.settings.recipients.default = import ./age-keys.nix;
        secrets.age.plugins = [ "age-plugin-yubikey" ];

        inventory.instances = {
          internet.roles.default.machines.uno.settings.host = "37.27.153.220";

          sshd.roles.server.tags.all = { };

          # User name defaults to the instance name, so this manages root.
          root = {
            module.name = "users";
            roles.default.tags.all = { };
            roles.default.settings = {
              share = true;
              openssh.authorizedKeys.keys = import ./ssh-keys.nix;
            };
          };
        };
      };
    in
    {
      inherit (clan.config) nixosConfigurations clanInternals;
      clan = clan.config;

      packages = forSystems appSystems (pkgs: {
        default = pkgs.callPackage ./nix/package.nix { };
      });

      nixosModules = clan.config.nixosModules // {
        default = import ./nix/module.nix self;
      };

      checks = forSystems appSystems (pkgs: {
        vm = pkgs.testers.runNixOSTest (import ./nix/test.nix self);
      });

      devShells =
        forSystems
          [
            "x86_64-linux"
            "aarch64-linux"
            "aarch64-darwin"
          ]
          (pkgs: {
            default = pkgs.mkShell {
              packages = [
                clan-core.packages.${pkgs.stdenv.hostPlatform.system}.clan-cli
                pkgs.go
                pkgs.gopls
                pkgs.nix-eval-jobs
                pkgs.sqlite
              ];
            };
          });
    };
}
