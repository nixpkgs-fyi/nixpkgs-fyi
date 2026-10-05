# End-to-end test: the update service evaluates a small offline subset of the
# nixpkgs this flake is built with, the web service serves the result.
self:
{ pkgs, ... }:
{
  name = "nixpkgs-maintenance";

  nodes.machine = {
    imports = [ self.nixosModules.default ];
    virtualisation.memorySize = 4096;
    environment.systemPackages = [ pkgs.sqlite ];
    services.nixpkgs-maintenance = {
      enable = true;
      update.workers = 1;
    };
    # Evaluate an offline subset of the nixpkgs this test is built with.
    systemd.services.nixpkgs-maintenance-update.environment = {
      NIXMAINT_NIXPKGS = "${pkgs.path}";
      NIXMAINT_REV = "0000000000000000000000000000000000000000";
      NIXMAINT_SUBSET = "p: { inherit (p) hello stdenv; }";
    };
  };

  testScript = ''
    db = "/var/lib/nixpkgs-maintenance/nixpkgs.sqlite"

    machine.wait_for_unit("nixpkgs-maintenance.service")
    machine.wait_for_open_port(8787)
    machine.succeed("curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8787/teams | grep -x 503")
    machine.succeed("curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8787/ | grep -x 302")

    # Default frequency: once per day ("daily" is normalised by systemd).
    machine.succeed("systemctl is-active nixpkgs-maintenance-update.timer")
    machine.succeed("systemctl show -p TimersCalendar nixpkgs-maintenance-update.timer | grep -F 'OnCalendar=*-*-* 00:00:00'")

    machine.succeed("systemctl start nixpkgs-maintenance-update.service")
    machine.succeed(f"sqlite3 {db} \"SELECT maintainers FROM packages WHERE attr = 'hello'\" | grep -w stv0g")
    machine.succeed(f"sqlite3 {db} \"SELECT COUNT(*) FROM dependencies d JOIN packages p ON p.id = d.dep_id WHERE p.attr = 'stdenv'\" | grep -x 1")

    machine.succeed("curl -sf http://127.0.0.1:8787/package/hello | grep -w stv0g")
    machine.succeed("curl -sf 'http://127.0.0.1:8787/?maintainer=stv0g' | grep -F '/package/hello'")
    machine.succeed("curl -sf http://127.0.0.1:8787/maintainers | grep -w stv0g")

    # Same revision again: nothing to do, database untouched.
    machine.succeed("systemctl start nixpkgs-maintenance-update.service")
    machine.succeed("journalctl -u nixpkgs-maintenance-update.service | grep 'already up to date'")
  '';
}
