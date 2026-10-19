# Generalizing the graph loader

Status: design, not implemented. The decisions below are circus FDR-0043's
decisions 18 to 22 (operator-approved 2026-10-19; the reconciling note is
circus `docs/plans/2026-10-19-bricks-reconciliation.md`). What is built today is
under "Where the code stands". Origin: the operator's direction that roustabout
hard-codes no graphs.

## What roustabout is

Two things and nothing else.

1. **A grammar for workflows.** The alphabet is node kinds: agents, decisions,
   scripts (commit and sink), and later the gates and `switch`. The rules say
   which shapes are well-formed. A workflow document is a content-addressed
   sentence of the grammar; the railway diagram is the picture of one sentence
   and the EBNF the picture of the rules.
2. **An interpreter for sentences.** It walks a document node by node: agents
   through juggler, scripts through ringmaster, taking the edge each outcome
   names, with one ledger entry per node. It is strictly scoped (a node gets
   only what its kind and the document declare; a write tool is conferred on
   one node and never held by the interpreter) and deterministic (what runs
   next is a function of the document and the outcome; an agent's output is not
   deterministic, the walk is).

**Outside roustabout**, and stated as such: who may pin which grammar
(FDR-0043 decision 13, with spinclass FDR 0032 and moxy FDR 0013); the trusted
value a `switch` reads (decision 14, principals); and who writes grammars and
sentences (the host).

## Where the code stands

Steps 1 to 3 of decision 22 are merged. The loader (`internal/graph`) has the
kind table (outcomes, required outcomes and may-write per kind), finds the four
nodes by kind with labels as display names only, takes the entry from the
document's optional `entry` field or the one node no edge leads to, checks every
edge against the source node's outcomes, and refuses unreachable nodes and
cycles. It still enforces exactly the issue-filer topology on top of that: one
node of each of agent, decide, commit and sink, and five fixed edges. The
interpreter (`internal/run`) is a walker: from the entry it runs each node by
its kind (agent, decide, commit) and takes the edge its outcome names. Not yet
document-driven: the sink, the exit wake and `resolve` are a fixed phase after
the walk (step 4 declares them in a `result` section); freeze-and-store is part
of the agent step; there is no `switch`, `graph` or gate node; and the host
seam is `graph.Expect`. The interpreter does not yet use the kind table's
may-write fact.

Already out of the code: the judge's question names are the keys of
`questions.json`, the modes must cover exactly those keys, and the judge node
carries an explicit `gate` (one of those keys) that the loader requires and
roustabout hands to `juggler decide --question`. `just test-conformance` runs
circus's glue and `roustabout run` on the same scenarios as a regression net.

## The decisions (FDR-0043 18 to 22)

- **18. The result contract is a graph-level `result` section.** Freeze-then-store
  of an agent node's traffic file, wake-before-resolve and resolve are
  ordering invariants across nodes, and ordering that fails closed is
  roustabout's. The document declares which steps run and from which node's
  artifact, from a closed vocabulary roustabout implements (`freeze-store
  <agent node>`, `wake`, `resolve <record builder>`); it cannot add or reorder
  steps. How outcomes become `state`, `failed_checks` and `reason` is a record
  builder roustabout ships by name and version (`issue-filer/1`), not a schema
  the document writes. The grammar fixes the result list exactly and in order,
  with a step marked optional where omitting it is allowed; it is not an
  in-order subset, because a document that may drop `freeze-store` would judge
  unfrozen traffic. `freeze-store <node>` must name a node of kind agent.
- **19. `switch` is a node kind whose outcome names a case.** Edges stay
  `[from, cond, to]`. `cond` must be in the outcome set of the source node: for
  `agent`, `decide`, `commit` and `sink` that is fixed by the kind; for `switch`
  it is computed from the kind plus the node document (the declared case names,
  plus `fail` for no match). The reason for the flat form is interpreter
  uniformity: edges in one list mean the checks, the walker and the ledger each
  have one loop over one shape, with the `graph` node the single nested ledger
  entry. A composite authoring syntax is acceptable as long as it desugars to a
  `switch` node plus one edge per case. Hashing is not the objection: a composite
  whose cases are node digests is a Merkle subtree, as the `graph` node is.
- **20. The judge is one `decide` brick.** Its questions, their modes and its gate
  are internal; it is one juggler call, with the threshold applied per question
  by the interpreter. The modes read the draft's repository listing, which is
  whole-run state, so they are not independent legs. `all`, `any` and `not`
  compose whole bricks, never the questions inside a `decide`. How `decide`
  behaves across several questions with one gate is juggler's to state (clown);
  here it is known only from the flags the glue passes.
- **21. Kinds are roustabout's; grammars are the host's.** What a node kind is,
  its outcome set, its required outcomes, its parameter schema and whether it
  may hold a write tool is a Go table in roustabout. It is the trust boundary:
  only a roustabout change adds a kind. `script` splits into `commit`
  (write-capable) and `sink` (never), so the table can carry that fact; a
  grammar over a single `script` kind could not tell them apart. A grammar is a
  document the host supplies, pinned by digest in `graph.Expect` and checked by a
  checker roustabout ships; the graph document never carries the grammar that
  validates it. The checker refuses a grammar version it does not know, and a
  grammar edge whose `cond` is not in its kind's outcome set.
- **22. Refactor order, each step behind `just test-conformance`:** edge
  conditions as node outcome sets (with the commit/sink split); topology-free
  structure checks and the document-named entry; the walker, with freeze, store,
  wake and resolve left as a fixed post-walk phase in code; the `result` section
  last. The host seam (`graph.Expect`) exists and is not a step.

## Structure checks that name no topology

Node ids are digests of the node documents; the graph digest covers the
document; every edge endpoint is a node; every edge `cond` is in the source
node's outcome set; every node has an edge for each of its kind's required
outcomes (an `agent` or `decide` needs both `ok` and `fail`; a `commit` or
`sink` needs none); every node is reachable from the entry (the unique node with
no inbound edge, or an explicit `entry`); and a cycle is refused until a bound
is part of the document. Grammar checks sit on top of these and are local to the
flat document: each node's kind is in the grammar's kinds, each
`[kind(from), cond, kind(to)]` is in its edges (a `switch` case edge uses a
wildcard), the constraints hold (entry kind, one fail target, per-kind counts,
which gates may appear), and the document's `result` list matches the grammar's.
Binding a node to reviewed content (a questions digest, a program allowlist) is
`graph.Expect`'s, not the grammar's: the grammar says shape, the expectation
says content.

## What stays roustabout's, and what is the document's

Roustabout's, whatever the graph: digest verification of the document and every
file it names; node-kind semantics; the run key and idempotency; the run ledger;
ordering that fails closed (frozen and stored before judged; nothing committed
that was not judged); credential hygiene (the commit token minted into a `0600`
file and passed by path, a scrubbed environment for children); write tools held
pass-only and conferred on the one commit node; time limits and signal handling;
and, once spinclass FDR 0032 gives it a key, signing the run record, per-node
narrowing (moxy FDR 0013) and grants. Those last three are not started and are
other sessions' to settle.

The document's: topology; each node's parameters (brief, moxin, questions,
thresholds, modes, programs, gate); the runtime pins (juggler, moxy, glue store
paths); and, once step 4 exists, the `result` section.

The rule: if a value changes what ran, it is in the document and covered by its
digest; if it changes whether a run is trustworthy, it is roustabout's and the
document cannot override it.

## Choosing a question set per request source

Circus owns the sets. The options were: **A**, one graph per source (circus
picks the graph directory before calling `roustabout run GRAPH_DIR`; no loader
change; the digest names exactly which questions were asked; nodes are
content-addressed, so the draft, commit and sink node files are shared and only
the judge node and its questions file differ); **B**, one graph with several
sets in the judge node (the digest then no longer says which was used, and the
record shape is circus's contract); **C**, a `switch` node routing on a request
attribute to one of several judge nodes. FDR-0043 decision 14 takes A now and C
once principals exist; under C the value `switch` reads is the caller's
authenticated principal, supplied by the runtime, never read from the request
body. Consequence for consumers: under A, `failed_checks` carries names from the
graph that ran, so records from different sources use different vocabularies.
