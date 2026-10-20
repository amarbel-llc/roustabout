# conformance

`just test-conformance` runs 72 scenarios through `roustabout run` and compares
what each did with `expected/<scenario>/<artifact>`. `just codemod` re-pins
(`PIN=1`); read the diff in git before committing one, because a pin that moves
is a change in what `roustabout run` does.

Everything runs against stand-ins: no juggler, madder, troupe or ringmaster,
no model, no network, and nothing outside this repo.

## Layout

- `run.bash` the scenarios and the comparison.
- `default.nix` builds the kit: the stand-ins, the graph documents (six
  variants: default, no-sink, shadow, gate-dup, no-gate, bad-graph) and
  `settings.env`. `nix build .#conformance-kit`.
- `standins/` doubles of the juggler, madder and troupe CLIs, the issues moxin's
  `list_repos`, the commit script and the sink, and of ringmaster. Each logs
  what it was given under `$FAKE_LOG` and behaves as `FAKE_SCENARIO` (and
  `COMMIT`, `SINK`, `TROUPE`) say. They pin the flags roustabout passes.
- `fixtures/` the drafter brief, the Jev question set (v6) and the graph builder
  the issue-filer graph is made from.
- `expected/` the pins: the exit status, the summary and everything the
  stand-ins logged, per scenario. Store paths, node ids and the graph digest
  are normalised, so a pin does not move when the stand-ins are rebuilt on
  another nixpkgs. `expected/defaults.txt` pins roustabout's flag defaults.

## Where the copies come from

`standins/fake-*.bash` (except `fake-ringmaster.bash`, which is this repo's),
`fixtures/` and the variants in `default.nix` are frozen copies of circus's
`infra/hosts` at `628300926d66` (`pkgs/issue-filer-glue/test`,
`pkgs/issue-filer-graph`, `krone/issue-filer/issue-drafter.brief.toml`,
`krone/pebble-graph/approve-question-split-v6.json`). Checked once when this
was made: the default, gate-dup, shadow and no-sink graphs built from them equal
circus's glue-test graphs once node ids and store paths are normalised, and the
brief and questions are byte-identical. They do not follow circus: when circus
changes the issue-filer graph or its stand-ins, refresh the copies on purpose,
re-pin, and read the diff.

## Where the pins come from

They were first pinned from a run that agreed with circus's bash glue on every
scenario (the harness at `a6a1522`, which ran `roustabout run` and the glue on
the same input and diffed them). Re-run from git history against the new kit,
the pins equal that harness's roustabout-side artifacts on all 72 scenarios,
with sha256 values loosened. A pin is a record of what the glue accepted, not a
claim the glue was right. The differences the old harness accepted, which the
pins carry:

- `summary.json:ledger`: roustabout adds a per-node ledger (FDR-0043 decision
  1); the glue has none.
- `m2.bad-graph`, `gt3.no-gate-graph`: both refuse with outcome `refused` and
  exit 2; the `reason` text names the shape each implementation runs. Not
  journaled.
- `s5.term-during-madder.*`: on a signal during `madder write` the glue waits
  for the call to return and roustabout cancels it at once. Same exit 143, same
  interrupted summary, same resolve.
- `s7.term-during-commit.*`: the glue waits for the commit and records the
  issues it printed; roustabout cancels it, so the interrupted record lists no
  issues. A real commit is stopped by the unit's cgroup kill in both, so what it
  printed is lost in both.

## Not covered

A live juggler, madder, troupe or ringmaster; real `ringmaster start/done`
semantics; a real systemd cgroup kill; what `juggler decide --question` does
across several questions. A scriptable agent in clown would let a real juggler
run these scenarios (linenisgreat/clown#270).
