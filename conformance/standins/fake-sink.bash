# Stand-in for the graph's failure sink (see ./default.nix): logs that it ran
# and keeps the result JSON it was given on stdin as $FAKE_LOG/sink.stdin.
# Under SINK=fail it exits 3, to show the sink's exit never changes the run's
# outcome.
set -euo pipefail
log="${FAKE_LOG:?}"
printf 'sink\n' >> "$log/calls.log"
cat > "$log/sink.stdin"
[ "${SINK:-ok}" != fail ] || exit 3
