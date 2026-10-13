# this repo's justfile. Conventions: conformist-justfile(7). `default` is the
# read-only CI lane; `just` passing means the tree is conformant. Go only
# (FDR-0043): no graph runtime yet, just the `roustabout version` scaffold.

default: build test

# --- build ---

build: build-go

# build the roustabout binary (godyn(7): no ambient go, deps in ./go.nix)
[group('build')]
build-go:
    #!/usr/bin/env bash
    set -euo pipefail
    nix build --no-link --print-build-logs

# --- test ---

test: test-go

# run the Go test suite via godyn's per-package test lane
[group('test')]
test-go:
    #!/usr/bin/env bash
    set -euo pipefail
    system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
    nix build ".#checks.${system}.tests" --no-link --print-build-logs

# --- lint ---

lint: lint-fmt

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
