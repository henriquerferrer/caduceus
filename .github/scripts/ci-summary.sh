#!/usr/bin/env bash
# Aggregates per-job statuses into a single CI summary.
# Reads job statuses from $GITHUB_STEP_SUMMARY-like artifacts via gh CLI when available.
set -euo pipefail

if ! command -v gh >/dev/null; then
  echo "## CI Summary"
  echo "(gh CLI unavailable; run-level summary skipped)"
  exit 0
fi

if [ -z "${GITHUB_RUN_ID:-}" ]; then
  echo "## CI Summary"
  echo "(GITHUB_RUN_ID unset; cannot aggregate)"
  exit 0
fi

run_id="$GITHUB_RUN_ID"
repo="${GITHUB_REPOSITORY:-}"
[ -z "$repo" ] && { echo "GITHUB_REPOSITORY unset" >&2; exit 0; }

cat <<HEADER
## CI Summary

| Job | Result |
|---|---|
HEADER

gh api "repos/$repo/actions/runs/$run_id/jobs" --jq '.jobs[] | "| \(.name) | \(.conclusion // .status) |"' || \
  echo "| (gh api failed) | unknown |"

echo
echo "**Aggregate JSON:**"
echo
echo '```json'
gh api "repos/$repo/actions/runs/$run_id/jobs" --jq '[.jobs[] | {name, status, conclusion}]' || echo '[]'
echo '```'
