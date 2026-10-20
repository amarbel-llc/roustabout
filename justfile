# this repo's justfile. Conventions: conformist-justfile(7). `default` is the
# read-only CI lane, the whole local CI: `just` passing means the flake
# evaluates, the tree is conformant, the binary builds, the Go tests pass and
# `roustabout run` does what conformance/expected/ pins on every scenario.

conformance_tools := "nixpkgs#jq nixpkgs#coreutils nixpkgs#gnused nixpkgs#gnugrep nixpkgs#gawk nixpkgs#util-linux nixpkgs#diffutils nixpkgs#findutils"

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

# Builds the conformance kit (stand-ins and graph documents, conformance/default.nix)
# and roustabout, runs each scenario through `roustabout run` and diffs what it
# did against conformance/expected/ (conformance/run.bash). Nothing outside this
# repo is read. SCENARIOS select by name prefix, e.g. `just test-conformance b c e`.
#
# compare roustabout run with its pinned expectations on the conformance scenarios
[group('test')]
test-conformance *scenarios:
    #!/usr/bin/env bash
    set -euo pipefail
    kit=$(nix build .#conformance-kit --no-link --print-out-paths)
    rou=$(nix build --no-link --print-out-paths)/bin/roustabout
    nix shell --inputs-from . {{ conformance_tools }} -c bash conformance/run.bash "$kit" "$rou" {{ scenarios }}

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

codemod: codemod-fmt codemod-conformance-pins

# Rewrites conformance/expected/ from a fresh run of every scenario (SCENARIOS
# select by name prefix). Read the diff in git before committing it: a pin that
# moves is a change in what `roustabout run` does.
#
# rewrite the conformance expectations from a fresh run
[group('codemod')]
codemod-conformance-pins *scenarios:
    #!/usr/bin/env bash
    set -euo pipefail
    kit=$(nix build .#conformance-kit --no-link --print-out-paths)
    rou=$(nix build --no-link --print-out-paths)/bin/roustabout
    PIN=1 nix shell --inputs-from . {{ conformance_tools }} -c bash conformance/run.bash "$kit" "$rou" {{ scenarios }}

codemod-fmt: codemod-fmt-tree

# format the tree in place (repair mode) via `nix fmt`
codemod-fmt-tree:
    nix fmt
