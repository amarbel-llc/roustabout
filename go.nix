# go.nix — this module's dependencies (igloo FDR 0008, godyn(7)); go.mod,
# gomod2nix.toml and the package graph are rendered or derived from it inside
# nix. Edit through the escape hatch (godyn-go) or by hand.
{
  module = "code.linenisgreat.com/roustabout";
  go = "1.26";
  flakeInputs = {
    "code.linenisgreat.com/purse-first/libs/go-mcp" = {
      input = "purse-first";
      subPath = "libs/go-mcp";
    };
  };
  replace = { };
  require = {
    # go-mcp/command's own (sole) third-party dependency, used by its shell
    # arg parsing; pinned the same way circus-cli (the other go-mcp command
    # consumer) pins it.
    "mvdan.cc/sh/v3" = {
      go = "1.25.0";
      hash = "sha256-FM+2xpGELZLm4Gc09OE5Z3JeNLPMvzeb1wRcdAIeKy4=";
      indirect = true;
      version = "v3.13.1";
    };
  };
}
