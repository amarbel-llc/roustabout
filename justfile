# this repo's justfile. Conventions: conformist-justfile(7). `default` is the
# read-only CI lane, the whole local CI: `just` passing means the flake
# evaluates, the tree is conformant, the binary builds, the Go tests pass and
# `roustabout run` matches circus's glue on its scenarios. The last needs a
# circus checkout (see test-conformance).

default: validate lint build test

# --- validate ---

validate: validate-nix

# `nix flake check` evaluates every flake output and builds every check
# (formatting, tests, layout).
#
# run nix flake check
[group('validate')]
validate-nix:
    nix flake check

# --- build ---

build: build-go

# build the roustabout binary (godyn(7): no ambient go, deps in ./go.nix)
[group('build')]
build-go:
    #!/usr/bin/env bash
    set -euo pipefail
    nix build --no-link --print-build-logs

# --- test ---

test: test-go test-conformance

# run the Go test suite via godyn's per-package test lane
[group('test')]
test-go:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.tests" --no-link --print-build-logs

# Builds circus's issue-filer glue against its own stand-ins (circus's
# infra/hosts/pkgs/issue-filer-glue/test; read-only, nothing in circus is
# written) and roustabout, runs the glue's scenarios through both and diffs
# the results (conformance/compare.bash). It needs a circus checkout at HOSTS
# (default ~/eng/repos/circus/infra/hosts), so a merge here fails when that
# checkout is missing or its glue is broken. SCENARIOS select by name prefix,
# e.g. `just test-conformance "" b c e`.
#
# compare roustabout run with circus's glue on the glue's own scenarios
[group('test')]
test-conformance hosts=(env('HOME') / "eng/repos/circus/infra/hosts") *scenarios:
    #!/usr/bin/env bash
    set -euo pipefail
    glue=$(cd {{ hosts }} && nix build --no-link --print-out-paths --impure --inputs-from . --expr \
      'let pkgs = (builtins.getFlake "nixpkgs").legacyPackages.${builtins.currentSystem}; in import ./pkgs/issue-filer-glue/test { inherit pkgs; }')
    rou=$(nix build --no-link --print-out-paths)/bin/roustabout
    GLUE_NIX={{ hosts }}/pkgs/issue-filer-glue/default.nix \
      nix shell --inputs-from . nixpkgs#jq nixpkgs#coreutils nixpkgs#gnused nixpkgs#gnugrep nixpkgs#gawk \
      nixpkgs#util-linux nixpkgs#diffutils nixpkgs#findutils -c \
      bash conformance/compare.bash "$glue" "$rou" {{ scenarios }}

# --- lint ---

lint: lint-fmt lint-layout lint-impure

# internal/<level>/<leaf> must sit at the NATO level dagnabit computes from its
# imports (checks.layout). On failure the log names each package and the level
# to move it to; move it by hand (git mv) and fix the import paths.
#
# check the Go package levels against dagnabit
[group('lint')]
lint-layout:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.layout" --no-link --print-build-logs

# Read-only formatting + the eng/eng-go preset's file-based linters, via the
# sandboxed checks.formatting derivation.
#
# run the read-only formatting check
lint-fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.formatting" --no-link --print-build-logs

lint-impure: lint-worktree

# The impure eng checks (git remotes, sweatfile, agents-md) against the working
# tree, where .git is available. Runs conformist from the devShell (direnv
# `use flake`).
#
# run the impure eng checks against the working tree
lint-worktree:
    #!/usr/bin/env bash
    set -euo pipefail
    cfg=$(nix build --no-link --print-out-paths '.#conformist-impure-config')
    conformist check --config-file "$cfg" --tree-root .

# --- codemod ---

codemod-fmt: codemod-fmt-tree

# format the tree in place (repair mode) via `nix fmt`
codemod-fmt-tree:
    nix fmt
