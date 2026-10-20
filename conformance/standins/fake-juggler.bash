# Stand-in for `juggler` (see ./default.nix), adapted from
# pebble-graph-glue/test/fake-juggler.bash. Logs each call's verb, argv and
# the identity environment to $FAKE_LOG/calls.log, the credentials it could
# see to $FAKE_LOG/env.log, keeps what the call was given to read, and answers
# in the shapes of clown 814d90a0 (NewRunResult, WaitRecord, ResolveOutcome;
# jugglerdecide's gating rule). FAKE_SCENARIO is a comma-separated list of
# knobs:
#   draft-ok        spawn --brief --wait: the drafter records one list_issues
#                   search and one create_issue draft into the traffic file
#                   the brief's [env] names, then succeeds
#   draft-big       as draft-ok, with a 9000-character draft body
#   draft-cannot    the drafter calls cannot_complete (exit 2)
#   draft-slow      the drafter takes 2s before succeeding (for a signal
#                   delivered while the glue waits on it)
#   draft-appends   as draft-ok, and a process left behind keeps APPENDING
#                   drafts to the traffic file after the wait returned: it
#                   waits for the glue's madder write (the freeze is done by
#                   then), appends ten more create_issue lines, then touches
#                   $FAKE_LOG/appended, which the stand-in commit waits for
#   draft-many      as draft-ok, with 12 create_issue drafts, each with a
#                   4000-character body (the exit wake's review then exceeds
#                   32 KiB and its bodies are dropped, the drafts kept)
#   draft-flood     as draft-ok, with 400 create_issue drafts, each with a
#                   300-character title and a 100-character body (the review
#                   exceeds 32 KiB even without bodies, so its drafts are
#                   dropped too)
#   decide-slow     decide takes 2s (after logging its call and reading its
#                   stdin), then answers
#   judge-ok        decide: every question in the payload is answered ok at 0.9
#   judge-dup       decide: `no-duplicate-of-existing-issue` is a problem at 0.8,
#                   the rest ok at 0.9
#   judge-low       decide: all ok, but `explicit-instruction-to-file` at 0.4
#   judge-clean     decide: `no-sensitive-content` is a problem at 0.8, the rest
#                   ok at 0.9
#   judge-faithful  decide: `draft-says-what-the-requester-said-and-nothing-else`
#                   ok but at 0.2, the rest ok at 0.9
#   draft-private   as draft-ok, but the draft targets friedenberg/dotfiles,
#                   listed " (private)"
#   judge-five      decide: one answer short, `no-duplicate-of-existing-issue`
#                   missing
#   judge-noanswers decide exits 0 with no answers object
#   judge-fail      decide exits 2 with a reason (no usable answer)
#   tamper          decide also appends a line to the glue's FROZEN traffic
#                   file (its path is derived from the brief's traffic path)
#   newrun-fail     spawn --new-run exits 1 before minting anything
#   newrun-slow     spawn --new-run takes 2s (after logging its call)
#   existing-unresolved  spawn --new-run answers existing true, resolved false
#   resolve-fail    resolve exits 1, every time
#   resolve-slow    resolve takes 2s (after logging its call), then succeeds
# A run juggler already knows is a file under $XDG_STATE_HOME/fake-juggler/
# runs/<key>: new-run creates it and answers `existing: true` (with
# `resolved` as the file says) when it is already there, like the real
# juggler's run store; resolve marks it resolved.
set -euo pipefail
log="${FAKE_LOG:?}"
scenario="${FAKE_SCENARIO:?}"
knob() { case ",$scenario," in *",$1,"*) return 0 ;; *) return 1 ;; esac; }

verb="$1"
flag() { # value of --NAME in the argv, either "--NAME V" or "--NAME=V"
  local name="$1" prev=""
  shift
  for a in "$@"; do
    if [ "$prev" = "--$name" ]; then printf '%s' "$a"; return; fi
    case "$a" in "--$name="*) printf '%s' "${a#*=}"; return ;; esac
    prev="$a"
  done
}
has() { local name="$1"; shift; for a in "$@"; do [ "$a" = "--$name" ] && return 0; done; return 1; }

if [ "$verb" = spawn ] && has new-run "$@"; then verb=new-run; fi
if [ "$verb" = spawn ] && [ -n "$(flag brief "$@")" ]; then verb=spawn-agent; fi
{
  printf '%s' "juggler $verb as=${CLOWN_SESSION_ID:-}/${TROUPE_XMPP_USER:-}@${TROUPE_XMPP_DOMAIN:-}/${TROUPE_XMPP_PASSWORD_FILE:-} argv:"
  printf ' %q' "$@"
  printf '\n'
} >> "$log/calls.log"
printf '%s mint=%s/%s pw=%s res=%s token=%s\n' "juggler $verb" "${TROUPE_MINT_USER:-}" "${TROUPE_MINT_PASSWORD_FILE:-}" \
  "${TROUPE_XMPP_PASSWORD:-}" "${TROUPE_XMPP_RESOURCE:-}" "${ISSUES_COMMIT_TOKEN:-}" >> "$log/env.log"

# From clown c4aa51c: a brief may not name MOXIN_PATH or any MOXY_* key (the
# real one exits 1 before it mints), and the agent's unit gets the SPAWNER's
# MOXIN_PATH, which is recorded here for the scenarios to check.
if [ "$verb" = spawn-agent ]; then
  if grep -qE '^(MOXIN_PATH|MOXY_[A-Z_]*) *=' "$(flag brief "$@")"; then
    echo "juggler: spawn: brief [env] may not set MOXIN_PATH or MOXY_*" >&2
    exit 1
  fi
  printf '%s' "${MOXIN_PATH:-}" > "$log/spawn.moxin-path"
fi

# The traffic file the brief names, and the glue's frozen copy beside it.
traffic_of_brief() { sed -n 's/^ISSUES_TRAFFIC_FILE = "\(.*\)"$/\1/p' "$1" | head -n 1; }

runs_dir="${XDG_STATE_HOME:?}/fake-juggler/runs"
case "$verb" in
  new-run)
    cp "$(flag input "$@")" "$log/new-run.input"
    key="$(flag run-key "$@")"
    domain="$(flag room-domain "$@")"
    operator="$(flag operator-jid "$@")"
    # The real juggler refuses these before it mints anything.
    [ -z "$(flag room "$@")" ] || { echo "juggler: spawn: exactly one of an existing room or a room domain is required" >&2; exit 1; }
    [ -n "$domain" ] && [ -n "$operator" ] || { echo "juggler: spawn: --room-domain requires --operator-jid" >&2; exit 1; }
    [[ $key =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || { echo "juggler: spawn: run key $key is not a valid id" >&2; exit 1; }
    room="${key,,}@$domain"
    ! knob newrun-fail || { echo "juggler: spawn: minting the run root: troupe mint: exit status 1" >&2; exit 1; }
    ! knob newrun-slow || sleep 2
    mkdir -p "$runs_dir"
    existing=false
    resolved=false
    if knob existing-unresolved; then
      existing=true
    elif [ -e "$runs_dir/$key" ]; then
      existing=true
      resolved="$(cat "$runs_dir/$key")"
    else
      printf 'false' > "$runs_dir/$key"
    fi
    jq -nc --arg k "$key" --arg room "$room" --arg op "$operator" --argjson e "$existing" --argjson r "$resolved" \
      '{run_key:$k, root_principal:"root-uuid-1", root_credential_ref:"/fake/root.pass",
        root_jid:"root-uuid-1@xmpp.example.invalid", room:$room, run_job:"job-run-1", operator_jid:$op,
        root_stanza:"chat-request-1", existing:$e, resolved:$r, torn_down:$r}'
    ;;
  decide)
    cat > "$log/decide.stdin"
    ! knob decide-slow || sleep 2
    # jugglerdecide's gating rule (clown 814d90a0): a payload with more than
    # one question needs --question, and it must name one of them.
    n="$(jq '.questions | length' "$log/decide.stdin")"
    gate="$(flag question "$@")"
    if [ "$n" -gt 1 ] && [ -z "$gate" ]; then
      echo "juggler: decide: $n questions given; --question is required to pick the gating one" >&2
      exit 1
    fi
    if [ -n "$gate" ] && ! jq -e --arg g "$gate" '.questions | has($g)' "$log/decide.stdin" > /dev/null; then
      echo "juggler: decide: question $gate is not in the payload" >&2
      exit 1
    fi
    if knob tamper; then
      frozen="$(dirname "$(traffic_of_brief "$log/brief.toml")")/traffic.frozen.jsonl"
      chmod u+w "$frozen"
      jq -nc '{call:"create_issue", draft:{repo:"linenisgreat/clown", title:"tampered", body:"", labels:[]}, repo_listing:"linenisgreat/clown",
        validation:{repo_listed:true, repo_is_mirror:false, pii_scan:"clean", duplicate_search_recorded:false}}' >> "$frozen"
    fi
    if knob judge-fail; then
      jq -nc '{reason:"endpoint returned status 502: bad gateway", http_status:502}'
      exit 2
    fi
    if knob judge-noanswers; then
      jq -nc '{id:"gen-dec-1", model:"typesafe/jev-1.13"}'
      exit 0
    fi
    answer() { jq -nc --arg c "$1" --argjson conf "$2" '{type:"choice", choice:$c, confidence:$conf, probabilities:{($c):$conf}}'; }
    ok="$(answer ok 0.9)"
    answers="$(jq -c --argjson ok "$ok" '.questions | map_values($ok)' "$log/decide.stdin")"
    set_answer() { answers="$(jq -c --arg k "$1" --argjson a "$2" '.[$k] = $a' <<< "$answers")"; }
    ! knob judge-dup || set_answer no-duplicate-of-existing-issue "$(answer problem 0.8)"
    ! knob judge-low || set_answer explicit-instruction-to-file "$(answer ok 0.4)"
    ! knob judge-clean || set_answer no-sensitive-content "$(answer problem 0.8)"
    ! knob judge-faithful || set_answer draft-says-what-the-requester-said-and-nothing-else "$(answer ok 0.2)"
    # Uncertain problems, as Jev gave them on 2026-10-20: 0.49 ok / 0.51 problem
    # is confidence 0.02.
    ! knob judge-unsure || set_answer no-sensitive-content "$(answer problem 0.02)"
    ! knob judge-unsure || set_answer draft-says-what-the-requester-said-and-nothing-else "$(answer problem 0.3)"
    ! knob judge-five || answers="$(jq -c 'del(.["no-duplicate-of-existing-issue"])' <<< "$answers")"
    jq -nc --argjson a "$answers" '{answers:$a, id:"gen-dec-1", model:"typesafe/jev-1.13"}'
    ;;
  spawn-agent)
    brief="$(flag brief "$@")"
    cp "$brief" "$log/brief.toml"
    cp "$(flag task "$@")" "$log/task"
    room="$(flag run-key "$@")@runs.example.invalid"
    record() { jq -nc --arg s "$1" --arg m "$2" --argjson a "$3" --argjson cc "$4" --arg room "$room" \
      '{jid:"agent-1@xmpp.example.invalid", job:"job-agent-1", room:$room, state:$s,
        reason:"x", message:$m, ledger:"/fake/ledger.json", artifacts:$a, cannot_complete:$cc}'; }
    # The drafter's tool calls, as the issues moxin records them in record
    # mode: appended to the traffic file the brief names (so the glue's own
    # list_repos line, written before the spawn, stays first).
    traffic="$(traffic_of_brief "$brief")"
    [ -n "$traffic" ] && [ -w "$traffic" ] || { echo "fake juggler: the brief names no writable ISSUES_TRAFFIC_FILE" >&2; exit 1; }
    if knob draft-cannot; then
      record failed 'cannot_complete: "no confident repository match"' '[]' \
        '{"reason":"no confident repository match for \"the router thing\"; closest: linenisgreat/moxy, linenisgreat/clown"}'
      exit 2
    fi
    ! knob draft-slow || sleep 2
    big_body="the restart tool should reconnect automatically"
    ! knob draft-big || big_body="$(head -c 9000 /dev/zero | tr '\0' 'x')"
    draft_repo=linenisgreat/moxy draft_listing=linenisgreat/moxy
    if knob draft-private; then draft_repo=friedenberg/dotfiles draft_listing="friedenberg/dotfiles (private)"; fi
    draft_line() { jq -nc --arg t "$1" --arg b "$big_body" --arg r "$draft_repo" --arg l "$draft_listing" '{call:"create_issue",
      draft:{repo:$r, title:$t, body:$b, labels:["zz-from_pebble"]},
      repo_listing:$l,
      validation:{repo_listed:true, repo_is_mirror:false, pii_scan:"clean", duplicate_search_recorded:true}}'; }
    {
      jq -nc '{call:"list_issues", repo:"linenisgreat/moxy", query:"restart", state:"all", results:[]}'
      if knob draft-many; then
        big_body="$(head -c 4000 /dev/zero | tr '\0' 'x')"
        for i in $(seq 12); do draft_line "draft $i"; done
      elif knob draft-flood; then
        big_body="$(head -c 100 /dev/zero | tr '\0' 'x')"
        pad="$(head -c 290 /dev/zero | tr '\0' 't')"
        for i in $(seq 400); do draft_line "draft $i $pad"; done
      else
        draft_line "Restart tool should reconnect automatically"
      fi
    } >> "$traffic"
    if knob draft-appends; then
      # Left behind with every descriptor closed, so the glue's wait on this
      # call's stdout ends now, not when the appender does.
      (
        for _ in $(seq 100); do [ -e "$log/madder.stdin" ] && break; sleep 0.1; done
        for i in $(seq 10); do draft_line "late draft $i" >> "$traffic"; done
        : > "$log/appended"
      ) < /dev/null > /dev/null 2>&1 &
      disown
    fi
    # A recorded draft has no uri: nothing was filed (design P4).
    record succeeded "evaluator passed" '[{"tool":"issues_create_issue","kind":null,"uris":[]}]' null
    ;;
  resolve)
    # The real one refuses one flag of the canary pair without the other, and
    # this glue passes neither.
    [ -z "$(flag result-line "$@")" ] && [ -z "$(flag canary-room "$@")" ] || { echo "juggler: resolve: this glue passes no result line" >&2; exit 1; }
    case "$(flag state "$@")" in
      succeeded | failed) ;;
      *) echo "juggler: resolve: --state must be succeeded or failed" >&2; exit 2 ;;
    esac
    [ -n "$(flag reason "$@")" ] || { echo "juggler: resolve: --reason is required" >&2; exit 2; }
    printf '%s' "$(flag reason "$@")" > "$log/resolve.reason"
    printf '%s' "$(flag fallback-artifacts "$@")" > "$log/resolve.artifacts"
    ! knob resolve-fail || { echo "juggler: resolve: ringmaster done: exit status 1" >&2; exit 1; }
    ! knob resolve-slow || sleep 2
    mkdir -p "$runs_dir"
    for f in "$runs_dir"/*; do [ -e "$f" ] && printf 'true' > "$f"; done
    jq -nc --arg s "$(flag state "$@")" \
      '{run_key:"k", run_job:"job-run-1", state:$s, reason:(if $s == "succeeded" then "normal" else "failed" end),
        ledger:"/fake/runs/k/ledger.json", woken:["issue-filer-test"], already_resolved:false, torn_down:true,
        teardown:[{tool:"teardown",kind:"account",ok:true,uris:[],principal:"agent-uuid-1",jid:"agent-1@xmpp.example.invalid",reason:"revoked"},
                  {tool:"teardown",kind:"account",ok:true,uris:[],principal:"root-uuid-1",jid:"root-uuid-1@xmpp.example.invalid",reason:"revoked"}]}'
    ;;
  *)
    echo "fake juggler: unexpected verb $verb" >&2
    exit 1
    ;;
esac
