# The juggler adapter contract file and its recorded output

Status: proposal, agreed in outline with juggler/prime-banyan by message
(2026-11-12), operator ruling relayed: a contract file consumed by a test in
juggler's repo, plus recorded real output as adapter test data; no lock change;
juggler becomes a flake input later. Nothing is built. This note follows
`docs/proposals-brief-check-and-juggler-contract.md` (options B and C there).
What juggler's lane can and cannot do is juggler's own account, given by message
from reading its lane's code and fakes; it is not verified here.

## The chain

adapter code → the contract file (a roustabout test keeps them equal) → juggler's
vendored copy (byte-for-byte, held by a digest) → the real juggler binary on its
fake lane (juggler's test). Recorded output is the other direction: the real
binary's bytes → roustabout's adapter tests.

## 1. The contract file

`internal/charlie/adapters/testdata/juggler-adapter.contract.json` in roustabout
(built; the first draft of this note said `contract/juggler-adapter.json`), schema
1, JSON (juggler's tests are bats and read every assertion with jq, so no new
tool). It lives under `testdata/` because godyn's test lane runs a package's tests
with the package directory as cwd, so `testdata/` resolves, while `go:embed` is not
supported there (no embed configuration) and files outside the package are not
available. Juggler's vendored copy keeps the name `contract/roustabout-adapter.json`.

Built as of the slice-1 commit: the file with the four verbs, and the test
`contract_test.go` that runs the adapter on each verb with fixed placeholder
values (with and without each optional group) and fails if the argv differs from
the file's, if a listed `env` name is not set, if an `env_absent` name is set, if
`decide`'s stdin differs from the file's payload, or if the file declares a verb
the adapter does not call or an undeclared placeholder. The test fails on drift
(checked by changing `--min-confidence` in the file and seeing it fail with the
adapter's and the file's argv side by side). It does not yet check the `stdout` and
`exit` entries against the adapter; those are juggler's to run against the real
binary, and the recorded testdata (slice 2) is what the adapter's parsing is
tested against.

```
{ "schema": 1, "consumer": "roustabout",
  "placeholders": { "run_key": "string", "input_file": "file", "room_domain": "string",
                    "operator_jid": "string", "issuer": "string", ... },
  "adapter_exits": [124, 127, "128+n"],
  "verbs": [ {
    "name": "spawn-new-run",
    "argv": ["spawn","--new-run","--run-key","{run_key}","--input","{input_file}", ...],
    "argv_optional": [["--issuer","{issuer}"]],
    "env": ["TROUPE_MINT_USER","TROUPE_MINT_PASSWORD_FILE"],
    "stdin": null,
    "stdout": [ {"key":"run_job","type":"string","present_at_exit":[0]},
                {"key":"pending","type":"bool","present_at_exit":[1]} ],
    "exit": [0, 1]
  } ] }
```

Rules (juggler's corrections incorporated):

- **Verbs**: `spawn-new-run`, `spawn-wait`, `decide`, `resolve`; the argv exactly as
  the adapter builds it, with `{name}` placeholders.
- **`placeholders`** declares every name an argv may use, with a type (`string`,
  `file`, `duration`). A `{name}` not declared fails juggler's test, so a new
  placeholder cannot slip in. File placeholders are made by the runner from fixed
  fixtures; the adapter needs no particular content of them.
- **`argv_optional`**: groups of arguments that may be added; each is tested both
  present and absent.
- **`stdout` keys**: only keys roustabout reads are listed; any other key is
  ignored. `present_at_exit` lists the exit codes at which the key must be present;
  `absent_at_exit` the ones at which it must be absent. A key with neither is
  "read if present, no claim". This replaces a single presence word: for `decide`,
  `answers` is present at exit 0, 3 and 4 and absent at 2, and `reason` is present
  at 2 and 4 (juggler's reading of its code).
- **`key_present: true`** on a key (`result` in `spawn-wait`) means a JSON `null`
  is a value and absence is absence; the runner checks the key, not truthiness.
- **`exit`**: every listed code needs a scenario that produces it, or juggler's test
  fails with "no scenario for <verb> exit N". A code juggler produces that is not
  listed is not an error. roustabout claims: `spawn-wait` 0, 2, 3, 4, 5 and 1 (a
  usage error); `spawn-new-run` 0 and 1 (any non-zero is "not started"); `decide`
  0 (any non-zero is a failure); `resolve` 0 and 1 (non-zero is retried).
- **`adapter_exits`** are roustabout's own (its time limit, a child that could not
  start, a signal); the runner ignores them.
- **`env`**: juggler enforces only the minter variable for `resolve` (exit 1 before
  any side effect); the others are decided by the real troupe binary, which its fake
  ignores. So the `env` column is verified for `resolve` only and is documentation
  elsewhere; the runner says so in its output.
- **`stdin`**: `decide` takes its payload on stdin. The file names a fixture
  (`{"state":{},"questions":{"request":{}}}`); juggler may use its own questions
  JSON instead.

On our side a test renders each verb's argv with fixed placeholder values from the
adapter and compares it to the file, so the file cannot drift from the code.

## 2. Recorded output (adapter test data)

`internal/charlie/adapters/testdata/juggler/<case>/{args,stdout,rc,stderr}`, plus
at the top: `COMMIT` (the juggler commit it came from), `NORMALISE` (the rules
applied, so a reader sees exactly what was changed) and `README` (below). Each case
has a hand-written `expect.json` in roustabout (what the adapter must parse out of
those bytes); juggler writes none of it.

Cases (13), all producible by the real binary on juggler's no-model fake lane:
new-run ok; existing unresolved; existing resolved; pending-failure (exit 1,
`pending:true`); spawn-wait succeeded; with `result`; with `result:null`; with
`result_error`; `cannot_complete`; a failed state carrying a `result`; a dead
agent with no ledger keys; `decide` ok; `resolve` ok; `resolve` already_resolved.

**What these recordings are, said plainly (juggler's account).** In the lane,
systemd never runs the agent's unit: for every `spawn-wait` case the journal
terminal and the ledger are *seeded by the test*. The stdout bytes are real
juggler output (the wait record assembled from a ledger), but the ledger content,
including `result`, is hand-seeded, not produced by a real loop; `decide`'s
`answers` come from the fake endpoint. They are recordings of juggler's output
shape, not end-to-end recordings, and the testdata README says so.

**Normalisation (shape-preserving, juggler's proposal, agreed).** Rather than raw
bytes, which would differ on every capture: UUIDs → `00000000-0000-4000-8000-00000000000N`
(N by first appearance); job ids → `juggler-agent-0000000N`; stanza ids →
`chat-0000000N`; the state directory → `/state`; the fake directory → `/fake`.
Random per run: child principal and jid, job ids, stanza ids; deterministic: the
root principal (a UUIDv5 of the run key); no timestamps appear. The adapters parse
some values structurally (a JID must contain `@`; a stanza id must be non-empty),
so normalisation must keep those shapes. **Open, juggler's to verify:** that a case
run twice and normalised gives identical bytes; until it has, this is a plan.

## 3. Who does what, on a go

- **Juggler** (its repo): the vendored copy `contract/roustabout-adapter.json` with
  a sidecar `contract/roustabout-adapter.source` (`roustabout <commit> sha256:<hex>`)
  and a bats test that recomputes the digest; a sync recipe; a data-driven bats
  runner next to its nine existing `contract:` tests; and the capture, a flake
  package that writes the `<case>/{args,stdout,rc,stderr}` layout and `NORMALISE`
  (a flake.nix change, not a lock change). Drift between the two copies is possible
  until the flake input lands and is found only when someone syncs.
- **roustabout** (this repo): the contract file and the test that keeps it equal to
  the adapters; the testdata directory layout, the `expect.json` per case and the
  adapter tests over them; and `just codemod-juggler-testdata DIR COMMIT`, which
  copies a capture in, writes `COMMIT`, and fails if a case directory is missing or
  has an extra file. roustabout is the only writer of its testdata; juggler is the
  only author of the capture.

Order: the contract file and its equality test first (it unblocks juggler's
runner); then the testdata layout with the cases juggler can capture; then the
recipe. Each is a docs-and-tests change in this repo; none touches the issue-filer
path or the 75 conformance pins.

## Questions

1. Is the format above (placeholders, `present_at_exit`, `key_present`, `exit`,
   `adapter_exits`) agreed as the schema-1 contract? (Juggler's corrections are in;
   its confirmation that its runner can read this exact shape is not yet.)
2. Who confirms byte-stability of a normalised capture (juggler, by running a case
   twice) before roustabout commits testdata, and what is the fallback if it is not
   stable? Recommendation: do not commit testdata until it is.
3. Should the testdata README state "seeded, not end-to-end" as above?
   Recommendation: yes.
