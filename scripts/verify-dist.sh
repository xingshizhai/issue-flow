#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary="$root/dist/issue-flow_linux_amd64"

if [ ! -x "$binary" ]; then
  echo "missing executable distribution artifact: $binary" >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM

cat >"$work/.issue-flow.yaml" <<'EOF'
version: 1
provider:
  type: fake
  data_file: issues.json
workflow:
  ready_label: agent-ready
  claimed_label: agent-claimed
  working_label: agent-working
  blocked_label: agent-blocked
  review_label: agent-review
  done_label: agent-done
  lease_minutes: 120
  auto_close: false
  sync_provider_state: true
  provider_states:
    ready: open
    working: progressing
    done: closed
project:
  instruction_files: []
validation:
  commands: []
git:
  branch_pattern: "{type}/issue-{number}-{slug}"
  allow_commit: false
  allow_push: false
  allow_pull_request: false
automation:
  level: patch
security:
  redact_keys: [password, token, secret]
EOF
printf '[]\n' >"$work/issues.json"

output=$($binary doctor --project "$work" --format json)
printf '%s\n' "$output" | grep -q '"ok":true' || {
  echo "distribution artifact is stale or incompatible with the current config schema" >&2
  printf '%s\n' "$output" >&2
  exit 1
}
