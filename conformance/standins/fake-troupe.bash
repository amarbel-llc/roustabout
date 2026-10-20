# Stand-in for troupe(1), the one `troupe dm` the glue sends as the exit wake
# (circus#309; see ./default.nix). Logs its argv to calls.log and the identity
# it was given to env.log, in the same shape as the other stand-ins, keeps the
# --message as $FAKE_LOG/wake.message, and prints a chat id as the real one
# does. Under TROUPE=fail it exits 1, to show a failed wake never fails a run.
set -euo pipefail
log="${FAKE_LOG:?}"
{
  printf 'troupe argv:'
  printf ' %q' "$@"
  printf '\n'
} >> "$log/calls.log"
printf '%s as=%s//%s mint=%s/%s pw=%s res=%s token=%s\n' "troupe" "${TROUPE_XMPP_USER:-}" "${TROUPE_XMPP_PASSWORD_FILE:-}" \
  "${TROUPE_MINT_USER:-}" "${TROUPE_MINT_PASSWORD_FILE:-}" \
  "${TROUPE_XMPP_PASSWORD:-}" "${TROUPE_XMPP_RESOURCE:-}" "${ISSUES_COMMIT_TOKEN:-}" >> "$log/env.log"
prev=""
for a in "$@"; do
  [ "$prev" != --message ] || printf '%s' "$a" > "$log/wake.message"
  prev="$a"
done
[ "${TROUPE:-ok}" != fail ] || {
  echo "troupe dm: connect: connection refused" >&2
  exit 1
}
echo chat-fake-1
