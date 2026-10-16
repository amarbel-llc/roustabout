{
  description = "roustabout: the graph runtime above juggler and ringmaster (circus FDR-0043)";

  inputs = {
    # conformist provides the linter/formatter multiplexer, its Nix module
    # library (conformist.lib), and the eng-convention presets. Following its
    # nixpkgs-master/utils pins keeps this flake's closure shared with conformist's.
    conformist.url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
    conformist.inputs.igloo.follows = "igloo";
    conformist.inputs.nixpkgs-master.follows = "nixpkgs-master";
    nixpkgs.follows = "conformist/nixpkgs-master";
    utils.follows = "conformist/utils";

    # just-us is the eng `just` fork. It supplies BOTH the devShell's `just` and
    # the justfile-orphan-summary linter module: that check reads the fork-only
    # `doc_prelude` field of `just --dump --dump-format json`, which upstream
    # `just` never emits, so running it against `pkgs.just` would pass
    # vacuously. The coupling lives in just-us rather than conformist because
    # conformist must stay strictly upstream of its consumers. Its nixpkgs /
    # flake-utils / conformist inputs follow this flake's so the closure stays
    # shared; its `bats` input deliberately keeps its own pin (see just-us's
    # flake.nix) and is unused here.
    just-us.url = "https://code.linenisgreat.com/just-us/archive/master.tar.gz";
    just-us.inputs.nixpkgs.follows = "nixpkgs";
    just-us.inputs.flake-utils.follows = "utils";
    just-us.inputs.conformist.follows = "conformist";
    just-us.inputs.igloo.follows = "igloo";
    just-us.inputs.nixpkgs-master.follows = "nixpkgs-master";
    just-us.inputs.clown.inputs.purse-first.follows = "purse-first";

    # igloo carries the Go build helpers: godyn (buildGoAuto, the go.nix
    # manifest, godyn-go / godyn-test) and mkGoPkgs. See godyn(7).
    igloo.url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
    igloo.inputs.nixpkgs-master.follows = "nixpkgs";

    # Source of the code.linenisgreat.com/purse-first/libs/go-mcp module,
    # bridged via go.nix's flakeInputs (its go-pkgs, RFC 0001): the `command`
    # framework roustabout's single `version` verb is built on.
    purse-first.url = "https://code.linenisgreat.com/purse-first/archive/master.tar.gz";
    purse-first.inputs.igloo.follows = "igloo";
    purse-first.inputs.nixpkgs-master.follows = "nixpkgs";
    purse-first.inputs.utils.follows = "utils";
    purse-first.inputs.conformist.follows = "conformist";
    nixpkgs-master.url = "github:NixOS/nixpkgs/b4fd65b198c599cbe814fcb9f42d25d021595ec9";
  };

  outputs =
    inputs@{
      self,
      conformist,
      just-us,
      nixpkgs,
      utils,
      igloo,
      purse-first,
      ...
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        # igloo's pkgs carry everything plain nixpkgs does, plus the godyn
        # overlay (buildGoAuto, godyn-go, godyn-test) used below.
        pkgs = import igloo { inherit system; };

        roustaboutVersion = builtins.head (
          builtins.match ".*ROUSTABOUT_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
        );
        roustaboutCommit = self.shortRev or self.dirtyShortRev or "unknown";

        # godyn build (igloo FDR 0008, godyn(7)): dependencies live in
        # ./go.nix (no tracked go.mod/gomod2nix.toml, no ambient go); go.mod
        # and gomod2nix.toml are rendered from it inside nix, the same way
        # circus-cli (circus/circus-cli/default.nix) builds its own binary.
        roustabout = pkgs.buildGoAuto {
          pname = "roustabout";
          version = roustaboutVersion;
          inherit inputs;
          src = ./.;
          manifest = ./go.nix;
          subPackages = [ "cmd/roustabout" ];
          # godyn's per-package test lane (passthru.checkAll).
          tests = true;
          nativeArgs = {
            commit = roustaboutCommit;
          };
          bgaArgs = {
            commit = roustaboutCommit;
            GOTOOLCHAIN = "local";
          };
          # The binary writes its own section-1 manpage and bash/zsh/fish
          # completions from the command definitions (purse-first's go-mcp
          # command framework).
          postInstall = ''
            $out/bin/roustabout generate-artifacts $out
          '';
        };

        # `.default` is the bga (buildGoApplication) build on every system —
        # platform-agnostic, no ca-derivations needed. (`.conformist-native` is an
        # opt-in godyn build, x86_64-linux only.)
        conformistPkg = conformist.packages.${system}.default;

        # The fork's `just`: the devShell's command runner AND the binary EVERY
        # justfile-* linter invokes. Never substitute `pkgs.just` — those checks
        # read fork-only parser data, so a stock `just` either rejects the format
        # outright or reads as zero findings.
        justPkg = just-us.packages.${system}.default;

        # Pure lane: the eng + eng-go presets (eng-go(7): goimports then
        # gofumpt, this repo's only language) + the just-us justfile roster +
        # this repo's own formatters/excludes (./conformist.nix). Drives
        # `nix fmt` and the sandboxed `checks.formatting`.
        eval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            conformist.lib.presets.eng-go
            # The justfile-convention linters ship from just-us, NOT conformist.
            # They read `just --dump --dump-format model` (and, for the
            # orphaned-summary rule, `doc_prelude`) — fork-only parser data — so
            # the coupling lives in the repo that owns the parser, and conformist
            # stays strictly upstream of it. conformist-justfile(7) remains the
            # normative prose home for the rules themselves.
            just-us.lib.conformistPresets.justfile
            ./conformist.nix
          ];
          package = conformistPkg;

          # ONE setting for the whole justfile-linter family: every rule in the
          # roster reads it. It MUST be the fork.
          linters.justfile-common.justPackage = justPkg;
        };

        # Impure lane: the git-state checks (git-remotes, sweatfile, agents-md).
        # They need a live .git / host tools, so they run via `just lint-worktree`
        # against the working tree, not the sandboxed check. Wire this in once the
        # repo has SSH remotes + a sweatfile (see the justfile).
        impureEval = conformist.lib.evalModule pkgs {
          imports = [ conformist.lib.presets.eng-impure ];
          package = conformistPkg;
          projectRootFile = "flake.nix";
        };
      in
      {
        packages.default = roustabout;
        packages.roustabout = roustabout;

        formatter = eval.config.build.wrapper;
        checks.formatting = eval.config.build.check self;
        # godyn's per-package test lane from go.nix. `just test-go` builds this.
        checks.tests = roustabout.passthru.checkAll or roustabout;
        packages.conformist-impure-config = impureEval.config.build.configFile;

        # A git pre-commit hook running THIS repo's config (conformist#47/#51):
        # `conformist --staged --exit-zero-on-fix` wrapped with the generated
        # store-path config, so it formats staged files with the SAME pinned
        # toolchain as `nix fmt` — no reliance on the formatters being on the
        # author's PATH (the silent-skip trap of a bare `conformist --staged`).
        # It is on the devShell PATH as `conformist-pre-commit`; wire the
        # sweatfile hook to that name (see ./sweatfile).
        packages.conformist-pre-commit = eval.config.build.preCommit;

        # The merge-time repair sibling (conformist#54): `conformist --commit
        # --amend --exit-zero-on-fix`, same store-pinned toolchain, on the
        # devShell PATH as `conformist-repair`. It is the supported command for a
        # spinclass pre-merge REPAIR hook. Exposed and available here; the
        # sweatfile leaves `repair = "conformist-repair"` as a documented opt-in,
        # since with the per-commit hook active the tree is already conformant at
        # merge time (see ./sweatfile).
        packages.conformist-repair = eval.config.build.repair;

        devShells.default = pkgs.mkShell {
          packages = [
            conformistPkg
            # The config-specific, toolchain-hermetic hooks, on PATH as
            # `conformist-pre-commit` / `conformist-repair` so the sweatfile can
            # name them.
            eval.config.build.preCommit
            eval.config.build.repair
            justPkg
            # No ambient go: dependencies live in go.nix (igloo FDR 0008); go
            # commands run through godyn-go, tests through godyn-test.
            pkgs.godyn-go
            pkgs.godyn-test
          ];
        };
      }
    );
}
