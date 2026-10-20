# The conformance kit: everything `roustabout run` needs to be driven through
# conformance/run.bash with no model, no XMPP and no network, and nothing from
# outside this repo. It is a build of stand-ins and graph documents:
#
#   settings.env       the flags run.bash hands roustabout, as shell assignments
#   graphs/<variant>/  the graph document directories run.bash runs
#
# The stand-ins (./standins/fake-*.bash) are doubles of the juggler, madder and
# troupe CLIs, the issues moxin's list_repos, the commit script and the sink.
# Each logs what it was given under $FAKE_LOG and behaves as FAKE_SCENARIO (and
# COMMIT / SINK / TROUPE) say. The graph builder (./fixtures/issue-filer-graph.nix)
# and its inputs (the drafter brief, the Jev question set) are a frozen copy of
# what circus builds its issue-filer graph from; see conformance/README.md for
# which circus revision and what to do when the copy is refreshed. The graph
# variants mirror circus's glue test, so the documents carry the same shape as
# the ones krone runs.
{ pkgs }:
let
  fake = name: pkgs.writeShellScript name (builtins.readFile (./standins + "/${name}.bash"));
  # Shaped like the issues moxin's output: the command is found in
  # issues/list_repos.toml the way moxy would. The other two tools are named so
  # the moxin looks complete; the commit script is a stand-in too.
  issuesMoxin = pkgs.runCommand "fake-issues-moxin" { } ''
    mkdir -p "$out/issues"
    printf 'command = "%s"\n' ${fake "fake-list-repos"} > "$out/issues/list_repos.toml"
    printf 'command = "/nonexistent/issues-list-issues"\n' > "$out/issues/list_issues.toml"
    printf 'command = "/nonexistent/issues-create-issue"\n' > "$out/issues/create_issue.toml"
  '';
  # The graph builder wants the commit node's program at bin/issues-commit.
  commitScript = pkgs.writeShellScriptBin "issues-commit" (
    builtins.readFile ./standins/fake-commit.bash
  );
  graphWith =
    sinkScript: questionModes: gate:
    import ./fixtures/issue-filer-graph.nix {
      inherit
        pkgs
        issuesMoxin
        commitScript
        sinkScript
        questionModes
        gate
        ;
      drafterBrief = ./fixtures/issue-drafter.brief.toml;
      questions = ./fixtures/approve-question-split-v6.json;
    };
  allEnforce = {
    "explicit-instruction-to-file" = "enforce";
    "one-draft-per-requested-issue" = "enforce";
    "right-repo-not-a-mirror" = "enforce";
    "draft-says-what-the-requester-said-and-nothing-else" = "enforce";
    "no-sensitive-content" = "enforce";
    "no-duplicate-of-existing-issue" = "enforce";
  };
  # krone's modes (circus#335, shadow attestation D1).
  shadowModes = {
    "explicit-instruction-to-file" = "shadow";
    "one-draft-per-requested-issue" = "shadow";
    "right-repo-not-a-mirror" = "shadow";
    "draft-says-what-the-requester-said-and-nothing-else" = "shadow";
    "no-sensitive-content" = "enforce-public";
    "no-duplicate-of-existing-issue" = "shadow";
  };
  graph = graphWith (fake "fake-sink") allEnforce "explicit-instruction-to-file";
  # The graph with its last edge (commit --fail--> sink) removed and the digest
  # recomputed, so what is refused is the wiring, not a stale digest.
  badGraph = pkgs.runCommand "issue-filer-bad-graph" { nativeBuildInputs = [ pkgs.jq ]; } ''
    mkdir -p "$out"
    cp -r ${graph}/. "$out"/
    chmod -R u+w "$out"
    jq -c 'del(.edges[4])' ${graph}/graph.json > "$out/graph.json"
    sha256sum "$out/graph.json" | cut -d' ' -f1 | sed 's/^/sha256:/' > "$out/graph.digest"
  '';
  # The graph with the judge node's gate removed, digest recomputed: a graph
  # whose judge names no gate is refused.
  noGateGraph = pkgs.runCommand "issue-filer-no-gate-graph" { nativeBuildInputs = [ pkgs.jq ]; } ''
    mkdir -p "$out"
    cp -r ${graph}/. "$out"/
    chmod -R u+w "$out"
    jq -c '(.nodes[] | select(.label == "judge")) |= del(.gate)' ${graph}/graph.json > "$out/graph.json"
    sha256sum "$out/graph.json" | cut -d' ' -f1 | sed 's/^/sha256:/' > "$out/graph.digest"
  '';
  settings = ''
    JUGGLER=${fake "fake-juggler"}
    MADDER=${fake "fake-madder"}
    TROUPE_BIN=${fake "fake-troupe"}
    ISSUES_MOXIN=${issuesMoxin}
    COMMIT_PROGRAM=${commitScript}/bin/issues-commit
    ROOM_DOMAIN=runs.example.invalid
    OPERATOR_JID=operator@xmpp.example.invalid
    ISSUER=issue-filer-test
    WAKE_USER=issue-filer-wake
    WAKE_PASS_FILE=/fake/wake.pass
    # Short, so a scenario that hangs a stand-in finishes quickly, and no pause
    # between resolve attempts.
    JUDGE_TIMEOUT=3s
    RESOLVE_RETRY_PAUSE=0s
  '';
in
pkgs.runCommand "roustabout-conformance-kit" { } ''
  mkdir -p "$out/graphs"
  cp ${pkgs.writeText "settings.env" settings} "$out/settings.env"
  ln -s ${graph} "$out/graphs/default"
  ln -s ${graphWith null allEnforce "explicit-instruction-to-file"} "$out/graphs/no-sink"
  ln -s ${
    graphWith (fake "fake-sink") shadowModes "explicit-instruction-to-file"
  } "$out/graphs/shadow"
  ln -s ${
    graphWith (fake "fake-sink") allEnforce "no-duplicate-of-existing-issue"
  } "$out/graphs/gate-dup"
  ln -s ${noGateGraph} "$out/graphs/no-gate"
  ln -s ${badGraph} "$out/graphs/bad-graph"
''
