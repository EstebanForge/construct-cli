#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ "${NO_COLOR:-0}" == "1" || -n "${NO_COLOR:-}" ]]; then
  RED=''
  GREEN=''
  YELLOW=''
  NC=''
elif [[ "${FORCE_COLOR:-0}" == "1" || -t 1 ]]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[1;33m'
  NC='\033[0m'
else
  # Default to colored output for local runs, even when tty detection is flaky.
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[1;33m'
  NC='\033[0m'
fi

CI_MODE="false"
if [[ "${1:-}" == "--ci" ]]; then
  CI_MODE="true"
fi

UNIT_LOG="$(mktemp)"
INTEGRATION_LOG="$(mktemp)"
trap 'rm -f "$UNIT_LOG" "$INTEGRATION_LOG"' EXIT

unit_exit=0
integration_exit=0

if [[ "$CI_MODE" == "true" ]]; then
  echo "Running unit tests (CI fast)..."
  go test -v ./internal/... | tee "$UNIT_LOG"
  unit_exit=${PIPESTATUS[0]}
else
  echo "Running unit tests..."
  go test -v -race -coverprofile=coverage.out ./internal/... | tee "$UNIT_LOG"
  unit_exit=${PIPESTATUS[0]}
fi

unit_pass=$(grep -c '^--- PASS:' "$UNIT_LOG" || true)
unit_fail=$(grep -c '^--- FAIL:' "$UNIT_LOG" || true)
unit_skip=$(grep -c '^--- SKIP:' "$UNIT_LOG" || true)
unit_pkg_ok=$(grep -c '^ok[[:space:]]' "$UNIT_LOG" || true)
unit_pkg_fail=$(grep -c '^FAIL[[:space:]]' "$UNIT_LOG" || true)

if [[ $unit_exit -eq 0 ]]; then
  echo -e "${GREEN}✓ Unit tests passed${NC}"
else
  echo -e "${RED}✗ Unit tests failed${NC}"
fi

echo "Building construct..."
go build -ldflags "-s -w" -o construct ./cmd/construct
echo "✓ Built: construct"

echo "Running integration tests..."
./scripts/integration.sh ./construct | tee "$INTEGRATION_LOG"
integration_exit=${PIPESTATUS[0]}

integration_clean_log="$(mktemp)"
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g' "$INTEGRATION_LOG" > "$integration_clean_log"
integration_total=$(awk -F: '/Total tests run:/ {gsub(/[[:space:]]/, "", $2); print $2}' "$integration_clean_log" | tail -n1)
integration_pass=$(awk -F: '/Tests passed:/ {gsub(/[[:space:]]/, "", $2); print $2}' "$integration_clean_log" | tail -n1)
integration_fail=$(awk -F: '/Tests failed:/ {gsub(/[[:space:]]/, "", $2); print $2}' "$integration_clean_log" | tail -n1)
rm -f "$integration_clean_log"

integration_total=${integration_total:-0}
integration_pass=${integration_pass:-0}
integration_fail=${integration_fail:-0}

unit_pass_color="$GREEN"
unit_fail_color="$GREEN"
unit_skip_color="$GREEN"
unit_pkg_ok_color="$GREEN"
unit_pkg_fail_color="$GREEN"
integration_total_color="$GREEN"
integration_pass_color="$GREEN"
integration_fail_color="$GREEN"

if [[ "$unit_fail" -gt 0 ]]; then
  unit_fail_color="$RED"
fi
if [[ "$unit_skip" -gt 0 ]]; then
  unit_skip_color="$YELLOW"
fi
if [[ "$unit_pkg_fail" -gt 0 ]]; then
  unit_pkg_fail_color="$RED"
fi
if [[ "$integration_fail" -gt 0 ]]; then
  integration_fail_color="$RED"
fi
if [[ "$integration_total" -eq 0 ]]; then
  integration_total_color="$YELLOW"
fi
if [[ "$integration_pass" -eq 0 ]]; then
  integration_pass_color="$YELLOW"
fi

echo ""
echo "======================================"
echo "All Tests Summary"
echo "======================================"
echo -e "Unit tests passed:      ${unit_pass_color}${unit_pass}${NC}"
echo -e "Unit tests failed:      ${unit_fail_color}${unit_fail}${NC}"
echo -e "Unit tests skipped:     ${unit_skip_color}${unit_skip}${NC}"
echo -e "Unit packages passed:   ${unit_pkg_ok_color}${unit_pkg_ok}${NC}"
echo -e "Unit packages failed:   ${unit_pkg_fail_color}${unit_pkg_fail}${NC}"
echo -e "Integration total:      ${integration_total_color}${integration_total}${NC}"
echo -e "Integration passed:     ${integration_pass_color}${integration_pass}${NC}"
echo -e "Integration failed:     ${integration_fail_color}${integration_fail}${NC}"

if [[ $unit_exit -ne 0 || $integration_exit -ne 0 ]]; then
  echo -e "Overall status:         ${RED}FAILED${NC}"
  echo "======================================"
  exit 1
fi

echo -e "Overall status:         ${GREEN}PASSED${NC}"
echo "======================================"
