# go.nix — this module's dependencies (FDR 0008); go.mod, gomod2nix.toml and
# the package graph are rendered or derived from it inside nix. Edit through
# the escape hatch (godyn-go) or by hand.
{
  flakeInputs = {
    "code.linenisgreat.com/purse-first/libs/go-mcp" = {
      input = "purse-first";
      subPath = "libs/go-mcp";
    };
  };
  go = "1.26";
  module = "code.linenisgreat.com/roustabout";
  replace = { };
  require = {
    "mvdan.cc/sh/v3" = {
      go = "1.25.0";
      hash = "sha256-FM+2xpGELZLm4Gc09OE5Z3JeNLPMvzeb1wRcdAIeKy4=";
      indirect = true;
      version = "v3.13.1";
    };
  };
}
