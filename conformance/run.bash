#!/usr/bin/env bash
# Conformance of `roustabout run` against its own pinned expectations.
# usage: run.bash KIT ROUSTABOUT_BIN [SCENARIO-PREFIX...]
#
# KIT is the build of conformance/default.nix (settings.env, graphs/<variant>):
# the stand-ins for juggler, madder, troupe, the issues moxin's list_repos, the
# commit script and the sink, and the graph documents. Nothing is read from
# outside this repo. Each scenario runs `roustabout run` once, with the input,
# environment and stand-in behaviour it names, and what that run did is
# compared with conformance/expected/<scenario>/<artifact>: the exit status,
# the summary on stdout (null keys dropped), and everything the stand-ins logged
# (the calls in order with their argv, the identity and credentials each saw,
# what decide, the sink, the commit, madder and resolve were given, the wake
# message, the files left in the scratch directory). Normalised first: paths
# under TMPDIR, store paths, and the graph's node ids and digest, so a pin does
# not move when the stand-ins are rebuilt on another nixpkgs. Nothing else is.
#
# The expectations were first pinned from a run that agreed with circus's bash
# glue on every scenario, bar the differences in conformance/README.md. They are
# a record of what the glue accepted, not a claim that it was right; changing
# one is a deliberate act (PIN=1, then read the diff in git).
#
# PIN=1 rewrites the expectations of the scenarios that ran instead of
# comparing. Selecting scenarios by name prefix (a, b, sh, ...) runs only
# those; a full run also fails on an expectation no scenario produces.
# CONFORMANCE_OUT keeps the per-run directories there instead of deleting them.
# Prints a table, then each difference; exits with the number of scenarios that
# differ (or are not pinned).
set -uo pipefail
kit="$1"
rou="$2"
shift 2
only=("$@")
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fake_ringmaster="$here/standins/fake-ringmaster.bash"
expected="$here/expected"
# shellcheck disable=SC1091
. "$kit/settings.env"

if [ -n "${CONFORMANCE_OUT:-}" ]; then
  out="$CONFORMANCE_OUT"
  rm -rf "$out"
  mkdir -p "$out"
else
  out="$(mktemp -d)"
  trap 'rm -rf "$out"' EXIT
fi

# ---- one run ----------------------------------------------------------------

roustabout_cmd() { # VARIANT: prints roustabout's argv, one word per line
  printf '%s\n' "$rou" run
  printf '%s\n' --juggler "$JUGGLER" --madder "$MADDER" --troupe "$TROUPE_BIN" --ringmaster "$fake_ringmaster"
  printf '%s\n' --issues-moxin "$ISSUES_MOXIN" --commit-program "$COMMIT_PROGRAM"
  printf '%s\n' --room-domain "$ROOM_DOMAIN" --operator-jid "$OPERATOR_JID" --issuer "$ISSUER"
  printf '%s\n' --wake-user "$WAKE_USER" --wake-pass-file "$WAKE_PASS_FILE"
  printf '%s\n' --judge-timeout "$JUDGE_TIMEOUT" --resolve-retry-pause "$RESOLVE_RETRY_PAUSE"
  printf '%s\n' "$kit/graphs/$1"
}

# The environment every run starts from: it PLANTS the credentials the
# interpreter must not pass on.
child_env() { # DIR KNOBS STATE
  printf '%s\n' env -u CLOWN_SESSION_ID -u TROUPE_XMPP_USER -u TROUPE_XMPP_DOMAIN -u TROUPE_XMPP_PASSWORD_FILE \
    TROUPE_MINT_USER=fake-minter TROUPE_MINT_PASSWORD_FILE=/fake/minter.pass \
    TROUPE_XMPP_PASSWORD=ambient-secret TROUPE_XMPP_RESOURCE=ambient-resource ISSUES_COMMIT_TOKEN=ambient-token \
    "TMPDIR=$1/tmp" "FAKE_LOG=$1/fake" "FAKE_SCENARIO=$2" "COMMIT=${COMMIT:-ok}" "SINK=${SINK:-ok}" "TROUPE=${TROUPE:-ok}" "XDG_STATE_HOME=$3"
}

# prepare NAME KNOBS VARIANT -> sets d, variant, argv (array)
prepare() {
  local name="$1" knobs="$2" state
  variant="$3"
  d="$out/$name"
  rm -rf "$d"
  mkdir -p "$d/fake" "$d/tmp"
  : > "$d/fake/calls.log"
  : > "$d/fake/env.log"
  state="${STATE_ROOT:-$d/state}"
  mkdir -p "$state"
  mapfile -t argv < <(child_env "$d" "$knobs" "$state")
  local w
  while IFS= read -r w; do argv+=("$w"); done < <(roustabout_cmd "$variant")
}

finish() { # after the run: $d, $rc
  printf '%s' "$rc" > "$d/fake/rc"
  cp "$d/stdout" "$d/fake/summary.json"
  (cd "$d/tmp" && ls -A) > "$d/fake/tmp-left"
}

run_one() { # NAME KNOBS INPUT VARIANT
  prepare "$1" "$2" "$4"
  rc=0
  printf '%s' "$3" | "${argv[@]}" > "$d/stdout" 2> "$d/stderr" || rc=$?
  finish
}

# signalled NAME KNOBS INPUT PATTERN MODE VARIANT: starts the run, sends TERM
# once calls.log shows PATTERN. MODE pid signals the process alone; group
# signals its whole process group, as a unit's KillMode=control-group does.
signalled() {
  prepare "$1" "$2" "$6"
  printf '%s' "$3" > "$d/input.json"
  local pid
  setsid "${argv[@]}" < "$d/input.json" > "$d/stdout" 2> "$d/stderr" &
  pid=$!
  for _ in $(seq 100); do grep -qE -- "$4" "$d/fake/calls.log" && break; sleep 0.1; done
  if [ "$5" = group ]; then kill -TERM -- "-$pid" 2> /dev/null; else kill -TERM "$pid" 2> /dev/null; fi
  rc=0
  wait "$pid" || rc=$?
  finish
}

# ---- comparing --------------------------------------------------------------

artifacts=(rc summary.json calls.log env.log decide.stdin madder.stdin new-run.input resolve.reason resolve.artifacts
  sink.stdin wake.message brief.toml task commit.labels commit.traffic commit.moxin commit.token-mode spawn.moxin-path tmp-left)

# The sed script that makes a graph's node ids and digest neutral: the ids are
# hashes of node documents that name store paths, so they move with the nixpkgs
# the stand-ins were built on.
ids_sed() { # VARIANT
  local g="$kit/graphs/$1"
  printf 's#%s#<graph-digest>#g\n' "$(cat "$g/graph.digest")"
  jq -r '.nodes | to_entries[] | "s#\(.key)#<node:\(.value.label)>#g"' "$g/graph.json"
}

normalise() { # DIR NAME: the artifact, readable for diffing; paths and ids neutral
  local dir="$1" f="$1/fake/$2"
  [ -e "$f" ] || { echo "<absent>"; return; }
  {
    case "$2" in
      summary.json)
        # A key that is null and a key that is absent read the same to a
        # consumer that picks fields with jq.
        jq -S 'with_entries(select(.value != null))' "$f" 2> /dev/null || cat "$f"
        ;;
      decide.stdin | new-run.input | resolve.reason | resolve.artifacts | sink.stdin)
        jq -S . "$f" 2> /dev/null || cat "$f"
        ;;
      madder.stdin | commit.traffic) jq -S -c . "$f" 2> /dev/null || cat "$f" ;;
      wake.message)
        head -n 1 "$f"
        sed '1,2d' "$f" | jq -S . 2> /dev/null || sed '1,2d' "$f"
        ;;
      *) cat "$f" ;;
    esac
  } | sed -E -e "s#$dir/tmp#<TMP>#g" -e "s#<TMP>/[^/ \"']*#<TMP>/<scratch>#g" \
    -e 's#^(troupe argv: dm .* --message ).*#\1<compared as wake.message>#' \
    -e 's#/nix/store/[a-z0-9]{32}-#/nix/store/<hash>-#g' \
    | sed -f <(ids_sed "$variant")
}

declare -A status       # scenario -> "identical" | space-separated differing artifacts | "unpinned"
declare -A rcs          # scenario -> exit status
details=""
total=0
scenario_order=()

compare() { # NAME
  local name="$1" a want got list="" e
  e="$expected/$name"
  scenario_order+=("$name")
  rcs[$name]="$(cat "$out/$name/fake/rc")"
  if [ -n "${PIN:-}" ]; then
    rm -rf "$e"
    mkdir -p "$e"
    for a in "${artifacts[@]}"; do
      [ -e "$out/$name/fake/$a" ] || continue
      normalise "$out/$name" "$a" > "$e/$a"
    done
    status[$name]=pinned
    return
  fi
  if [ ! -d "$e" ]; then
    status[$name]=unpinned
    total=$((total + 1))
    details+=$'\n'"### $name: no expectation under conformance/expected/ (PIN=1 writes it)"$'\n'
    return
  fi
  for a in "${artifacts[@]}"; do
    got="$(normalise "$out/$name" "$a")"
    if [ -e "$e/$a" ]; then want="$(cat "$e/$a")"; else want="<absent>"; fi
    [ "$want" != "$got" ] || continue
    list+=" $a"
    details+=$'\n'"### $name: $a (< pinned, > this run)"$'\n'"$(diff <(printf '%s\n' "$want") <(printf '%s\n' "$got") | head -n 40)"$'\n'
  done
  if [ -n "$list" ]; then
    status[$name]="${list# }"
    total=$((total + 1))
    details+=$'\n'"### $name: the end of stderr"$'\n'"$(tail -n 8 "$out/$name/stderr" | cut -c1-300)"$'\n'
  else
    status[$name]=identical
  fi
}

# ---- scenarios --------------------------------------------------------------

selected() { # NAME
  [ "${#only[@]}" -gt 0 ] || return 0
  local p
  for p in "${only[@]}"; do case "$1" in "$p"*) return 0 ;; esac; done
  return 1
}

# sc NAME KNOBS INPUT [VARIANT]: COMMIT=, SINK=, TROUPE=, STATE_ROOT= in front
# pick the stand-ins' behaviour and share a state directory.
sc() {
  selected "$1" || return 0
  run_one "$1" "$2" "$3" "${4:-default}"
  compare "$1"
}
# sig NAME KNOBS INPUT PATTERN MODE [VARIANT]
sig() {
  selected "$1" || return 0
  signalled "$1" "$2" "$3" "$4" "$5" "${6:-default}"
  compare "$1"
}

# Every request names its receipt moment, so the run key does not depend on the
# clock; a scenario that tests the key overrides it.
plain='file an issue on moxy that the restart tool should reconnect automatically'
at=1760313600123
inp() { jq -nc --arg t "$1" --arg at "$at" '{text:$t, recorded_at:$at}'; }
with() { jq -c "$2" <<< "$(inp "$1")"; }
caller='b0cc-key@flic.xmpp.example.invalid'
notified() { jq -c --arg n "$caller" '. + {notify:$n}' <<< "$(inp "$1")"; }

# ---- the defaults -----------------------------------------------------------
# What a host gets by naming no timeout, no model and no user-manager flag: the
# flag package prints every flag with its default on an unknown flag (-h is
# taken by the command framework, which prints its own help). Pinned whole, so
# a changed default is a visible change in git.
defaults_diff=""
check_defaults() {
  local got want="$expected/defaults.txt"
  got="$("$rou" run -no-such-flag 2>&1 || true)"
  if [ -n "${PIN:-}" ]; then
    mkdir -p "$expected"
    printf '%s\n' "$got" > "$want"
    return
  fi
  if [ ! -f "$want" ]; then
    defaults_diff="no expectation at conformance/expected/defaults.txt (PIN=1 writes it)"
  else
    defaults_diff="$(diff "$want" <(printf '%s\n' "$got") | head -n 40)"
  fi
}
[ "${#only[@]}" -gt 0 ] || check_defaults

# -- failure paths: the run ends without a filed issue --
sc b.drafter-cannot-complete draft-cannot,judge-ok "$(inp "$plain")"
sc c.judge-finds-duplicate draft-ok,judge-dup "$(inp "$plain")"
sc d.below-threshold draft-ok,judge-low "$(inp "$plain")"
sc d2.judge-exits-2 draft-ok,judge-fail "$(inp "$plain")"
sc d3.judge-answers-five draft-ok,judge-five "$(inp "$plain")"
sc d4.judge-no-answers-object draft-ok,judge-noanswers "$(inp "$plain")"
COMMIT=partial sc e.commit-partial draft-ok,judge-ok "$(inp "$plain")"
COMMIT=none sc e2.commit-files-nothing draft-ok,judge-ok "$(inp "$plain")"
sc h.repo-listing-fails repos-fail,draft-ok,judge-ok "$(inp "$plain")"
sc h2.madder-fails madder-fail,draft-ok,judge-ok "$(inp "$plain")"
sc i.run-not-started newrun-fail,draft-ok,judge-ok "$(inp "$plain")"
sc j.resolve-fails-every-time draft-ok,judge-ok,resolve-fail "$(inp "$plain")"
SINK=fail sc k.sink-fails draft-ok,judge-dup "$(inp "$plain")"
sc t2.frozen-traffic-tampered draft-ok,judge-ok,tamper "$(inp "$plain")"
sc m1.no-sink-graph draft-ok,judge-dup "$(inp "$plain")" no-sink
sc m2.bad-graph draft-ok,judge-ok "$(inp "$plain")" bad-graph
sc gt3.no-gate-graph draft-ok,judge-ok "$(inp "$plain")" no-gate
sc sh3.shadow-sensitive-public draft-ok,judge-clean "$(inp "$plain")" shadow
sc sh6.shadow-judge-fails draft-ok,judge-fail "$(inp "$plain")" shadow

# -- refusals: nothing is called --
sc g.no-text draft-ok,judge-ok '{"request_id":"x"}'
sc g2.blank-text draft-ok,judge-ok "$(inp '   ')"
sc f3.bad-request-id draft-ok,judge-ok "$(with "$plain" '. + {request_id:"bad id/with slash"}')"
sc o3.bad-origin draft-ok,judge-ok "$(with "$plain" '. + {origin:"Pebble Ring!"}')"
sc w5.bad-notify draft-ok,judge-ok "$(notified "$plain" | jq -c '.notify = "not a jid"')"
sc rc4.bad-recorded-at draft-ok,judge-ok "$(jq -nc --arg t "$plain" '{text:$t, recorded_at:"2026-10-13T00:00:00Z"}')"

# -- the exit wake --
sc w3.wake-on-rejection draft-ok,judge-dup "$(notified "$plain")"
TROUPE=fail sc w4.wake-send-fails draft-ok,judge-ok "$(notified "$plain")"
COMMIT=none sc rv3.review-after-failed-commit draft-ok,judge-ok "$(notified "$plain")"
sc rv4.no-review-before-judge draft-cannot "$(notified "$plain")"
sc rv5.oversized-draft-body draft-ok,draft-big,judge-ok "$(notified "$plain")"
sc sh5.shadow-wake draft-ok,judge-dup "$(notified "$plain")" shadow
# The 32 KiB bound: first the draft bodies are dropped, then the drafts.
sc rv6.wake-bodies-dropped draft-ok,draft-many,judge-ok "$(notified "$plain")"
sc rv7.wake-drafts-dropped draft-ok,draft-flood,judge-ok "$(notified "$plain")"

# -- idempotence on request_id --
shared="$out/shared-f"
mkdir -p "$shared"
STATE_ROOT="$shared" sc f1.request-id-first draft-ok,judge-ok "$(with "$plain" '. + {request_id:"req-2026-10-12-a"}')"
STATE_ROOT="$shared" sc f2.request-id-again draft-ok,judge-ok "$(with 'something else entirely' '. + {request_id:"req-2026-10-12-a"}')"
sc f4.run-exists-unresolved existing-unresolved,draft-ok,judge-ok "$(with "$plain" '. + {request_id:"req-2026-10-12-b"}')"
wshared="$out/shared-w"
mkdir -p "$wshared"
STATE_ROOT="$wshared" sc w6.wake-with-request-id draft-ok,judge-ok "$(notified "$plain" | jq -c '. + {request_id:"req-wake-a"}')"
STATE_ROOT="$wshared" sc w7.wake-request-id-again draft-ok,judge-ok "$(notified "$plain" | jq -c '. + {request_id:"req-wake-a"}')"

# -- signals: the pid alone, and the whole group (a unit's KillMode=control-group) --
sig s1.term-during-drafter.pid draft-slow,judge-ok "$(inp "$plain")" '^juggler spawn-agent' pid
sig s1.term-during-drafter.group draft-slow,judge-ok "$(inp "$plain")" '^juggler spawn-agent' group
sig s2.term-during-resolve.pid draft-ok,judge-ok,resolve-slow "$(inp "$plain")" '^juggler resolve' pid
sig s2.term-during-resolve.group draft-ok,judge-ok,resolve-slow "$(inp "$plain")" '^juggler resolve' group
sig s3.term-during-existing.pid existing-unresolved,newrun-slow,draft-ok,judge-ok "$(with "$plain" '. + {request_id:"req-2026-10-12-c"}')" '^juggler new-run' pid
sig s4.term-during-repo-listing.pid listrepos-slow,draft-ok,judge-ok "$(inp "$plain")" '^list_repos ' pid
sig s4.term-during-repo-listing.group listrepos-slow,draft-ok,judge-ok "$(inp "$plain")" '^list_repos ' group
sig s5.term-during-madder.pid madder-slow,draft-ok,judge-ok "$(inp "$plain")" '^madder argv' pid
sig s5.term-during-madder.group madder-slow,draft-ok,judge-ok "$(inp "$plain")" '^madder argv' group
sig s6.term-during-judge.pid decide-slow,draft-ok,judge-ok "$(inp "$plain")" '^juggler decide' pid
sig s6.term-during-judge.group decide-slow,draft-ok,judge-ok "$(inp "$plain")" '^juggler decide' group
sig s7.term-during-commit.pid commit-slow,draft-ok,judge-ok "$(inp "$plain")" '^issues-commit argv' pid
sig s7.term-during-commit.group commit-slow,draft-ok,judge-ok "$(inp "$plain")" '^issues-commit argv' group
sig s3.term-during-existing.group existing-unresolved,newrun-slow,draft-ok,judge-ok "$(with "$plain" '. + {request_id:"req-2026-10-12-c"}')" '^juggler new-run' group

# -- the rest: runs that file --
sc a.happy-path draft-ok,judge-ok "$(inp "$plain")"
sc a2.repo-hint draft-ok,judge-ok "$(with "$plain" '. + {repo_hint:"linenisgreat/moxy"}')"
sc a3.bad-repo-hint draft-ok,judge-ok "$(with "$plain" '. + {repo_hint:"moxy; rm -rf /"}')"
sc w1.wake-on-filed draft-ok,judge-ok "$(notified "$plain")"
sc w2.no-notify draft-ok,judge-ok "$(inp "$plain")"
sc rv1.review-on-rejection draft-ok,judge-dup "$(notified "$plain")"
sc rv2.review-on-filed draft-ok,judge-ok "$(notified "$plain")"
sc o1.origin-pebble draft-ok,judge-ok "$(with "$plain" '. + {origin:"pebble"}')"
sc o2.no-origin draft-ok,judge-ok "$(inp "$plain")"
hostile="$(printf -- '-rm -rf "$(touch /nonexistent/pwned)" `id` '"'"'single'"'"' \\back\nsecond line\n\ttab $HOME\n.')"
hostile="${hostile%.}"
sc l.hostile-text draft-ok,judge-ok "$(inp "$hostile")"
sc rc1.recorded-at-string draft-ok,judge-ok "$(jq -nc --arg t "$plain" '{text:$t, recorded_at:"1760313600123"}')"
sc rc2.recorded-at-number draft-ok,judge-ok "$(jq -nc --arg t "$plain" '{text:$t, recorded_at:1760313600123}')"
sc rc3.request-id-wins draft-ok,judge-ok "$(jq -nc --arg t "$plain" '{text:$t, request_id:"req-r3", recorded_at:"1760313600123"}')"
sc t1.late-draft-appends draft-appends,judge-ok "$(inp "$plain")"
sc gt1.gate-default draft-ok,judge-ok "$(inp "$plain")"
sc gt2.gate-dup draft-ok,judge-ok "$(inp "$plain")" gate-dup
sc sh1.shadow-duplicate draft-ok,judge-dup "$(inp "$plain")" shadow
sc sh2.shadow-faithful draft-ok,judge-faithful "$(inp "$plain")" shadow
sc sh4.shadow-sensitive-private draft-private,judge-clean "$(inp "$plain")" shadow
sc sh7.enforce-duplicate draft-ok,judge-dup "$(inp "$plain")"
# The pass rule: the lenient graph is the shadow graph with pass_rule
# no-confident-problem. An uncertain problem on enforce-public clean and on
# shadow faithful files with nothing recorded; a confident problem on clean
# still rejects; the same uncertain answers reject under the default rule.
sc pr1.lenient-unsure-files draft-ok,judge-unsure "$(inp "$plain")" lenient
sc pr2.lenient-confident-problem-rejects draft-ok,judge-clean "$(inp "$plain")" lenient
sc pr3.default-rule-unsure-rejects draft-ok,judge-unsure "$(inp "$plain")" shadow

# ---- the report -------------------------------------------------------------

# An expectation no scenario produces is a scenario that was removed or renamed.
stale=""
if [ "${#only[@]}" -eq 0 ] && [ -z "${PIN:-}" ] && [ -d "$expected" ]; then
  for e in "$expected"/*/; do
    n="$(basename "$e")"
    [ -n "${status[$n]:-}" ] || stale+=" $n"
  done
fi

if [ -n "${PIN:-}" ]; then
  echo "pinned ${#scenario_order[@]} scenarios under conformance/expected/ ; review the diff in git"
  exit 0
fi
echo "| scenario | exit | differs in |"
echo "|---|---|---|"
for s in "${scenario_order[@]}"; do
  mark=""
  [ "${status[$s]}" = identical ] || mark="!"
  printf '| %s | %s | %s%s |\n' "$s" "${rcs[$s]}" "${status[$s]}" "$mark"
done
echo
echo "A trailing ! marks a scenario whose run differs from its pinned expectation."
echo "$details"
bad_defaults=0
if [ -n "$defaults_diff" ]; then
  bad_defaults=1
  echo
  echo "### flag defaults (< pinned, > this run)"
  echo "$defaults_diff"
fi
if [ -n "$stale" ]; then
  echo
  echo "### expectations with no scenario:$stale"
  total=$((total + 1))
fi
total=$((total + bad_defaults))
echo
echo "scenarios: ${#scenario_order[@]}; differing or unpinned: $total"
exit "$total"
