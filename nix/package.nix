{
  lib,
  buildGoModule,
  makeWrapper,
  nix,
  nix-eval-jobs,
}:

buildGoModule {
  pname = "nixpkgs-maintenance";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../main.go
      ../internal
    ];
  };

  vendorHash = "sha256-7IC/p5GlD2EZkDXQzkaZ7E19S/ABKEBsg68vt8pykis=";

  nativeBuildInputs = [ makeWrapper ];

  postInstall = ''
    mv $out/bin/nixpkgs-maintenance $out/bin/nixmaint
    # nix-eval-jobs is pinned; nix (for nix-prefetch-url) only serves as a
    # fallback so the system's nix is used where available.
    wrapProgram $out/bin/nixmaint \
      --prefix PATH : ${lib.makeBinPath [ nix-eval-jobs ]} \
      --suffix PATH : ${lib.makeBinPath [ nix ]}
  '';

  meta = {
    description = "Extract nixpkgs packages, maintainers and dependencies into SQLite and browse unmaintained packages";
    mainProgram = "nixmaint";
  };
}
