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

## Operator rulings (relayed 2026-11-12)

- **The shared module lives in juggler.** Not a new repo, and not roustabout.
  It is a package juggler exports (not under `internal/`), and roustabout
  imports it; roustabout sits above juggler, so the import points the right
  way. Until it is built, the shared-core block below stays as the contract and
  roustabout's `internal/0/graph` as its source of truth, as that block says;
  once built, the module is the source and both notes defer to it.
- **A structured terminal result is accepted** (juggler FDR 0023, open question
  4): an optional `result` on the terminal body. roustabout's findings then come
  from that value rather than from the `message` string. That is an adapter
  change in roustabout and waits for juggler to build `result`; nothing in
  roustabout changes before then. Plan, for when it lands: the adapter reads the
  structured result; `ParseFindings` takes its value instead of a string; the
  fail-closed rules (an unreadable result fails the agent) are unchanged; the
  `message` fallback is removed, not kept, so there is one path to test.

Questions 2 and 6 below are answered by these; they are left in place with the
rulings noted.

## Shared core (duplicated verbatim)

The operator ruled (relayed 2026-11-12): the grammar is a pinned schema for
authored documents, with two dialects, juggler for turns and roustabout for
agents, tools and graphs; the pinning and loader machinery is shared, and
runtime records are mapped, not validated. The block below is juggler's text
(juggler FDR 0023, `docs/features/0023-turns-dialect-grammar.md`, juggler
37be19a), copied byte for byte as it asked; the copy in juggler's FDR is the
other half of the pair, and a copy that differs is a bug in the copy.

<!-- shared-core:begin -->

> This section is duplicated verbatim in juggler FDR 0023 and in roustabout's
> `docs/grammar-dialects.md`. **Source of truth for the shared core: roustabout's
> `internal/0/graph` (`grammar.go`), because it is built and merged; if the
> shared module of §3 option A is accepted, the module becomes the source and
> both repos defer to it.** Dialect-specific content is each repo's own.

1. **Pin by digest.** The host supplies the grammar document and pins it by its
   sha256 (`sha256:<hex>`). A document never carries the grammar that
   validates it.
2. **Versioned, closed.** A grammar document has a `grammar` name, a `version`,
   and a closed vocabulary; the loader refuses unknown fields and any version it
   does not know.
3. **Request type.** A grammar may name a request type that selects the
   sentence parser (`issue-filer/1`, `attest/1` in roustabout; `brief/1`,
   `script/1` proposed in juggler); absent means the dialect's default, so a
   grammar written before the field keeps its digest.
4. **Grammar right.** The ambient right a run of a grammar needs is named from
   the pinned digest, `<owner>:grammar:<digest>` (roustabout's is
   `roustabout:grammar:<digest>`), derived by the host and never supplied by a
   document.

Not shared: roustabout's node-kind table, outcomes, edges, result steps and
walker; juggler's turn vocabulary, tool offering, limits and stop conditions.

<!-- shared-core:end -->

(§3 there is juggler's section on the library's home; this note's version of it
is part (a) below. "Option A" there is a new small module below both.)

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

The shared core is the block above. In roustabout's code its four points are:

1. **Digest and pin**: `Digest(bytes)` as `sha256:<hex>`, and "these bytes are
   the ones the host pinned" (`graph.Digest`, `pinnedGrammar`).
2. **Versioned, closed**: `grammar` (name), `version`, unknown fields and
   unknown versions refused (`ParseGrammar`).
3. **Request type**: the grammar's `request` field, `issue-filer/1` by default,
   which selects the loader and runner (`PinnedRequestType`). This note does not
   add a separate `dialect` field: the request type already selects the sentence
   parser, and the dialect follows from the interpreter that owns it.
4. **The right's name**: `<owner>:grammar:<digest>` (`GrammarRight`).

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
module both import: a new repo, or an existing one below both. The operator
ruled it lives in juggler. Juggler's FDR 0023 (§3) reaches the same conclusion and
lists the module's content as `Digest`, `Pin`, a strict versioned `Decode`,
`Right(owner, digest)` and a request-type registry.

**What roustabout would change to consume it (option C).** `Digest`,
`errNotPinned`, the pin check in `pinnedGrammar` and the right's name move out
and are imported; the request types stay a registry roustabout fills in. No
grammar document, node, edge, record or summary changes. The proof that the live
issue-filer graph is unchanged is the existing one: the 75 conformance scenarios
stay at 0 differing, the issue-filer graph's digest does not move, and a test
that a grammar document pinned before the change still has the same digest
guards the grammar files.

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

Juggler's answer (FDR 0023, from its message): `spawn` has no such flag today;
it proposes `--dialect <file> --dialect-digest sha256:...`, optional, with
today's briefs an implicit dialect `brief/1` so nothing changes without it.
That fits option 2 here: the node's `{dialect, digest}` maps to those two
arguments. It is a proposal in juggler, not something roustabout can call.

## (e) Risks

- **A premature shared library** shaped by one consumer (the reason for
  recommending B first).
- **Calling two different things one grammar.** A workflow grammar constrains
  the shape of a graph; a turns grammar, as I understand it, constrains a
  sequence of turn bodies. Sharing the envelope is plausible; sharing the body
  language would be a forced fit, and the shared note should say "dialect" and
  mean it.
- **A new field on grammar documents** (the `request` field, already added, is
  the example) changes what a document means; the defaulting rule (absent means
  the dialect's default) is what keeps digests, and a test that a pre-existing
  grammar's digest is unchanged is
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
   not in roustabout. Recommendation: a new small module. **Ruled: in juggler**
   (see "Operator rulings").
3. **Keep the dialect implied by the request type and the interpreter, with no
   separate `dialect` field in the envelope?** Recommendation: yes; the shared
   core (point 3) already has the request type select the sentence parser, and
   a second selector could disagree with the first.
4. **Right naming across dialects**: `<owner>:grammar:<digest>` (the shared
   core, point 4). Recommendation: yes.
5. **An agent node's loop reference** as `{dialect, digest}` with a grammar
   constraint on allowed dialects. Recommendation: yes (option 2).
6. **Replace the findings-in-a-message convention with a structured terminal
   body** once juggler offers one. Juggler proposes an optional `result` on the
   terminal body, an additive change to the on-disk turn format, and has made it
   an open operator question of its own (FDR 0023, open question 4).
   Recommendation: yes if that is accepted; the decision is juggler's and the
   operator's, and roustabout changes only its adapter. **Ruled: accepted**
   (see "Operator rulings"); roustabout waits for juggler to build it.

## What this depends on

From juggler (prime-banyan): the shape of the turns dialect; whether `spawn`
takes a dialect document; a structured terminal body; the identifier for a
run's turn log, if any. From circus and the operator: decisions 13 and 14 of
FDR 0043 (who may pin, principals), and the grants of spinclass FDR 0032 and
moxy FDR 0013, without which section (c) is design only.
