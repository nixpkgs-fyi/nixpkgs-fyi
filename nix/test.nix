# End-to-end test: the update service evaluates a small offline subset of the
# nixpkgs this flake is built with and compares it with a fake Repology API,
# the web service serves the result.
self:
{ pkgs, ... }:
{
  name = "nixpkgs-maintenance";

  nodes.machine = {
    imports = [ self.nixosModules.default ];
    virtualisation.memorySize = 4096;
    environment.systemPackages = [ pkgs.sqlite ];
    # Fake Repology API: the test script writes the pages.
    systemd.tmpfiles.rules = [ "d /srv/repology/api/v1/projects/hello 0755 root root -" ];
    systemd.services.fake-repology = {
      wantedBy = [ "multi-user.target" ];
      serviceConfig.ExecStart = "${pkgs.python3}/bin/python3 -m http.server 8000 --bind 127.0.0.1 --directory /srv/repology";
    };
    services.nixpkgs-maintenance = {
      enable = true;
      update.workers = 1;
    };
    # Evaluate an offline subset of the nixpkgs this test is built with.
    systemd.services.nixpkgs-maintenance-update.environment = {
      NIXMAINT_NIXPKGS = "${pkgs.path}";
      NIXMAINT_REV = "0000000000000000000000000000000000000000";
      NIXMAINT_SUBSET = "p: { inherit (p) hello stdenv; }";
      NIXMAINT_REPOLOGY_URL = "http://127.0.0.1:8000/api/v1";
    };
  };

  testScript = ''
    import json, shlex

    db = "/var/lib/nixpkgs-maintenance/nixpkgs.sqlite"

    def fake_repology(newest):
        # Both pages: the first, and the one starting at its last project.
        page = json.dumps({"hello": [
            {"repo": "nix_unstable", "srcname": "hello", "version": "${pkgs.hello.version}", "origversion": None, "status": "outdated"},
            {"repo": "upstream", "srcname": "hello", "version": newest, "origversion": None, "status": "newest"},
        ]})
        for path in ["projects", "projects/hello"]:
            machine.succeed(f"echo {shlex.quote(page)} > /srv/repology/api/v1/{path}/index.html")

    machine.wait_for_unit("nixpkgs-maintenance.service")
    machine.wait_for_open_port(8787)
    machine.succeed("curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8787/teams | grep -x 503")
    machine.succeed("curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8787/ | grep -x 302")

    # Default frequency: once per day ("daily" is normalised by systemd).
    machine.succeed("systemctl is-active nixpkgs-maintenance-update.timer")
    machine.succeed("systemctl show -p TimersCalendar nixpkgs-maintenance-update.timer | grep -F 'OnCalendar=*-*-* 00:00:00'")

    fake_repology("99.0")
    machine.wait_for_open_port(8000)
    machine.succeed("systemctl start nixpkgs-maintenance-update.service")
    machine.succeed(f"sqlite3 {db} \"SELECT maintainers FROM packages WHERE attr = 'hello'\" | grep -w stv0g")
    machine.succeed(f"sqlite3 {db} \"SELECT COUNT(*) FROM dependencies d JOIN packages p ON p.id = d.dep_id WHERE p.attr = 'stdenv'\" | grep -x 1")

    machine.succeed("curl -sf http://127.0.0.1:8787/package/hello | grep -w stv0g")
    machine.succeed("curl -sf 'http://127.0.0.1:8787/?maintainer=stv0g' | grep -F '/package/hello'")
    machine.succeed("curl -sf http://127.0.0.1:8787/maintainers | grep -w stv0g")

    machine.succeed(f"sqlite3 {db} \"SELECT r.status, r.newest FROM repology r JOIN packages p ON p.id = r.package_id WHERE p.attr = 'hello'\" | grep -x 'outdated|99.0'")
    machine.succeed("curl -sf http://127.0.0.1:8787/package/hello | grep -F 'newest version is <b>99.0</b>'")
    machine.succeed("curl -sf 'http://127.0.0.1:8787/?repology=outdated' | grep -F '/package/hello'")
    machine.fail("curl -sf 'http://127.0.0.1:8787/?repology=outdated' | grep -F '/package/stdenv'")

    # Same revision again: no evaluation, only the Repology data is refreshed.
    fake_repology("100.0")
    machine.succeed("systemctl start nixpkgs-maintenance-update.service")
    machine.succeed("journalctl -u nixpkgs-maintenance-update.service | grep 'already up to date'")
    machine.succeed(f"sqlite3 {db} \"SELECT newest FROM repology\" | grep -x '100.0'")
    machine.succeed("curl -sf http://127.0.0.1:8787/package/hello | grep -F 'newest version is <b>100.0</b>'")
  '';
}
