#!/usr/bin/env bash
# Conformance of `roustabout run` against circus's issue-filer glue, on the same
# scenarios. usage: compare.bash GLUE_TEST_DIR ROUSTABOUT_BIN [SCENARIO...]
#
# GLUE_TEST_DIR is the build of circus infra/hosts/pkgs/issue-filer-glue/test
# (bin/issue-filer-run, variants/<name>/bin/issue-filer-run): the real glue wired
# to circus's stand-ins (fake-juggler, fake-madder, fake-commit, fake-sink,
# fake-troupe, fake-list-repos) and the real graph builder. Nothing is copied
# from circus: every setting roustabout is given (the stand-in paths, the graph
# directory, the issues moxin, the commit program, the room domain, the wake
# account, the timeouts) is read from the assignments at the top of the glue
# script, so the two implementations run the same graph document against the
# same stand-ins by construction. The scenarios are scenarios.bash's, run twice
# each; roustabout also gets ./fake-ringmaster.bash, because it runs its script
# nodes as ringmaster jobs and the glue does not.
#
# For each scenario both implementations get the same input, environment and
# stand-in behaviour, and these are compared: the exit status, the summary on
# stdout (key by key), and everything the stand-ins logged (the calls in order
# with their argv, the identity and credentials each saw, what decide, the sink,
# the commit, madder and resolve were given, the wake message, the files left in
# the scratch directory). Paths under TMPDIR are normalised; nothing else is.
# Prints a table, then each difference. Exits with the number of scenarios that
# differ in a way conformance/known-divergences.tsv does not account for.
#
# SCENARIO arguments select scenarios by name prefix (a, b, sh, ...).
# CONFORMANCE_OUT keeps the per-run directories there instead of deleting them.
set -uo pipefail
glue_dir="$1"
rou="$2"
shift 2
only=("$@")
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fake_ringmaster="$here/fake-ringmaster.bash"
known="$here/known-divergences.tsv"
default_bin="$glue_dir/bin/issue-filer-run"

if [ -n "${CONFORMANCE_OUT:-}" ]; then
  out="$CONFORMANCE_OUT"
  rm -rf "$out"
  mkdir -p "$out"
else
  out="$(mktemp -d)"
  trap 'rm -rf "$out"' EXIT
fi

# ---- what the glue was built with -------------------------------------------

# A setting from the top of a glue script: name='value' (escapeShellArg).
glue_var() { # SCRIPT NAME
  local line v
  line="$(grep -m1 -E "^$2=" "$1")" || return 1
  v="${line#*=}"
  v="${v#\'}"
  printf '%s' "${v%\'}"
}
# The glue's timeouts are timeout(1) durations or bare seconds; Go wants units.
dur() { case "$1" in *[a-z]) printf '%s' "$1" ;; *) printf '%ss' "$1" ;; esac }

roustabout_cmd() { # GLUE-SCRIPT: prints roustabout's argv, one word per line
  local g="$1" ms
  printf '%s\n' "$rou" run
  printf '%s\n' --juggler "$(glue_var "$g" juggler)" --madder "$(glue_var "$g" madder)" --troupe "$(glue_var "$g" troupe)"
  ms="$(glue_var "$g" madder_store)"
  [ -z "$ms" ] || printf '%s\n' --madder-store "$ms"
  printf '%s\n' --ringmaster "$fake_ringmaster"
  printf '%s\n' --issues-moxin "$(glue_var "$g" issues_moxin)" --commit-program "$(glue_var "$g" commit_script)"
  printf '%s\n' --room-domain "$(glue_var "$g" room_domain)" --operator-jid "$(glue_var "$g" operator_jid)"
  [ -z "$(glue_var "$g" issuer)" ] || printf '%s\n' --issuer "$(glue_var "$g" issuer)"
  [ -z "$(glue_var "$g" wake_user)" ] || printf '%s\n' --wake-user "$(glue_var "$g" wake_user)" --wake-pass-file "$(glue_var "$g" wake_pass_file)"
  printf '%s\n' --judge-model "$(glue_var "$g" jev_model)"
  printf '%s\n' --new-run-timeout "$(dur "$(glue_var "$g" new_run_timeout)")" \
    --list-repos-timeout "$(dur "$(glue_var "$g" list_repos_timeout)")" \
    --agent-timeout "$(dur "$(glue_var "$g" agent_timeout)")" \
    --agent-backstop-timeout "$(dur "$(glue_var "$g" agent_backstop_timeout)")" \
    --madder-timeout "$(dur "$(glue_var "$g" madder_timeout)")" \
    --judge-timeout "$(dur "$(glue_var "$g" judge_timeout)")" \
    --commit-timeout "$(dur "$(glue_var "$g" commit_timeout)")" \
    --sink-timeout "$(dur "$(glue_var "$g" sink_timeout)")" \
    --resolve-timeout "$(dur "$(glue_var "$g" resolve_timeout)")" \
    --signal-resolve-timeout "$(dur "$(glue_var "$g" signal_resolve_timeout)")" \
    --resolve-retry-pause "$(dur "$(glue_var "$g" resolve_retry_pause)")" \
    --resolve-attempts "$(glue_var "$g" resolve_attempts)"
  printf '%s\n' "$(glue_var "$g" graph_dir)"
}

# ---- one run ----------------------------------------------------------------

# The environment every run starts from: scenarios.bash's, which PLANTS the
# credentials neither implementation may pass on.
child_env() { # DIR KNOBS STATE
  printf '%s\n' env -u CLOWN_SESSION_ID -u TROUPE_XMPP_USER -u TROUPE_XMPP_DOMAIN -u TROUPE_XMPP_PASSWORD_FILE \
    TROUPE_MINT_USER=fake-minter TROUPE_MINT_PASSWORD_FILE=/fake/minter.pass \
    TROUPE_XMPP_PASSWORD=ambient-secret TROUPE_XMPP_RESOURCE=ambient-resource ISSUES_COMMIT_TOKEN=ambient-token \
    "TMPDIR=$1/tmp" "FAKE_LOG=$1/fake" "FAKE_SCENARIO=$2" "COMMIT=${COMMIT:-ok}" "SINK=${SINK:-ok}" "TROUPE=${TROUPE:-ok}" "XDG_STATE_HOME=$3"
}

# prepare IMPL BIN NAME KNOBS -> sets d, argv (array)
prepare() {
  local impl="$1" bin="$2" name="$3" knobs="$4" state
  d="$out/$name/$impl"
  rm -rf "$d"
  mkdir -p "$d/fake" "$d/tmp"
  : > "$d/fake/calls.log"
  : > "$d/fake/env.log"
  state="${STATE_ROOT:+$STATE_ROOT/$impl}"
  state="${state:-$d/state}"
  mkdir -p "$state"
  mapfile -t argv < <(child_env "$d" "$knobs" "$state")
  if [ "$impl" = glue ]; then
    argv+=("$bin")
  else
    local w
    while IFS= read -r w; do argv+=("$w"); done < <(roustabout_cmd "$bin")
  fi
}

finish() { # after the run: $d, $rc
  printf '%s' "$rc" > "$d/fake/rc"
  cp "$d/stdout" "$d/fake/summary.json"
  (cd "$d/tmp" && ls -A) > "$d/fake/tmp-left"
}

run_impl() { # IMPL BIN NAME KNOBS INPUT
  prepare "$1" "$2" "$3" "$4"
  rc=0
  printf '%s' "$5" | "${argv[@]}" > "$d/stdout" 2> "$d/stderr" || rc=$?
  finish
}

# signalled IMPL BIN NAME KNOBS INPUT PATTERN MODE: starts the run, sends TERM
# once calls.log shows PATTERN. MODE pid signals the process alone, as
# scenarios.bash does; group signals its whole process group, as a unit's
# KillMode=control-group does.
signalled_impl() {
  prepare "$1" "$2" "$3" "$4"
  printf '%s' "$5" > "$d/input.json"
  local pid
  setsid "${argv[@]}" < "$d/input.json" > "$d/stdout" 2> "$d/stderr" &
  pid=$!
  for _ in $(seq 100); do grep -qE -- "$6" "$d/fake/calls.log" && break; sleep 0.1; done
  if [ "$7" = group ]; then kill -TERM -- "-$pid" 2> /dev/null; else kill -TERM "$pid" 2> /dev/null; fi
  rc=0
  wait "$pid" || rc=$?
  finish
}

# ---- comparing --------------------------------------------------------------

artifacts=(rc summary.json calls.log env.log decide.stdin madder.stdin new-run.input resolve.reason resolve.artifacts
  sink.stdin wake.message brief.toml task commit.labels commit.traffic commit.moxin commit.token-mode spawn.moxin-path tmp-left)

normalise() { # DIR NAME: the artifact, readable for diffing; paths under TMPDIR neutral
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
  } | sed -e "s#$dir/tmp#<TMP>#g" -e "s#<TMP>/[^/ \"']*#<TMP>/<scratch>#g" \
    -e 's#^\(troupe argv: dm .* --message \).*#\1<compared as wake.message>#'
}

declare -A differs      # scenario -> space-separated differing artifacts
declare -A accounted    # scenario -> artifacts the known file accounts for
declare -A rcs          # scenario -> "glue/roustabout"
details=""
total=0
scenario_order=()

load_known() {
  [ -f "$known" ] || return 0
  local s a _
  while IFS=$'\t' read -r s a _; do
    case "$s" in '' | '#'*) continue ;; esac
    [ "$s" != '*' ] || s=ALL
    accounted["$s"]+=" $a "
  done < "$known"
}

# A difference is accounted for when the known file lists it for this scenario
# (or for *), either as the artifact itself or as its summary.json key.
is_accounted() { # NAME ARTIFACT
  local known_for="${accounted[$1]:-}${accounted[ALL]:-}"
  case "$known_for" in *" $2 "*) return 0 ;; esac
  # An artifact of * accounts for every difference in the scenario.
  case "${accounted[$1]:-}" in *" * "*) return 0 ;; esac
  return 1
}

compare() { # NAME
  local name="$1" a g r keys k list="" unaccounted=0
  scenario_order+=("$name")
  rcs[$name]="$(cat "$out/$name/glue/fake/rc")/$(cat "$out/$name/rou/fake/rc")"
  for a in "${artifacts[@]}"; do
    g="$(normalise "$out/$name/glue" "$a")"
    r="$(normalise "$out/$name/rou" "$a")"
    [ "$g" != "$r" ] || continue
    if [ "$a" = summary.json ]; then
      keys="$(jq -n -r --slurpfile a "$out/$name/glue/fake/summary.json" --slurpfile b "$out/$name/rou/fake/summary.json" \
        'def nn: with_entries(select(.value != null));
         ($a[0] // {} | nn) as $x | ($b[0] // {} | nn) as $y
         | [($x | keys) + ($y | keys) | unique[] | select($x[.] != $y[.])] | .[]' 2> /dev/null)"
      local unacc=() ks gk rk
      for k in $keys; do
        list+=" summary.json:$k"
        is_accounted "$name" "summary.json:$k" || unacc+=("$k")
      done
      if [ -z "$keys" ]; then
        # Not both JSON: show the raw text.
        list+=" summary.json"
        gk="$g" rk="$r"
      elif [ "${#unacc[@]}" -gt 0 ]; then
        # Only the keys the known file does not account for.
        ks="$(printf '%s\n' "${unacc[@]}" | jq -R . | jq -sc .)"
        gk="$(jq -S --argjson ks "$ks" 'with_entries(select(.key as $k | $ks | index($k)))' "$out/$name/glue/fake/summary.json")"
        rk="$(jq -S --argjson ks "$ks" 'with_entries(select(.key as $k | $ks | index($k)))' "$out/$name/rou/fake/summary.json")"
      fi
      if [ -z "$keys" ] || [ "${#unacc[@]}" -gt 0 ]; then
        details+=$'\n'"### $name: summary.json (< glue, > roustabout)"$'\n'"$(diff <(printf '%s\n' "$gk") <(printf '%s\n' "$rk") | head -n 40)"$'\n'
      fi
    else
      list+=" $a"
      # An accounted difference is listed in the table, not spelled out.
      is_accounted "$name" "$a" || details+=$'\n'"### $name: $a (< glue, > roustabout)"$'\n'"$(diff <(printf '%s\n' "$g") <(printf '%s\n' "$r") | head -n 40)"$'\n'
    fi
  done
  if [ "$(cat "$out/$name/glue/fake/rc")" != "$(cat "$out/$name/rou/fake/rc")" ]; then
    details+=$'\n'"### $name: the exit statuses differ; the end of each stderr"$'\n'"-- glue --"$'\n'"$(tail -n 8 "$out/$name/glue/stderr" | cut -c1-300)"$'\n'"-- roustabout --"$'\n'"$(tail -n 8 "$out/$name/rou/stderr" | cut -c1-300)"$'\n'
  fi
  differs[$name]="$list"
  for a in $list; do
    is_accounted "$name" "$a" || unaccounted=1
  done
  total=$((total + unaccounted))
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
  local bin="$default_bin"
  [ -z "${4:-}" ] || bin="$glue_dir/variants/$4/bin/issue-filer-run"
  run_impl glue "$bin" "$1" "$2" "$3"
  run_impl rou "$bin" "$1" "$2" "$3"
  compare "$1"
}
# sig NAME KNOBS INPUT PATTERN MODE [VARIANT]
sig() {
  selected "$1" || return 0
  local bin="$default_bin"
  [ -z "${6:-}" ] || bin="$glue_dir/variants/$6/bin/issue-filer-run"
  signalled_impl glue "$bin" "$1" "$2" "$3" "$4" "$5"
  signalled_impl rou "$bin" "$1" "$2" "$3" "$4" "$5"
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

load_known

# ---- the defaults -----------------------------------------------------------
# The scenarios hand roustabout the glue's settings explicitly, so they cannot
# show a default drifting. This compares roustabout's flag defaults with the
# defaults in the glue's default.nix (GLUE_NIX, the source file; skipped when
# unset): what a host gets by naming no timeout.
seconds() { # 170s | 2m50s | 5 | 1m0s -> whole seconds
  local v="$1" h=0 m=0 s=0
  [[ $v =~ ^([0-9]+)$ ]] && { echo "${BASH_REMATCH[1]}"; return; }
  [[ $v =~ ([0-9]+)h ]] && h="${BASH_REMATCH[1]}"
  [[ $v =~ ([0-9]+)m($|[0-9]) ]] && m="${BASH_REMATCH[1]}"
  [[ $v =~ ([0-9]+)s ]] && s="${BASH_REMATCH[1]}"
  echo $((h * 3600 + m * 60 + s))
}
defaults_table=""
defaults_bad=0
check_defaults() {
  [ -n "${GLUE_NIX:-}" ] && [ -r "$GLUE_NIX" ] || { echo "(defaults not checked: GLUE_NIX is unset)"; return; }
  local usage pair flag nixname g r
  # An unknown flag makes the flag package print every flag with its default
  # (-h is taken by the command framework, which prints its own help).
  usage="$("$rou" run -no-such-flag 2>&1 || true)"
  defaults_table+="| default | glue (default.nix) | roustabout | equal |"$'\n'"|---|---|---|---|"$'\n'
  for pair in new-run-timeout:newRunTimeout list-repos-timeout:listReposTimeout agent-timeout:agentTimeout \
    agent-backstop-timeout:agentBackstopTimeout madder-timeout:madderTimeout judge-timeout:judgeTimeout \
    commit-timeout:commitTimeout sink-timeout:sinkTimeout resolve-timeout:resolveTimeout \
    signal-resolve-timeout:signalResolveTimeout resolve-retry-pause:resolveRetryPause resolve-attempts:resolveAttempts \
    judge-model:jevModel; do
    flag="${pair%%:*}" nixname="${pair##*:}"
    g="$(sed -n "s/^  $nixname ? \"\{0,1\}\([^\",]*\)\"\{0,1\},.*/\1/p" "$GLUE_NIX" | head -n 1)"
    r="$(grep -A1 -E "^  -$flag " <<< "$usage" | sed -n 's/.*(default \(.*\)).*/\1/p' | tr -d '"')"
    case "$flag" in
      judge-model) ;;
      resolve-attempts) ;;
      *) g="$(seconds "$g")" r="$(seconds "$r")" ;;
    esac
    if [ "$g" = "$r" ] && [ -n "$g" ]; then
      defaults_table+="| $flag | $g | $r | yes |"$'\n'
    else
      defaults_table+="| $flag | $g | $r | NO |"$'\n'
      defaults_bad=$((defaults_bad + 1))
    fi
  done
  # The user manager: the glue's userManager defaults to true; roustabout's
  # drafter runs under systemd-run --user unless --no-user-manager is given.
  g="$(sed -n 's/^  userManager ? \([a-z]*\),.*/\1/p' "$GLUE_NIX" | head -n 1)"
  r="true"
  grep -qE '^  -no-user-manager' <<< "$usage" && grep -A1 -E '^  -no-user-manager' <<< "$usage" | grep -q 'default' && r=false
  if [ "$g" = "$r" ]; then
    defaults_table+="| user manager (systemd-run --user) | $g | $r | yes |"$'\n'
  else
    defaults_table+="| user manager (systemd-run --user) | $g | $r | NO |"$'\n'
    defaults_bad=$((defaults_bad + 1))
  fi
}
check_defaults

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

# -- signals: the pid alone (scenarios.bash), and the whole group (a unit's KillMode=control-group) --
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

# ---- the report -------------------------------------------------------------

if [ -n "$defaults_table" ]; then
  printf '%s\n' "$defaults_table"
fi
echo "| scenario | exit (glue/roustabout) | differs in |"
echo "|---|---|---|"
for s in "${scenario_order[@]}"; do
  list="${differs[$s]# }"
  mark=""
  for a in $list; do
    is_accounted "$s" "$a" || mark="!"
  done
  printf '| %s | %s | %s%s |\n' "$s" "${rcs[$s]}" "${list:-identical}" "$mark"
done
echo
echo "A trailing ! marks a difference conformance/known-divergences.tsv does not account for."
echo "$details"
echo
total=$((total + defaults_bad))
echo "scenarios: ${#scenario_order[@]}; defaults that differ: $defaults_bad; with an unaccounted difference: $total"
exit "$total"
