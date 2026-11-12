# Recorded juggler output

What the juggler adapter (`../../juggler.go`) is tested against in
`recorded_test.go`: the bytes the real standalone juggler printed for each case.
`COMMIT` names the juggler commit they were captured from (at the time of
writing, juggler `ba290a04457db6f629ce6371bcc61c6336642ede`, its
`packages.adapter-cases`). If `COMMIT` is absent, `TestRecordedJugglerOutput`
skips, visibly.

**What is not recorded.** Juggler's capture has no case for `spawn --wait` exit 5
(still running) or for aborted agents, and none for the non-zero exits of `decide`
and `resolve`. The adapter's handling of those exits is covered by its own unit
tests and by juggler's lane (which checks every exit the contract lists), not by a
recording here.

## What these are, and are not

The recordings are **real juggler output over seeded state**, not end-to-end runs.
They come from juggler's no-model test lane (juggler's account, not checked here):
systemd never runs an agent's unit there, so for every `spawn --wait` case the
journal terminal and the ledger are **seeded by juggler's test**. The stdout bytes
are real juggler output (the wait record assembled from a ledger), but the ledger
content, including a `result`, is hand-seeded, not produced by a real agent loop,
and `decide`'s `answers` come from a fake decisions endpoint. They record the
*shape* of juggler's output, which is what the adapter parses.

## Layout

- `COMMIT`: the juggler commit the recordings were captured from. Written by the
  recipe; the test fails if it is empty.
- `NORMALISE`: the rules juggler applied so a recapture does not differ (UUIDs,
  job ids, stanza ids renumbered by first appearance; world paths replaced), so a
  reader sees exactly what was changed. Written by juggler's capture.
- `<case>/{args,stdout,rc,stderr}`: one directory per case.
- `../juggler-expect.json`, written by hand in this repo: for each case the verb
  whose adapter call parses it and what the adapter must read out of it.
  `recorded_test.go` requires this file and the case directories to name the same
  cases.

## Refreshing

Juggler writes the capture (a flake package in its repo). To take one in:

    just load-juggler-testdata DIR COMMIT

where DIR is the capture's directory and COMMIT the juggler commit it came from.
The recipe replaces the case directories and `NORMALISE`, writes `COMMIT`, and
fails if a case lacks one of the four files or has another file. It never touches
this README or `../juggler-expect.json`; read the diff in git, and update the
expect file by hand when a case's meaning changes.

The contract file `../juggler-adapter.contract.json` is the other half: it is what
juggler's test reads (a vendored copy) to check its binary against what this
adapter expects.
