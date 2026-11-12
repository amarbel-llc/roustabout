# Two proposals: a load-time check for `[result]` briefs, and a real-binary check of the juggler adapters

Status: proposals, for the coordinator and the operator; nothing built, nothing
decided. Each is an option set with a recommendation and numbered questions. Facts
below are from the code as of roustabout 2fdf252 and from what juggler sent
(juggler 92abf8d, ef32dee, 18f8418, and its contract comparison); where a fact is
juggler's or unverified, it says so.

## 1. A load-time check that attestation briefs opt in to `[result]`

**The problem.** An attestation agent's findings are read from juggler's
structured `result`, which a run carries only if the agent's brief has a
`[result]` table with `format = "json"`. roustabout does not read the brief, so a
brief without it fails at run time: "the run carries no result". The failure is
safe (it fails closed) but late: after a run is started, rooms are made and
earlier agents have run.

**Where it would live.** `graph.LoadAttest` (internal/0/graph/attest.go), at the
point it already reads and digest-checks `briefs/<digest>.toml` for each agent
node. So `roustabout run` and `roustabout check` both refuse the document before
anything starts, and circus can lint a graph with `check` before it deploys. The
issue-filer path is not touched (its drafter brief has no `[result]` and does not
need one).

**The error text.**
`graph document is not the shape roustabout runs: briefs/<digest>.toml (the agent
"<label>"): the brief has no [result] table with format = "json"; without it
juggler returns no structured result and the run cannot read the agent's findings`

**How the check reads a brief.** The real issue-drafter brief
(conformance/fixtures/issue-drafter.brief.toml) shows what briefs look like: tables
(`[evaluator]`, `[env]`, `[limits]`) and long multi-line literal strings
(`'''...'''`) holding prose. Roustabout's `go.nix` has no TOML library today.
Options:

- **A. A strict line scan, no new dependency.** Accept exactly one form: a line
  `[result]` outside any multi-line string, followed (before the next table header)
  by `format = "json"`. It must skip comments and track `'''` and `"""` blocks, so
  a `[result]` line inside a prompt is not mistaken for a table. Other TOML
  spellings of the same thing (`result.format = "json"`, `result = { format =
  "json" }`) are refused with the error above, which names the required form.
  Cost: roughly sixty lines and its tests; a mini-lexer for strings that must stay
  correct as briefs grow. Risk: a false refusal on a valid brief in another
  spelling (the message says how to fix it), and in principle a false accept if
  the scan mishandles a string form it does not model.
- **B. A real TOML parser.** Add the owner's TOML library (`tommy`, a sibling repo
  described as a TOML library for Go) as a flake input and import it. Exact for
  every spelling. Cost: a new flake input in `go.nix` and a `flake.lock` change,
  which is a lock bump and is not mine to make; it also adds a dependency to the
  loader's trust boundary. I have not read tommy's API.
- **C. Ask juggler to validate the brief** (a `juggler` verb, or a
  `spawn --check-brief`). It would be exact and keep brief semantics in juggler.
  I know of no such verb; juggler's FDR 0023 notes that a dialect which requires
  the opt-in would catch it at pin time, which is the same idea from the other
  side.
- **D. Leave it.** The failure is safe and the coordinator has said it is fine
  until an attestation graph exists.

**Recommendation.** D until the first attestation graph is written; then **B** if
the operator allows the lock change (exact, no scanner to maintain), else **A**.
Prefer **C** or juggler's dialect route if either lands, because it moves the
knowledge of what a brief must contain out of roustabout, which is where the
question "does this brief opt in" belongs.

**Test impact.** `graphtest.AttestBrief` gains the `[result]` table; a loader test
asserts the refusal and the text; an attestation test fixture without the table
asserts a load error.

**Questions.**
1. Do it now (A or B), or wait for the first attestation graph (D)? Recommend wait.
2. If B: may the lock change for a TOML library be made, and which library?
3. Should the check be part of `LoadAttest` (so `run` refuses too), or only of
   `roustabout check`? Recommend `LoadAttest`: a check that `run` can bypass is
   not a check.

## 2. Running the adapters against the real standalone juggler

**The problem.** The adapters (internal/charlie/adapters/juggler.go) are tested
against a helper process that prints what the tests tell it to, and the 75
conformance scenarios run on a bash stand-in. Neither executes a juggler, so a
contract change in juggler (a renamed key, an exit code) is invisible until a
live run. Juggler compared the standalone build (18f8418) with my contract list
by reading its source and found two points to clear up, both resolved; that
comparison is a snapshot, not a test.

**What I know of the other side.** Juggler has a no-model fake lane and fixtures
(its words: c48de5e). I have not read them, and I do not know how a caller points
a run at the fake lane, what it needs (a state directory, credentials, XMPP) or
how long it takes.

**Options.**

- **A. Real binary in roustabout's gate.** Add juggler as a flake input, build
  the standalone binary, and run the adapter tests (and perhaps a few
  scenarios) against it on its fake lane in a sandbox. Catches drift in the
  adapter's own test, in the repo that owns the adapter. Cost: a new flake input
  and `flake.lock` change (not mine to make); every roustabout merge builds
  juggler (build time unknown to me; it would have to be measured, I give no
  number); and the fake lane must be exported by juggler as a package. It couples
  the two repos' release cadence: a juggler change can fail a roustabout merge.
- **B. A contract file consumed by juggler's tests.** Roustabout publishes its
  contract (verbs, argv templates, env, the stdout keys it reads, the exit codes
  it maps) as one data file in this repo; juggler's repo reads it (through a flake
  input on roustabout, or a pinned copy named by digest) and its own test runs the
  real binary on its fake lane to check each claim. Juggler already offered to
  propose a contract test in its repo. Cost: one file here, kept in step with the
  adapters by a test that reads it; the real-binary run is in juggler, which owns
  the binary and the lane. Weakness: it checks juggler against what roustabout
  *says* it reads, not roustabout's adapter against juggler's bytes, unless the
  adapter tests are also driven from the same file.
- **C. Recorded real output as test data.** Capture the real standalone's stdout
  and exit codes for each verb on its fake lane once (done in juggler, which owns
  it), store them in `internal/charlie/adapters/testdata` with the juggler commit
  named, and have the adapter tests parse those bytes. A recipe refreshes them.
  Cost: small. Weakness: drift is found only when someone refreshes.

**Recommendation.** **B and C together, A not now.** C makes the adapters tested
on real bytes at the cost of a data directory; B puts the real-binary run where the
knowledge of the fake lane lives, which is juggler. A is the strongest check but
makes every roustabout merge depend on building another repo, adds a lock change,
and I cannot say what it costs.

**What I would build, in order, after a go.** (1) The contract file and the test
that keeps it equal to the adapters. (2) Testdata from juggler's capture and the
adapter tests over it. Both are docs-and-test changes in this repo; neither
touches the issue-filer path or the 75 pins.

**Questions.**
4. B and C, or A? Recommend B and C.
5. Who captures the real output for C (juggler/prime-banyan, since it owns the
   binary and lane), and where is it checked in (juggler's repo, roustabout's
   testdata, or both with a digest)?
6. May a `flake.lock` change for either A or the TOML library be made, and by
   whom? Hard limit for this session: I make no lock bump.
