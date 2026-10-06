{ config, inputs, ... }:
{
  imports = [ inputs.self.nixosModules.default ];

  # Static addressing (Hetzner Cloud: IPv4 gateway is on-link, IPv6 gateway is fe80::1).
  networking.useNetworkd = true;
  networking.useDHCP = false;
  networking.nameservers = [
    "185.12.64.1"
    "185.12.64.2"
    "2a01:4ff:ff00::add:1"
    "2a01:4ff:ff00::add:2"
  ];

  systemd.network.networks."10-uplink" = {
    matchConfig.Name = "enp1s0";
    address = [
      "37.27.153.220/32"
      "2a01:4f9:c015:30e6::1/64"
    ];
    routes = [
      {
        Gateway = "172.31.1.1";
        GatewayOnLink = true;
      }
      { Gateway = "fe80::1"; }
    ];
  };

  services.nixpkgs-maintenance = {
    enable = true;
    # Evaluating nixpkgs must fit the CX23's 4 GB RAM (default is 4 × 4096 MiB).
    update.workers = 1;
    update.maxMemory = 2048;
  };

  # Caddy obtains the TLS certificate for nixpkgs.fyi automatically (ACME over 80/443).
  services.caddy = {
    enable = true;
    virtualHosts."nixpkgs.fyi".extraConfig = ''
      reverse_proxy ${config.services.nixpkgs-maintenance.listen}
    '';
  };
  networking.firewall.allowedTCPPorts = [
    80
    443
  ];
}
