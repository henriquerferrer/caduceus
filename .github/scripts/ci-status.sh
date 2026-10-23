#!/usr/bin/env bash
# Emits a markdown + JSON status block to $GITHUB_STEP_SUMMARY.
# Usage: ci-status.sh <job-name> <status>
set -euo pipefail

JOB="$1"
STATUS="$2"

case "$STATUS" in
  success) icon="✅"; status_lc="success" ;;
  failure) icon="❌"; status_lc="failure" ;;
  cancelled) icon="⚠️"; status_lc="cancelled" ;;
  *) icon="❔"; status_lc="$STATUS" ;;
esac

annotations="[]"

repro="just ci-replay $JOB"
next_action="Run \`$repro\` locally to reproduce this CI failure."

cat <<HEADER
## CI Status: $JOB

**Result:** $icon $status_lc

\`\`\`json
HEADER

jq -nc \
  --arg job "$JOB" \
  --arg status "$status_lc" \
  --argjson annotations "$annotations" \
  --arg repro "$repro" \
  --arg next_action "$next_action" \
  '{job: $job, status: $status, annotations: $annotations, repro: $repro, next_action: $next_action}'

cat <<FOOTER
\`\`\`
FOOTER
