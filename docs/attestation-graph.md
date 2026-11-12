# The attestation graph on the merge host

Status: design, operator-approved on the three recommendations below
(roustabout#6, 2026-11-12). Only the first slice (the grammar right's name) is
built. Context decided by the operator: circus EPIC 0001 and FDR 0042 as amended.

## What is decided elsewhere

- The attestation graph runs on the merge host (flic or nikulin) under that
  host's ringmaster. A terminal `sc merge` or `sc run` requests the run
  synchronously and waits; an MCP merge requests it earlier through a session
  tool.
- roustabout mints the run root as a fresh principal under the requesting
  session's grant chain. The graph holds no authority of its own, and the root
  is distinct from the session.
- Nodes are unattended juggler agents reading the session's worktree read-only
  at the diff against the merge base. The only node write is `emit_issue`. A
  script fails the run on any P0 to P2 finding.
- The run's result is the parent job's terminal record, carrying roustabout's
  run record (graph digest, each node's digest and job id, the verdict).
- A grammar is an ambient right named by its digest:
  `roustabout:grammar:<digest>`.

Not determined, and not decided here: the request transport; whether the run
root is minted on request or at slot time; read-only worktree mounting
(spinclass's lane).

## The three decisions this document records

1. **`emit_issue` is the `commit` kind.** The may-write fact, the one-run
   token and the host's program allowlist (`graph.Expect.CommitProgram`) are
   already built for `commit`. A new write-capable kind would duplicate them
   and widen the trust boundary (decision 21: only a roustabout change adds a
   kind).
2. **The attestation grammar has its own record builder, `attest/1`.** The
   issue-filer record (`issue-filer/1`, CONTRACT.md's shape) is untouched.
   `attest/1` writes the record a gate can digest: the verdict, the graph
   digest, and the run-level ledger (each node's digest, kind, job id, verdict
   and edge). Today the ledger is only in the stdout summary; under `attest/1`
   it is in the record, so the parent job's terminal record is what a gate
   references. This uses the result section's existing "record builder named
   by the document" seam (FDR-0043 decision 18).
3. **The grammar chooses the request type.** A pinned grammar fixes the
   request parser along with the shape of the graph, so a run's inputs cannot
   drift from the graph that was reviewed. `request.Parse` is
   issue-filer-specific today; the attestation request is a second type
   (session key, worktree path, merge base, diff range, requesting
   principal), selected by the grammar and refused when the grammar names none
   the build ships.

## What the grammar needs that the loader does not have

The loader still enforces the issue-filer topology (one agent, decide,
commit and sink, five fixed edges). An attest-seq grammar, five agent nodes, a
coverage-audit script, a cutoff script and `emit_issue`, with no judge, needs:

- several agent nodes, run in sequence: drop the topology-bound checks and let
  the walker run more than one agent node (the grammar's `max_nodes_per_kind`
  already carries the count);
- a script kind that never writes and is not the sink, for the audit and the
  cutoff, or those nodes reuse `sink` (to be decided when the slice is cut;
  a kind-table change, roustabout's by decision 21);
- a `result` list and builder of its own (`attest/1`), since the fixed
  post-walk phase assumes a judged run;
- a request type for the attestation request (decision 3 above).

A script failing the run on a P0 to P2 finding is a script node taking its
`fail` edge, which the walker already does.

## The grammar right

roustabout declares the right a run needs as the string
`roustabout:grammar:<digest>`, where `<digest>` is the `sha256:<hex>` digest
the host already pins the grammar by. The name is derived, never supplied by
the document. Checking it against the run's grant chain waits for a grant
chain to exist to read (spinclass FDR 0032, moxy FDR 0013); until then the
digest pin in `graph.Expect` is the only authority and `check` reports the
right so a host can wire it. This is the first slice and is built:
`graph.GrammarRight`, and `grammar_right` in `roustabout check`'s output when
a grammar is pinned.

## Order

1. The grammar right's name (built).
2. The attestation request type and its parser, selected by the grammar
   (built: a grammar's optional `request` field names `issue-filer/1`, the
   default and absent from older grammars so their digests hold, or `attest/1`;
   `request.ParseAttest` reads `session_key`, `worktree`, `merge_base`, `head`,
   `principal` and an optional `request_id`. `roustabout run` does not yet
   dispatch on it: it refuses a grammar that names `attest/1` rather than run
   it on an issue-filer request, until the walker runs an attestation graph,
   step 3.)
3. The walker running several agent nodes; the topology-bound loader checks
   moved to the grammar. Built as a separate path, so the pinned issue-filer
   one cannot move (operator-approved, 2026-11-12):
   - `graph.LoadAttest` reads an attestation graph under a pinned `attest/1`
     grammar (it refuses a graph without one): agents in a sequence of ok
     edges, `check` scripts, at most one `commit`, exactly one `sink`, an agent
     entry, and the result `[{resolve, attest/1}]`. An agent node declares
     `brief` (a digest, the file in `briefs/<digest>.toml`), `moxin`, `model`
     and `worktree: "ro"`; the worktree path is the request's, never the
     document's.
   - `check` is a new script kind that never writes (the `sink`'s fail
     semantics differ): outcomes ok and fail, a fail edge required.
   - `run.AttestRunner` walks it: each agent is spawned as the run root with its
     own brief and a task naming the worktree and the diff; a `check` or the
     `commit` is a ringmaster job; the `commit` alone is conferred a
     one-run token in a `0600` file; a fail edge runs the sink and fails the
     run; the run resolves with the `attest/1` record. `roustabout run`
     dispatches on the pinned grammar's request type.
   - Not done in this slice: the agents' `model` and `worktree: ro` are
     validated and in the digest, but the runner does not yet pass them to
     juggler (`juggler spawn` has no flag for either that this repo knows of)
     and the read-only mount is spinclass's lane; the run root is minted
     under `--issuer`, not yet under the requesting principal's grant chain.
   - `roustabout check` follows the pinned grammar as `run` does: it loads an
     attestation graph under an `attest/1` grammar, and reports `request_type`
     (also for issue-filer graphs) and `grammar_right`.
4. Findings (built; the operator ruled the schema, 2026-11-12: `severity`,
   `path`, `summary`; a check fails the run on any P0 to P2 finding).
   - An agent's final message is exactly one JSON object,
     `{"findings":[{"severity","path","summary"}]}`, the array empty when it
     found nothing. `severity` is `P0` to `P4`; `path` and `summary` are
     non-empty and bounded; at most 500 findings a run. Anything else (prose,
     an empty message, another key, a second object) fails the agent: an
     attestation that cannot read what its agents found must not pass.
   - roustabout writes each finding, with the reporting node's label as
     `agent` (set by roustabout, never by the agent), to the run's
     `findings.jsonl` (0600, one JSON object per line). `check` is given the
     request file then the findings file; `commit` the request file, the
     findings file, then its token. Both also get `ATTEST_*` environment
     variables naming the run, the graph and the diff.
   - The P0 to P2 rule is the check program's, a host-allowlisted program like
     the commit; roustabout's part is that it reads a validated file.
   - The coverage audit and cutoff scripts are `check` nodes the host supplies;
     their programs are not in this repo.
5. `attest/1`, with the ledger in the record (built: `AttestRecord`: state
   `passed` or `failed`, run key, graph digest, session key, merge base, head,
   findings counts by severity (all five keys), the ledger, and a reason on a
   failed run).
6. Checking the right against the grant chain, once grants exist.

Each step stays behind `just test-conformance`.

## Open

- Where the minter credential lives on a dev host (today two ambient
  environment variables, `TROUPE_MINT_USER` and `TROUPE_MINT_PASSWORD_FILE`,
  handed only to the calls that create accounts or run roots), and whether
  the root can be minted under a session's grants rather than the minter's
  credential. Both are spinclass's and the host's to say.
- The request transport.
