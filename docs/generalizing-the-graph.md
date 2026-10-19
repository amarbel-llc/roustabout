# Generalizing the graph loader

Status: design view, not implemented. Written in answer to the operator's
direction relayed by circus (coco): roustabout hard-codes no graphs; it is the
substrate on which graphs are defined, run and kept with integrity.

## Where the code stands

The loader (`internal/graph`) accepts exactly the issue-filer topology: four
nodes labelled draft, judge, commit and sink, and five fixed edges. The
interpreter (`internal/run`) is that topology written as a function: start,
draft, freeze, store, judge, commit, sink, wake, resolve, in that order. The
graph document does not drive the walk; it is checked against it.

The question names were the first piece of fixed content to come out: the
judge's questions are now the keys of `questions.json`, the judge's modes must
cover exactly those keys, and the gating question handed to juggler is the
first of them (`Graph.Questions`). The topology is still fixed.

## What generalizes, in order

1. **Structure checks that name no topology.** Node ids are digests of the node
   documents; the graph digest covers the document; edges are
   `[from, condition, to]` over ids with conditions `ok` and `fail`; every edge
   endpoint is a node; every non-terminal node has an edge for each outcome it
   can produce; every node is reachable from the entry; and a cycle is refused
   until a bound is part of the document. None of this mentions draft, judge,
   commit or sink.
2. **An entry, named by the document.** The unique node with no inbound edge,
   or an explicit `entry` field. Labels become display names only.
3. **A walker.** Run the current node by kind, take the edge its outcome
   names, record a ledger entry, stop at a node with no outgoing edge for the
   outcome. The per-kind executors already exist as the ports (juggler
   spawn/decide, script-as-ringmaster-job).
4. **The issue-filer's hard-wired steps become properties of kinds or of a
   result contract.** Freeze-then-store of the traffic file after an agent
   node, the exit wake, and `resolve` are not graph topology; they are the
   issue-filer's result contract. They should be declared by the document (a
   `result` section, or agent-node options) and executed by roustabout, so a
   second graph can declare a different contract. This is the largest piece
   and the one I have not designed in code: `run.go` entangles the walk with
   these steps, and untangling it is a refactor with its own test plan.
5. **Host expectations stay a separate seam.** `graph.Expect` (the moxin and
   commit program the host build supplies) is the right shape: the host
   states what it expects, the document does not hard-code it.

## What stays roustabout's, and what is the document's

Roustabout's, regardless of graph: digest verification of the document and
every file it names; node-kind semantics; the run key and idempotency; the run
ledger; ordering guarantees that fail closed (frozen and stored before
judged; nothing committed that was not judged); credential hygiene (the commit
token minted into a `0600` file and passed by path, scrubbed environment
for children); write tools held pass-only and conferred on the one commit
node; time limits and signal handling; and, once spinclass FDR 0032 gives it a
key, signing the run record, per-node narrowing (moxy FDR 0013) and grants.
Those last three are not started and are other sessions' to settle.

The document's: topology; each node's parameters (brief, moxin, questions,
thresholds, modes, programs); the runtime pins (juggler, moxy, glue store
paths); and the result contract once step 4 exists.

The rule I would hold to: if a value changes what ran, it is in the document
and covered by its digest; if it changes whether a run is trustworthy, it is
roustabout's and the document cannot override it.

## Choosing a question set per request source

Circus owns the sets; the question is how the graph and `roustabout run`
carry the choice. Three options:

- **A. One graph per source.** Voice requests and typed agent requests each
  get their own graph document with their own `questions.json`. Circus picks
  the graph directory by request source before calling `roustabout run
  GRAPH_DIR`. No loader change; the graph digest names exactly which questions
  were asked. Nodes are content-addressed, so the draft, commit and sink node
  files are shared between the graphs and only the judge node and its
  questions file differ.
- **B. One graph, several sets in the judge node,** selected by a `--source`
  flag or a field of the request. The digest covers all sets, so it no longer
  says which was used; the run record would need to name the set, and the
  record's shape is circus's contract.
- **C. A selection node** that routes on a request attribute to one of
  several judge nodes. Most general, but it needs edge conditions beyond
  `ok`/`fail` and belongs with the walker (step 3).

Recommendation: A now. It needs nothing beyond names-from-the-graph, and it
keeps the property that one digest means one exact thing that ran. Consider C
when the walker exists and a second graph needs routing for other reasons.

Consequence to flag to consumers: under A, `failed_checks` carries names from
the graph that ran, so records from different sources use different
vocabularies.

Not verified: how circus selects the graph directory today, and whether the
hook that starts a request knows its source at that point.
