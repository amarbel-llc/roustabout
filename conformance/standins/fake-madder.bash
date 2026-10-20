# Stand-in for `madder write` (see ./default.nix). Logs its argv, keeps what
# it read on stdin as $FAKE_LOG/madder.stdin, and prints the NDJSON record
# madder-write(1) prints when stdout is a pipe: {id, size, source}. The id is
# "fake-" plus the sha256 of the bytes, so the glue's traffic_blob is
# madder://blobs/fake-<sha256 of the traffic file>. Under the madder-fail
# knob it exits 1 with nothing on stdout.
set -euo pipefail
log="${FAKE_LOG:?}"
{
  printf 'madder argv:'
  printf ' %q' "$@"
  printf '\n'
} >> "$log/calls.log"
[ "${1:-}" = write ] || { echo "fake madder: only write is understood" >&2; exit 2; }
case ",${FAKE_SCENARIO:-}," in
  # madder-slow: takes 2s after logging its call, then reads stdin and answers
  # (for a signal delivered while the glue waits on the store).
  *,madder-slow,*) sleep 2 ;;
esac
case ",${FAKE_SCENARIO:-}," in
  *,madder-fail,*) cat > /dev/null; echo "madder: write: open store: permission denied" >&2; exit 1 ;;
esac
cat > "$log/madder.stdin"
sha="$(sha256sum < "$log/madder.stdin" | cut -d' ' -f1)"
size="$(wc -c < "$log/madder.stdin")"
jq -nc --arg id "fake-$sha" --argjson size "$size" '{id:$id, size:$size, source:"-"}'
