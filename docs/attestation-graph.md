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
2. The attestation request type and its parser, selected by the grammar.
3. The walker running several agent nodes; the topology-bound loader checks
   moved to the grammar.
4. The non-writing script kind decision, then the audit and cutoff nodes.
5. `attest/1`, with the ledger in the record.
6. Checking the right against the grant chain, once grants exist.

Each step stays behind `just test-conformance`.

## Open

- Where the minter credential lives on a dev host (today two ambient
  environment variables, `TROUPE_MINT_USER` and `TROUPE_MINT_PASSWORD_FILE`,
  handed only to the calls that create accounts or run roots), and whether
  the root can be minted under a session's grants rather than the minter's
  credential. Both are spinclass's and the host's to say.
- The request transport.
