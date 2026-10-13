# This repo's conformist overlay, merged with conformist.lib.presets.eng and
# .eng-go in flake.nix. The presets enable the eng-convention linters and the
# canonical Go formatter chain (goimports, gofumpt); here: nixfmt for the
# flake itself, and the repo-specific tweaks.
{ ... }:
{
  programs.nixfmt.enable = true;

  linters.eng-versioning.key = "ROUSTABOUT_VERSION";

  # Prose and generated files are out of scope for code formatters.
  settings.excludes = [
    "*.md"
    "flake.lock"
    "LICENSE"
  ];
}
