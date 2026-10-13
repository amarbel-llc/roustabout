# roustabout

roustabout is the graph runtime above juggler and ringmaster
(circus `docs/features/0043-roustabout-the-graph-runtime.md`). Nothing is
built yet beyond this scaffold: a Go module with one verb, `roustabout
version`. This README records what the runtime is for, the shape of document
it will interpret, and what moves here from circus, so that the FDR can move
from `exploring` to `proposed`.

## What it is

circus FDR-0041 builds a graph of agents and scripts (drafter, Jev judge,
commit, sink) as a bash glue in circus that accepts exactly one graph shape.
FDR-0042 wants three more graphs (attestation, release, post-merge targets).
Each would otherwise get its own glue, its own run ledger, its own idea of
which node may call which tool, and its own way of signing a result. The
layers below are settled: ringmaster is one job with a journal and a wake;
juggler is one agent with a brief, a ledger, a principal and a room. What is
missing is the layer that runs a graph of them.

roustabout is that layer. It takes a content-addressed graph document (nodes
by digest, edges by node id, the graph by its own digest), runs each node as
a ringmaster job, a juggler agent or a script, and keeps the run-level
ledger: one entry per node with the node's digest, its job, its verdict, and
the edge taken. It enforces permissions per node: each node's toolset
narrows the graph's ceiling, and roustabout is the principal that holds a
write tool such as `create_issue` as pass-only, conferring it on the commit
node and never calling it itself. It has cryptographic properties: the run
record is signed by the run's root principal under the card chain once
spinclass FDR 0032 slice 1 gives it a key, and is unsigned and advisory until
then. It is its own repository, not a verb inside clown: its rules are the
fleet's (spinclass FDR 0032, moxy FDR 0013), circus is its first consumer
rather than its owner, and FDR-0042's graphs run on hosts with no clown
session. And juggler is to be extracted from clown into its own repository
too, eventually, for the same reason — it is the agent runtime every graph
uses, not a feature of the coding-agent wrapper; roustabout depends on
juggler as a library or a binary either way.

## The graph document it accepts

The schema is derived from circus
`infra/hosts/pkgs/issue-filer-graph/default.nix`, which writes the
issue-filer graph down as exactly this shape today:

- **Nodes** are keyed by id in a `graph.json` document's `nodes` object.
  Each node is a small JSON document of its own (`{kind, label, ...}`); its
  id is `sha256:<hex>` of the canonical JSON bytes of that document — the
  node is addressed by its own digest, not by an assigned name. There are
  three node kinds: `agent` (a juggler-run agent, e.g. the drafter, which
  carries a `brief` digest and a `moxin`), `decide` (a juggler `decide` call,
  e.g. the judge, which carries a `questions` digest and a `min_confidence`
  threshold), and `script` (a plain program by store path, e.g. the commit
  node's `program`). The failure sink is a `script` node by convention
  (`program` is the literal string `"none"` when there is no sink program).
- **Edges** are `[from_id, condition, to_id]` triples: a node id, the
  condition under which the edge is taken (`"ok"` or `"fail"`), and the node
  id it leads to.
- The **judge's threshold** is the `decide` node's `min_confidence`, a plain
  number. The **six question names** a judge node answers against are
  exactly `clean`, `complete`, `dup`, `faithful`, `repo`, `request` (circus
  `infra/hosts/krone/issue-filer/CONTRACT.md`); a graph whose questions file
  does not have exactly these six keys is not this shape.
- The **graph's own digest** (`graph.digest`) is `sha256:<hex>` of the raw
  bytes of `graph.json` — the one value a run names to say exactly what
  graph it ran.

This is the shape the issue-filer graph has today — four nodes (`draft:
agent`, `judge: decide`, `commit: script`, `sink: script`) and five edges
(`draft--ok-->judge`, `judge--ok-->commit`, `draft--fail-->sink`,
`judge--fail-->sink`, `commit--fail-->sink`) — and roustabout's first
interpreter accepts exactly it; a document of any other shape is refused
before anything is started (the same refusal circus
`infra/hosts/pkgs/issue-filer-glue/default.nix` performs today, in bash,
against the document `issue-filer-graph` builds).

## The run ledger's shape

Three ledgers exist at three levels:

- **The run-level ledger** roustabout keeps (FDR-0043 decision 1): one entry
  per node, naming the node's digest, the ringmaster job (or juggler run) it
  was executed as, its verdict, and the edge taken out of it. Where this
  ledger lives — ringmaster's journal on the run job, as FDR-0041 does
  today, or roustabout's own store — is one of the FDR's open open
  questions below.
- **The per-agent ledger** juggler already records for every agent run:
  tool, kind, ok, and uris per tool call. An arguments digest per call is
  coming with clown#255 (the per-run traffic file and args digest), which
  roustabout's audits are expected to read once it lands.
- **The result record**, as circus `infra/hosts/krone/issue-filer/CONTRACT.md`
  defines it: `{state, run_key, graph_digest, issues, not_filed,
  failed_checks, traffic_blob}` (plus `reason` on every state but `filed`).
  `state` is `filed`, `rejected` or `failed`; `failed_checks` names which of
  the six judge questions did not pass; `issues` lists what was filed (even
  on a partial commit); `traffic_blob` is the madder URI of the frozen
  traffic file the run judged and committed from. This record is the
  message of the run's terminal ringmaster record today (CONTRACT.md,
  circus `infra/hosts/pkgs/issue-filer-glue/default.nix`'s `result`
  variable), and is the shape a roustabout-run graph is expected to resolve
  with too.

## The permission rules it enforces

- Each node's toolset narrows the graph's ceiling (moxy FDR 0013
  narrowing): a node is handed no more than what the graph as a whole is
  allowed.
- Write tools — a tool like `create_issue` — are held **pass-only** by
  roustabout (spinclass#362) and conferred on the one node that commits
  (the `script` node that actually calls the forge); roustabout itself never
  calls a write tool.
- Read-only node kinds never become artifacts (clown#253): a `decide` or
  `agent` node that only reads or drafts leaves nothing in the run's
  artifact list, whatever it returns.
- The **commit token pattern** the issue-filer uses today is the model:
  a one-run token is minted into a `0600` file in a private scratch
  directory immediately before the commit step, the commit script is run
  with that file's path as an argument, and the token is in no argument and
  no environment variable of the glue process itself (circus
  `infra/hosts/pkgs/issue-filer-glue/default.nix`, the `commit()` function).
  A roustabout-run commit node is expected to receive its write credential
  the same way: by file, minted for that run alone, never by argv or env of
  the node that confers it.

## What moves from circus and what stays

**Moves to roustabout:** the glue's step sequence becomes the interpreter.
circus `infra/hosts/pkgs/issue-filer-glue/default.nix`'s header comment
names the steps, and the edges they take, as: `start` (juggler
`spawn --new-run`, idempotent on `request_id`), `draft` (the issues moxin's
`list_repos` in record mode, then `juggler spawn --brief --wait` runs the
drafter), `freeze` (the traffic file is copied to a read-only frozen file
the moment the drafter returns, and hashed), `store` (the frozen traffic is
written to madder before anything is judged — fail closed), `judge`
(`juggler decide` against the graph's threshold, per question), `commit`
(the token-minted commit script run on the frozen traffic), `sink` (the
graph's failure-sink program, when it is not `"none"`, gets the result
record on stdin), and `resolve` (`juggler resolve` with the result record
as the reason, retried, with a manual-recovery path if every attempt
fails). roustabout's job is to be the general engine this one bash script
is today: the graph document says which steps exist and which edges they
take: this script, order of steps included, no longer needs to be read to
know what ran.

**Stays in circus:** the drafter's brief template
(`infra/hosts/krone/issue-filer/issue-drafter.brief.toml`), the commit
script (`infra/hosts/pkgs/issue-filer-commit`), the `issues` moxin
(`infra/hosts/pkgs/pebble-graph-tools`), the hooks and runner that start a
request and the systemd unit it runs under, and the models file and keys
(Jev's model, the troupe mint credentials). These are the graph's content,
not the engine that runs it; circus remains the first consumer of
roustabout, not its owner.

## Dependencies and status

- **juggler** (presently a clown verb, clown FDR 0019): roustabout depends
  on it as a library or a binary; which is undecided (see below). Its
  extraction into its own repository is sequenced separately, by clown
  (clown#245 and its successors).
- **ringmaster**, at ProtocolVersion 3: clown#256, spinclass#367 and
  troupe#52 are the consumers' rebuilds against it currently in flight.
- **moxy FDR 0013** (tool narrowing) and moxy#451 (tool kinds) are the basis
  of the per-node permission rules above.

**Status:** nothing is built beyond this scaffold — a Go module on
purse-first's go-mcp `command` framework with one verb, `version`. No graph
document parser, no node runner, no run ledger, no signing.

**Open questions** (FDR-0043, verbatim, minus "language and framework": this
scaffold settles that one as Go on purse-first's go-mcp):

- The exact split between a roustabout run and a juggler run: whether a
  graph's root principal is minted by roustabout (one root per graph run,
  nodes as children) or whether each agent node is its own juggler run
  under a roustabout-owned parent.
- Whether a script node runs as a ringmaster job (observable, cancellable)
  or inline; FDR-0041's commit script would be the first case.
- Where the run-level ledger lives: ringmaster's journal on the run job, as
  FDR-0041 does today, or roustabout's own store.
