#!/usr/bin/env bash
# Stand-in for ringmaster(1), for conformance/compare.bash. circus's glue runs
# its commit and sink scripts directly; `roustabout run` runs them as ringmaster
# jobs (`ringmaster start`, then `ringmaster done`), so only roustabout calls
# this. It logs to $FAKE_LOG/ringmaster.log, never to calls.log, so the two
# implementations' calls.log stay comparable. `start` prints a job id; `done`
# checks that it is given a job id that `start` issued.
set -euo pipefail
log="${FAKE_LOG:?}"
printf 'ringmaster %s session=%s\n' "$*" "${CLOWN_SESSION_ID:-}" >> "$log/ringmaster.log"
case "${1:-}" in
  start)
    n="$(($(cat "$log/ringmaster.n" 2> /dev/null || echo 0) + 1))"
    printf '%s' "$n" > "$log/ringmaster.n"
    echo "job-script-$n"
    ;;
  done)
    grep -q "^ringmaster start " "$log/ringmaster.log" || {
      echo "fake ringmaster: done $2 without a start" >&2
      exit 1
    }
    ;;
  *)
    echo "fake ringmaster: unexpected verb ${1:-}" >&2
    exit 1
    ;;
esac
