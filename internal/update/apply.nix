# Applied by nix-eval-jobs to every derivation; the result becomes the
# `extraValue` field of the job's JSON line. Everything is coerced to plain
# JSON types and wrapped in tryEval so one odd attribute cannot drop a job.
drv:
let
  inherit (builtins)
    isAttrs
    isList
    isString
    isInt
    filter
    map
    tryEval
    deepSeq
    concatMap
    unsafeDiscardStringContext
    ;

  try =
    default: v:
    let
      r = tryEval (deepSeq v v);
    in
    if r.success then r.value else default;

  str = v: if isString v then unsafeDiscardStringContext v else null;
  int = v: if isInt v then v else null;
  list = v: if isList v then v else [ v ];
  meta = drv.meta or { };

  person = p: {
    name = str (p.name or null);
    github = str (p.github or null);
    githubId = int (p.githubId or null);
    email = str (p.email or null);
    matrix = str (p.matrix or null);
  };
  people = l: map person (filter isAttrs (list l));

  team = t: {
    shortName = str (t.shortName or null);
    github = str (t.github or null);
    scope = str (t.scope or null);
    members = try [ ] (people (t.members or [ ]));
  };

  license =
    l:
    if isAttrs l then
      str (l.spdxId or l.shortName or l.fullName or null)
    else
      str l;

  drvPathOf =
    x:
    let
      r = tryEval (if isAttrs x && x ? drvPath then unsafeDiscardStringContext x.drvPath else null);
    in
    if r.success then r.value else null;

  inputs =
    names:
    filter (x: x != null) (
      concatMap (
        n:
        let
          v = drv.${n} or [ ];
        in
        if isList v then map drvPathOf v else [ ]
      ) names
    );
in
{
  pname = try null (str (drv.pname or null));
  version = try null (str (drv.version or null));

  # Built with makeSetupHook: not software but a build helper, e.g.
  # ensureNewerSourcesForZipFilesHook. Detected by the builder script,
  # which installs nix-support/setup-hook.
  setupHook = try false (
    let
      cmd = drv.buildCommand or null;
    in
    isString cmd && builtins.length (builtins.split "nix-support/setup-hook" cmd) > 1
  );

  meta = try null {
    description = str (meta.description or null);
    longDescription = str (meta.longDescription or null);
    homepage =
      let
        h = meta.homepage or null;
      in
      if isList h then (if h == [ ] then null else str (builtins.head h)) else str h;
    licenses = filter (x: x != null) (map license (list (meta.license or [ ])));
    mainProgram = str (meta.mainProgram or null);
    position = str (meta.position or null);
    broken = (meta.broken or false) == true;
    insecure = (meta.insecure or false) == true;
    unfree = (meta.unfree or false) == true;
    maintainers = try [ ] (people (meta.maintainers or [ ]));
    # Absent on older nixpkgs, where team members were not merged into
    # `maintainers`; null then means "every maintainer is direct".
    nonTeamMaintainers = if meta ? nonTeamMaintainers then try [ ] (people meta.nonTeamMaintainers) else null;
    teams = try [ ] (map team (filter isAttrs (list (meta.teams or [ ]))));
  };

  # Dependency drv paths per kind; used to label the direct input
  # derivations reported by --show-input-drvs.
  deps = try { } {
    propagated = inputs [
      "propagatedBuildInputs"
      "depsTargetTargetPropagated"
    ];
    host = inputs [
      "buildInputs"
      "depsHostHost"
      "depsHostHostPropagated"
    ];
    native = inputs [
      "nativeBuildInputs"
      "propagatedNativeBuildInputs"
      "depsBuildBuild"
      "depsBuildBuildPropagated"
      "depsBuildTarget"
      "depsBuildTargetPropagated"
    ];
    check = inputs [
      "nativeCheckInputs"
      "checkInputs"
      "nativeInstallCheckInputs"
      "installCheckInputs"
    ];
  };
}
