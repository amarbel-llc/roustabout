# Stand-in for `issues-commit` (pkgs/issue-filer-commit; see ./default.nix).
# Logs its argv and what it was given, checks the glue's side of the contract
# (a readable traffic file with a recorded draft, a readable token file,
# ISSUES_MOXIN set, and the token NOT in its environment: the real script reads
# it from the file), and prints the result the real one would: every draft
# filed (exit 0), or under COMMIT=partial one filed and one not (exit 1), or
# under COMMIT=none nothing filed (exit 2).
set -euo pipefail
log="${FAKE_LOG:?}"
{
  printf 'issues-commit argv:'
  printf ' %q' "$@"
  printf '\n'
} >> "$log/calls.log"
printf '%s mint=%s/%s pw=%s res=%s token=%s\n' "issues-commit" "${TROUPE_MINT_USER:-}" "${TROUPE_MINT_PASSWORD_FILE:-}" \
  "${TROUPE_XMPP_PASSWORD:-}" "${TROUPE_XMPP_RESOURCE:-}" "${ISSUES_COMMIT_TOKEN:-}" >> "$log/env.log"
# Under the commit-slow knob the commit takes 2s after logging its call, before
# it checks anything or prints its result (for a signal delivered while the glue
# waits on it).
case ",${FAKE_SCENARIO:-}," in
  *,commit-slow,*) sleep 2 ;;
esac
# The labels the glue chose from the request's origin (circus#322).
printf '%s' "${ISSUES_LABELS-<unset>}" > "$log/commit.labels"
traffic="${1:-}"
tokfile="${2:-}"
# Under the draft-appends knob the stand-in drafter's leftover process is
# still appending to the ORIGINAL traffic file; the commit waits until it
# has finished, so the file it is given is read only after the late writes.
case ",${FAKE_SCENARIO:-}," in
  *,draft-appends,*) for _ in $(seq 100); do [ -e "$log/appended" ] && break; sleep 0.1; done ;;
esac
[ -r "$traffic" ] || { echo "fake issues-commit: no readable traffic file" >&2; exit 2; }
[ -r "$tokfile" ] || { echo "fake issues-commit: no readable token file" >&2; exit 2; }
[ -n "${ISSUES_MOXIN:-}" ] || { echo "fake issues-commit: ISSUES_MOXIN is not set" >&2; exit 2; }
cp "$traffic" "$log/commit.traffic"
printf '%s' "$ISSUES_MOXIN" > "$log/commit.moxin"
stat -c '%a' "$tokfile" > "$log/commit.token-mode"
jq -e 'select(.call == "create_issue")' "$traffic" > /dev/null || { echo "fake issues-commit: no draft in the traffic file" >&2; exit 2; }
filed='{"repo":"linenisgreat/moxy","number":50,"uri":"smith://forge.example.invalid/linenisgreat/moxy/issues/50"}'
case "${COMMIT:-ok}" in
  partial)
    jq -nc --argjson f "$filed" '{state:"failed", issues:[$f],
      not_filed:[{repo:"linenisgreat/clown", title:"second draft", reason:"create_issue failed: 502 bad gateway"}]}'
    exit 1
    ;;
  none)
    jq -nc '{state:"failed", issues:[], not_filed:[{repo:"linenisgreat/moxy", title:"Restart tool should reconnect automatically", reason:"create_issue failed: 502 bad gateway"}]}'
    exit 2
    ;;
  *)
    jq -nc --argjson f "$filed" '{state:"filed", issues:[$f], not_filed:[]}'
    ;;
esac
