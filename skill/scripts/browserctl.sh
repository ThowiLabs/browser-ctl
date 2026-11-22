#!/usr/bin/env bash
# browserctl.sh — thin wrapper around the browser-ctl binary for agent hosts.
#
# Usage:
#   browserctl.sh run "goto https://example.com
# get title" [--json] [--port N]
#   browserctl.sh serve [--port N] [--no-wait]
#   browserctl.sh tabs [--port N]
#   browserctl.sh status
#
# With --json, the result is printed as a single JSON object:
#   {"ok":true,"output":"..."}   or   {"ok":false,"error":"..."}

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN=""
for candidate in "$REPO_ROOT/bin/browser-ctl" "$REPO_ROOT/bin/browser-ctl.exe"; do
  if [ -x "$candidate" ]; then BIN="$candidate"; break; fi
done
if [ -z "$BIN" ]; then
  err="browser-ctl binary not found; run: CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o bin/browser-ctl ./cmd/browser-ctl"
  if [ "${1:-}" = "--json" ] || [ "${*}" *=*"--json"* ]; then
    printf '{"ok":false,"error":"%s"}\n' "$err"
  else
    echo "$err" >&2
  fi
  exit 1
fi

# Parse flags out of the arguments.
JSON=0
PORT=""
NOWAIT=0
sub=""
commands=""
declare -a rest=()

for a in "$@"; do
  case "$a" in
    --json) JSON=1 ;;
    --port) : ;; # value handled by next iteration via pending
    --port=*) PORT="${a#--port=}" ;;
    --no-wait) NOWAIT=1 ;;
    run|serve|tabs|status)
      if [ -z "$sub" ]; then sub="$a"; else rest+=("$a"); fi ;;
    *)
      if [ "$sub" = "run" ]; then
        if [ -z "$commands" ]; then commands="$a"; else commands="$commands
$a"; fi
      else
        rest+=("$a")
      fi ;;
  esac
done

# Handle "--port N" (space form): find --port and take the next arg.
args=("$@")
for i in "${!args[@]}"; do
  if [ "${args[$i]}" = "--port" ] && [ $((i+1)) -lt ${#args[@]} ]; then
    PORT="${args[$((i+1))]}"
  fi
done

if [ -z "$sub" ]; then
  msg="first argument must be run|serve|tabs|status"
  if [ "$JSON" = 1 ]; then printf '{"ok":false,"error":"%s"}\n' "$msg"; else echo "$msg" >&2; fi
  exit 2
fi

final=("$sub")
[ -n "$PORT" ] && final+=("--port" "$PORT")
if [ "$sub" = "serve" ] && [ "$NOWAIT" = 1 ]; then final+=("--no-wait"); fi
if [ "$sub" = "run" ] && [ -n "$commands" ]; then final+=("$commands"); fi

output="$("$BIN" "${final[@]}" 2>&1)"
code=$?

if [ "$JSON" = 1 ]; then
  # Minimal JSON string escaping.
  esc() {
    local s="$1"
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    s="${s//$'\n'/\\n}"
    s="${s//$'\r'/\\r}"
    s="${s//$'\t'/\\t}"
    printf '%s' "$s"
  }
  if [ $code -eq 0 ]; then
    printf '{"ok":true,"output":"%s"}\n' "$(esc "$output")"
  else
    printf '{"ok":false,"error":"%s"}\n' "$(esc "$output")"
  fi
else
  printf '%s\n' "$output"
fi
exit $code
