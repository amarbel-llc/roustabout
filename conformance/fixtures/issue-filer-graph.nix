# infra/hosts/pkgs/issue-filer-graph/default.nix: the issue-filer graph as a
# content-addressed document (FDR-0041, design P3). The graph is
#
#   draft (agent) --ok--> judge (decide) --ok--> commit (script)
#     \--fail--> sink      \--fail--> sink        \--fail--> sink (script)
#
# and this package writes it down so that a run can name EXACTLY what it ran:
# each node is a small JSON document of its own, its id is `sha256:<hex>` of
# that document's bytes, the edges name ids, and the graph's own digest
# (`graph.digest`) is the sha256 of `graph.json`. Two graphs that differ in a
# threshold, a brief, a question or a script path are two different graphs.
# The judge node also carries `gate`, the question it hands to `juggler decide
# --question`, when one is given; without one the node has no such field.
#
# Digests are computed at eval time without import-from-derivation (which the
# host evals avoid, see home/moxins.nix): a node's id is
# `builtins.hashString` of the exact JSON text that is then written to
# nodes/<id>.json, so `sha256sum nodes/<id>.json` reproduces it; the brief's
# and the questions' digests are `builtins.hashFile` on the source files. The
# moxin and the scripts are named by store path, which nix already
# content-addresses.
#
# Output:
#   graph.json           the document (nodes keyed by id, edges, schema 1)
#   graph.digest         "sha256:<hex of graph.json>", one line
#   nodes/<id>.json      each node's document, as hashed
#   drafter.brief.toml   the drafter's brief template (markers unfilled)
#   questions.json       Jev's questions, as the judge node names them
#
# The glue (pkgs/issue-filer-glue) interprets exactly this shape and refuses
# any other; it reads the brief and questions from here, so what it runs is
# what the digest names.
{
  pkgs,
  # The drafter's brief template (krone/issue-filer/issue-drafter.brief.toml).
  drafterBrief,
  # Jev's question set (krone/pebble-graph/approve-question-split-v2.json).
  questions,
  # Per-question confidence threshold the judge node carries, as a decimal
  # string; the glue reads it from the graph.
  minConfidence ? "0.5",
  # The question the judge hands to `juggler decide --question` (FDR-0043
  # decision 15). It must be one of the question file's keys. roustabout
  # 0f35d3ab refuses a judge node without one; null leaves the node without a
  # gate, as it was.
  gate ? null,
  # Per-question mode for the judge (circus#335, shadow attestation D1):
  # "enforce" (a failure rejects the run), "shadow" (recorded, never rejects),
  # or "enforce-public" (enforce when any draft targets a public repo, shadow
  # when every draft targets a private one). Keys must be exactly the question
  # file's names. null (the default) makes every question enforce.
  questionModes ? null,
  # The `issues` moxin (pkgs/pebble-graph-tools with serverName = "issues").
  issuesMoxin,
  # pkgs/issue-filer-commit: the commit node's program is its bin/issues-commit.
  commitScript,
  # The executable under commitScript/bin that the commit node names; the
  # shadow graph's stub (pkgs/issue-filer-commit-stub) is issues-commit-stub.
  commitBin ? "issues-commit",
  # A program the failure sink runs with the result JSON on stdin, or null for
  # the tracer bullet's no-op sink (the node's program is then "none").
  sinkScript ? null,
  # The build that interprets the graph (circus#314, roustabout's graph.go
  # Runtime): { juggler, moxy, glue } store paths, or null for a document that
  # names none (the digest then does not cover the runtime, as today). When set
  # it goes into graph.json, so two graphs that differ only in the clown build
  # are two graphs. `glue` is roustabout's own build; while the bash glue is
  # the interpreter it cannot be named here (it takes this graph as an input),
  # so krone leaves this null until roustabout is a flake input.
  runtime ? null,
}:
let
  inherit (pkgs.lib) concatStringsSep mapAttrsToList;
  questionNames = builtins.attrNames (builtins.fromJSON (builtins.readFile questions));
  modes =
    if questionModes == null then pkgs.lib.genAttrs questionNames (_: "enforce") else questionModes;
  validModes = [
    "enforce"
    "shadow"
    "enforce-public"
  ];
  modesOk =
    builtins.attrNames modes == questionNames
    && builtins.all (m: builtins.elem m validModes) (builtins.attrValues modes);
  digestOfFile = path: "sha256:${builtins.hashFile "sha256" path}";
  nodes = {
    draft = {
      kind = "agent";
      label = "draft";
      brief = digestOfFile drafterBrief;
      moxin = "${issuesMoxin}";
    };
    judge = {
      kind = "decide";
      label = "judge";
      questions = digestOfFile questions;
      min_confidence = builtins.fromJSON minConfidence;
      inherit modes;
    }
    // pkgs.lib.optionalAttrs (gate != null) { inherit gate; };
    # commit and sink were one kind, `script`, until FDR-0043 decision 21
    # (2026-10-19): a grammar over kinds could not tell the node that is
    # conferred the write token from the node that must never get it. The
    # loader (roustabout 4031ea89) knows both kinds; the programs run the same.
    commit = {
      kind = "commit";
      label = "commit";
      program = "${commitScript}/bin/${commitBin}";
    };
    sink = {
      kind = "sink";
      label = "sink";
      program = if sinkScript == null then "none" else "${sinkScript}";
    };
  };
  # A node document is its attrs as canonical JSON (builtins.toJSON sorts
  # keys), and its id is the digest of exactly that text. hashString refuses a
  # string that carries a store-path context (the moxin and program paths), so
  # the context is dropped for the hash only; the file is written from the
  # original text so the paths stay dependencies of the output.
  docs = builtins.mapAttrs (_: builtins.toJSON) nodes;
  ids = builtins.mapAttrs (
    _: doc: "sha256:${builtins.hashString "sha256" (builtins.unsafeDiscardStringContext doc)}"
  ) docs;
  graph = {
    schema = 1;
    review = "all-or-nothing";
  }
  // pkgs.lib.optionalAttrs (runtime != null) {
    runtime = {
      juggler = "${runtime.juggler}";
      moxy = "${runtime.moxy}";
      glue = "${runtime.glue}";
    };
  }
  // {
    nodes = builtins.listToAttrs (
      mapAttrsToList (name: attrs: {
        name = ids.${name};
        value = attrs;
      }) nodes
    );
    edges = [
      [
        ids.draft
        "ok"
        ids.judge
      ]
      [
        ids.judge
        "ok"
        ids.commit
      ]
      [
        ids.draft
        "fail"
        ids.sink
      ]
      [
        ids.judge
        "fail"
        ids.sink
      ]
      [
        ids.commit
        "fail"
        ids.sink
      ]
    ];
    # The result contract (FDR-0043 decision 18): which post-walk steps run and
    # from which node's artifact, from roustabout's closed vocabulary. The
    # loader (roustabout 802473a2 and later) refuses any other step, order or
    # field; a document without the section reads as exactly this list until
    # roustabout 4c makes the section required. `wake` is sent only when the
    # request names a notify target; declaring it keeps the document honest
    # about what the interpreter may do. `issue-filer/1` names the record
    # builder that turns outcomes into the CONTRACT.md result record.
    result = [
      {
        step = "freeze-store";
        node = ids.draft;
      }
      { step = "wake"; }
      {
        step = "resolve";
        builder = "issue-filer/1";
      }
    ];
  };
  graphJson = pkgs.writeText "issue-filer-graph.json" (builtins.toJSON graph);
  nodeFiles = concatStringsSep "\n" (
    mapAttrsToList (
      name: doc: "cp ${pkgs.writeText "issue-filer-node-${name}.json" doc} $out/nodes/${ids.${name}}.json"
    ) docs
  );
  gateOk = gate == null || builtins.elem gate questionNames;
in
if
  runtime != null
  &&
    builtins.attrNames runtime != [
      "glue"
      "juggler"
      "moxy"
    ]
then
  throw "issue-filer-graph: runtime must name exactly juggler, moxy and glue"
else if !modesOk then
  throw "issue-filer-graph: questionModes must name exactly the question names (${concatStringsSep ", " questionNames}), each enforce | shadow | enforce-public"
else if !gateOk then
  throw "issue-filer-graph: gate must be one of the question names (${concatStringsSep ", " questionNames}), got \"${toString gate}\""
else if builtins.match "[0-9]+(\\.[0-9]+)?" minConfidence == null then
  throw "issue-filer-graph: minConfidence must be a decimal number as a string, got \"${toString minConfidence}\""
else
  pkgs.runCommand "issue-filer-graph" { nativeBuildInputs = [ pkgs.coreutils ]; } ''
    mkdir -p $out/nodes
    cp ${graphJson} $out/graph.json
    ${nodeFiles}
    cp ${drafterBrief} $out/drafter.brief.toml
    cp ${questions} $out/questions.json
    sha256sum $out/graph.json | cut -d' ' -f1 | sed 's/^/sha256:/' > $out/graph.digest
    # The ids are reproducible from the files: a node file's sha256 is its id.
    for f in $out/nodes/sha256:*.json; do
      id="$(basename "$f" .json)"
      [ "sha256:$(sha256sum "$f" | cut -d' ' -f1)" = "$id" ] || { echo "node id $id does not match its file" >&2; exit 1; }
    done
  ''
