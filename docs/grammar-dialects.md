# One grammar, two dialects: turns (juggler) and graphs (roustabout)

Status: draft proposal, roustabout's half of a joint note. No code, nothing
decided: every choice below is an option with a recommendation and goes to the
operator as a numbered question. Origin: the operator's framing, "a grammar with
two dialects or applications would make sense here ... juggler for turns,
roustabout for agents/tools/graphs" (relayed by the coordinator, 2026-11-12).

The turns half is juggler's: juggler/prime-banyan writes it (proposed as juggler
FDR 0023) in its own repo. This note does not describe turns; where it needs
something from them it says so as a question. Everything here about roustabout
is anchored on the code at the time of writing; everything about juggler is
labelled as unverified.

## Shared core (duplicated verbatim)

Source of truth: this file, `docs/grammar-dialects.md` in the roustabout repo,
the block between the two marker lines below. Proposed: it is where the
envelope is implemented today, and the only place. The juggler note (proposed:
FDR 0023) copies the block byte for byte; a copy that differs is a bug in the
copy, fixed by re-copying. Operator ruling relayed 2026-11-12: the grammar is a
pinned schema for authored documents, with two dialects, juggler for turns and
roustabout for agents, tools and graphs.

<!-- shared-core:begin -->

A **grammar** is a schema for authored documents. A **dialect** is one
application of it: it fixes what the body of a grammar document and of a
document checked against it may contain. Two dialects are proposed: `turns`
(juggler) and `graph` (roustabout).

What every dialect shares:

1. **A document is checked against a pinned grammar.** The host supplies the
   grammar document and pins it by its digest, `sha256:<hex>` of its bytes. The
   document being checked never carries the grammar that validates it. The pin
   check is `digest(bytes) == pinned` and nothing else.
2. **The envelope.** Every grammar document has `grammar` (a name), `version`
   (an integer the checker knows or refuses) and, once a second dialect
   exists, `dialect` (a name; absent means `graph`, so documents written before
   the field keep their digests). A checker refuses a version or dialect it does
   not know, by name, before looking at the body.
3. **Strict decoding.** An unknown field is refused.
4. **The right's name.** Running anything under a grammar needs an ambient right
   named `<interpreter>:grammar:<digest>`, where `<digest>` is the pin. The name
   is derived from the pin and never supplied by a document. Checking the right
   against a grant chain waits for grants to exist; until then the pin is the
   only authority.

What a dialect supplies and the other does not read: the grammar body, the
checks on a document against it, and the interpreter that runs it.

<!-- shared-core:end -->

## What exists in roustabout today

- **A grammar document** (`internal/0/graph/grammar.go`): `grammar` (a name),
  `version` (1), `kinds`, `edges` (`[from kind, outcome, to kind]`),
  `constraints` (entry kind, one fail target, per-kind caps, gates), `result`
  (the closed list of result steps, some optional) and, optional, `request` (the
  request type a run takes, `issue-filer/1` when absent). Parsed strictly:
  unknown fields and unknown versions are refused.
- **Pin by digest** (`graph.Expect`): the host supplies the grammar document and
  its sha256; the document that is checked against it (the "sentence") never
  carries the grammar. The check is `Digest(bytes) == pinned`, nothing else.
- **A kind table in Go** (`kindTable`): what a node kind is, its outcomes, the
  outcomes it must have an edge for, and whether it may be conferred a write
  tool. Only a roustabout change adds a kind (FDR-0043 decision 21).
- **Request-type selection**: a pinned grammar names its request type, and
  `roustabout run` and `roustabout check` pick the loader and runner from it
  (`PinnedRequestType`).
- **A right named from the digest**: `graph.GrammarRight(digest)` returns
  `roustabout:grammar:<digest>`. It is reported by `check` and not yet checked
  against anything: no grant chain exists to read.

## (a) What is shared, and where it lives

What a turns dialect could reuse is the envelope, not the body. The body of
roustabout's grammar is about the shape of a directed graph of nodes; I have no
basis to say a turns dialect's body (as I understand it from juggler's message:
a tagged union of turn bodies) has anything in common with it, and the two
should not be forced into one vocabulary.

The shared core is the block above; the four items below are the same content
with the reasoning. The candidate pieces are:

1. **Digest and pin**: `Digest(bytes)` as `sha256:<hex>`, and "these bytes are
   the ones the host pinned".
2. **The envelope**: `grammar` (name), `version`, and a dialect name, parsed
   strictly, so a document of another dialect is refused by name before its
   body is looked at.
3. **The right's name**: `<interpreter>:grammar:<digest>`.
4. **Strict decoding**: unknown fields refused.

Options:

- **A. Nothing shared.** Each repo keeps its own copy. Cheapest now; the pin and
  the right then drift in spelling.
- **B. A shared spec, no shared code.** One short document defines the
  envelope, the pin and the right's name; each repo implements it. No new
  module or import edge.
- **C. A shared library.** A small Go module with `Digest`, `Pin(doc,
  pinned) error`, `Right(interpreter, digest)`, and a strict `Decode` of the
  envelope; each dialect supplies its body type and checks.

Recommendation: **B now, C when a second dialect has a parser**. Today one
repo parses a grammar; a library extracted from one consumer would be shaped by
that consumer, and the cost of getting the API wrong lands on both repos.

Where a library would live if built (option C): not in roustabout. roustabout
is the layer above juggler and ringmaster (circus FDR 0043), so juggler
importing a roustabout package would invert the layering, and everything
under roustabout's `internal/` cannot be imported at all. It would be a small
module both import: a new repo, or an existing one below both. That is the
operator's to choose.

**What roustabout would change to consume it (option C).** `Digest`,
`errNotPinned`, and the pin check in `pinnedGrammar` move out and are imported;
the grammar document gains an optional `dialect` field that defaults to the
graph dialect when absent, as `request` does, so every existing grammar
document keeps its digest. No node, edge, record or summary changes. The
proof that the live issue-filer graph is unchanged is the existing one: the 75
conformance scenarios stay at 0 differing, the issue-filer graph's digest does
not move, and the grammar documents already pinned do not move either, because
the new field is left out when it is the default.

## (b) The boundary

Roustabout orders runs: which agents and tools run, in what order, on which
outcome's edge, what is recorded for each (the run-level ledger), and what the
run resolves with (the record builder). Juggler orders turns inside one run: who
speaks, tool calls and results, how a run ends.

In roustabout's code that boundary is one place per node kind: an `agent` node
is one `juggler spawn --wait` and its answer (`state`, `message`,
`cannot_complete`); `decide` is one `juggler decide`; `resolve` closes the run.
Roustabout reads nothing inside a run's turns.

It blurs in two places I can see:

- **Reading an agent's result.** Attestation agents end with one JSON object
  (findings), and roustabout reads it out of the `message` string of spawn's
  output. That is roustabout parsing agent text because the interface gives it
  a string. A structured terminal body would make it a typed value.
- **`decide`.** A single juggler call over several questions; roustabout
  applies the thresholds per question (decision 20). It is one brick on
  roustabout's side and a call on juggler's.

Options: (1) keep the CLI-and-JSON contract as today, versioned; (2) a library
call between them. Recommendation: **(1)**. It keeps the layers independent and
is what exists; juggler's verbs are still moving (the adapter says so), so a
compile-time contract would be the wrong place to freeze them.

## (c) The permission surface

Today only one authority exists: the host pins a grammar by digest, and
roustabout refuses a graph that does not satisfy it. "Who may pin" is then
"whoever configures the host", which is not a permission model.

The design already decided elsewhere (the attestation epic, FDR 0042) is that a
grammar is an ambient right named by its digest. Extending that to a second
dialect:

- **Pinning** a dialect document is host configuration. **Running** a graph or
  a loop under a dialect needs the right in the run's grant chain.
- **Naming**: `<interpreter>:grammar:<digest>`, so `roustabout:grammar:<d>` and
  a juggler counterpart, by the same rule. Digest-addressed rights cannot
  collide across dialects, because the digest covers the document.
- **Blocked**: checking any of this against a grant chain waits for grants to
  exist (spinclass FDR 0032, moxy FDR 0013). Nothing in this note can be
  enforced before then, and nothing here should be read as if it were. I do not
  know what "ajour" refers to in the coordinator's request, so I have not
  designed around it.
- **Circus decisions**: who may pin which grammar is FDR-0043 decision 13 (with
  the two FDRs above), and principals are decision 14. Both are circus's and
  the operator's, not this repo's.

## (d) How an agent node would reference a loop dialect

An `agent` node references its brief by digest: `brief: sha256:...` and a file
`briefs/<digest>.toml`, verified at load. A loop dialect document can be
referenced the same way without roustabout learning anything about turns.

Options:

- **1. A bare digest**: `loop: sha256:...`, the file in `loops/<digest>.<ext>`.
  Opaque to roustabout; juggler is handed the digest or the file.
- **2. A dialect name and a digest**: `loop: {dialect: "turns/1", digest:
  "sha256:..."}`. Still opaque, but roustabout can refuse a node whose dialect
  the pinned grammar does not allow (a grammar constraint such as `loops:
  ["turns/1"]`) before anything starts, and a host can pin which dialects
  agents may use without roustabout parsing one.

Both are covered by the node id, so a changed loop is a changed graph digest.
Recommendation: **option 2**. Open question for juggler: does `juggler spawn`
have, or would it take, an argument that takes a loop dialect document or its
digest? I do not know of one.

Roustabout would also not interpret the loop's outcome beyond what it does
today: the run ended with a state, a final body and a ledger.

## (e) Risks

- **A premature shared library** shaped by one consumer (the reason for
  recommending B first).
- **Calling two different things one grammar.** A workflow grammar constrains
  the shape of a graph; a turns grammar, as I understand it, constrains a
  sequence of turn bodies. Sharing the envelope is plausible; sharing the body
  language would be a forced fit, and the shared note should say "dialect" and
  mean it.
- **Adding `dialect` to roustabout's grammar documents** changes what a
  document means; the defaulting rule (absent means the graph dialect) is what
  keeps digests, and a test that a pre-existing grammar's digest is unchanged is
  the guard.
- **Coupling through the ledger.** If roustabout's ledger cites a run's turn
  log, juggler's identifier for it becomes a long-lived interface.
- **Reading agent text.** Until the result is structured, roustabout's findings
  contract depends on the exact string juggler returns as `message`, which has
  not been observed against a live juggler.
- **Permission claims running ahead of enforcement** (section c).
- **Verified only on one side.** All of the juggler side here is from the
  message juggler/prime-banyan sent, not from reading juggler.

## Questions for the operator

1. **Shared code now or a shared spec first?** Recommendation: a spec first
   (option B); a library when a second dialect has a parser.
2. **If a library is built, where does it live?** It must sit below both, so
   not in roustabout. Recommendation: a new small module; the operator names the
   repo.
3. **Add `dialect` to the grammar envelope, defaulting to the graph dialect?**
   Recommendation: yes, with the digest-stability test.
4. **Right naming across dialects**: `<interpreter>:grammar:<digest>`.
   Recommendation: yes.
5. **An agent node's loop reference** as `{dialect, digest}` with a grammar
   constraint on allowed dialects. Recommendation: yes (option 2).
6. **Replace the findings-in-a-message convention with a structured terminal
   body** once juggler offers one. Recommendation: yes; juggler's to say when.

## What this depends on

From juggler (prime-banyan): the shape of the turns dialect; whether `spawn`
takes a dialect document; a structured terminal body; the identifier for a
run's turn log, if any. From circus and the operator: decisions 13 and 14 of
FDR 0043 (who may pin, principals), and the grants of spinclass FDR 0032 and
moxy FDR 0013, without which section (c) is design only.
