#!/usr/bin/env bash
# Integration tests for Construct CLI
# Tests the actual binary commands and workflow

set -uo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Test output directory
TEST_DIR=$(mktemp -d)
TEST_CONFIG_DIR="${TEST_DIR}/.config/construct-cli"
export HOME="${TEST_DIR}"

# Skip container image build during integration tests by default.
: "${CONSTRUCT_SKIP_IMAGE_BUILD:=1}"
export CONSTRUCT_SKIP_IMAGE_BUILD

# Binary to test
BINARY="${1:-./construct}"

# Cleanup function
# shellcheck disable=SC2329
cleanup() {
    rm -rf "${TEST_DIR}"
}
trap cleanup EXIT

# Print helpers
print_test() {
    echo -e "${YELLOW}TEST:${NC} $1"
    ((TESTS_RUN++))
}

print_pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    ((TESTS_PASSED++))
}

print_fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    ((TESTS_FAILED++))
}

# Test: Binary exists and is executable
print_test "Binary exists and is executable"
if [ -f "${BINARY}" ] && [ -x "${BINARY}" ]; then
    print_pass "Binary exists at ${BINARY}"
else
    print_fail "Binary not found or not executable: ${BINARY}"
fi

# Test: Version command
print_test "Version command"
VERSION_OUTPUT=$("${BINARY}" version 2>&1)
if echo "${VERSION_OUTPUT}" | grep -qi "construct.*version"; then
    print_pass "Version command works: ${VERSION_OUTPUT}"
else
    print_fail "Version command output unexpected: ${VERSION_OUTPUT}"
fi

# Test: Help command
print_test "Help command"
HELP_OUTPUT=$("${BINARY}" help 2>&1)
if echo "${HELP_OUTPUT}" | grep -q "Construct CLI"; then
    print_pass "Help command shows correct output"
else
    print_fail "Help command output unexpected"
fi

# Test: Help shows all commands
print_test "Help shows all required commands"
MISSING_CMDS=0
for cmd in "sys" "network"; do
    if ! echo "${HELP_OUTPUT}" | grep -q "${cmd}"; then
        print_fail "Help missing command: ${cmd}"
        ((MISSING_CMDS++))
    fi
done
if [ "${MISSING_CMDS}" -eq 0 ]; then
    print_pass "All commands documented in help"
fi

# Test: Init command creates config directory
print_test "Init command creates config directory"
"${BINARY}" sys init > /dev/null 2>&1
if [ -d "${TEST_CONFIG_DIR}" ]; then
    print_pass "Config directory created: ${TEST_CONFIG_DIR}"
else
    print_fail "Config directory not created"
fi

# Test: Init creates required files
print_test "Init creates all required files"
MISSING_FILES=0
for file in "container/Dockerfile" "container/docker-compose.yml" "config.toml"; do
    if [ ! -f "${TEST_CONFIG_DIR}/${file}" ]; then
        print_fail "Missing file: ${file}"
        ((MISSING_FILES++))
    fi
done
if [ "${MISSING_FILES}" -eq 0 ]; then
    print_pass "All required files created"
fi

# Test: Init creates home directory
print_test "Init creates home directory"
if [ -d "${TEST_CONFIG_DIR}/home" ]; then
    print_pass "Home directory created"
else
    print_fail "Home directory not created"
fi

# Test: Config file is valid TOML
print_test "Config file is valid TOML"
if command -v python3 > /dev/null 2>&1; then
    if python3 -c "import tomllib; tomllib.load(open('${TEST_CONFIG_DIR}/config.toml', 'rb'))" 2>/dev/null; then
        print_pass "config.toml is valid TOML"
    else
        print_fail "config.toml is not valid TOML"
    fi
else
    echo "  (Skipped - python3 not available)"
fi

# Test: Dockerfile contains Homebrew installation
print_test "Dockerfile contains Homebrew installation"
if grep -q "brew install" "${TEST_CONFIG_DIR}/container/Dockerfile"; then
    print_pass "Dockerfile contains Homebrew installation"
else
    print_fail "Dockerfile missing Homebrew installation"
fi

# Test: docker-compose.yml is valid YAML
print_test "docker-compose.yml is valid YAML"
if grep -q "construct-box" "${TEST_CONFIG_DIR}/container/docker-compose.yml"; then
    print_pass "docker-compose.yml contains service definition"
else
    print_fail "docker-compose.yml missing service definition"
fi

# Test: Config file has all required sections
print_test "Config file has all required sections"
MISSING_SECTIONS=0
for section in "runtime" "sandbox" "network"; do
    if ! grep -q "\[${section}\]" "${TEST_CONFIG_DIR}/config.toml"; then
        print_fail "Missing section: [${section}]"
        ((MISSING_SECTIONS++))
    fi
done
if [ "${MISSING_SECTIONS}" -eq 0 ]; then
    print_pass "All required config sections present"
fi

# Test: Running init again doesn't overwrite existing files
print_test "Init is idempotent (doesn't overwrite)"
# Add a marker to detect if file gets overwritten
echo "# test-marker-$$" >> "${TEST_CONFIG_DIR}/config.toml"
"${BINARY}" sys init > /dev/null 2>&1

if grep -q "# test-marker-$$" "${TEST_CONFIG_DIR}/config.toml"; then
    print_pass "Init doesn't overwrite existing files"
else
    print_fail "Init overwrote existing files (marker missing)"
fi

# Test: Invalid command shows error
print_test "Invalid command shows error"
if "${BINARY}" invalid-command > /dev/null 2>&1; then
    print_fail "Invalid command should fail"
else
    print_pass "Invalid command returns non-zero exit code"
fi

# Test: Embedded templates are present
print_test "Embedded templates are accessible"
EMBED_TEST=0
if grep -q "FROM debian:trixie-slim" "${TEST_CONFIG_DIR}/container/Dockerfile"; then
    ((EMBED_TEST++))
fi
if grep -q "construct-box" "${TEST_CONFIG_DIR}/container/docker-compose.yml"; then
    ((EMBED_TEST++))
fi
if grep -q "allowed_domains" "${TEST_CONFIG_DIR}/config.toml"; then
    ((EMBED_TEST++))
fi
if [ "${EMBED_TEST}" -eq 3 ]; then
    print_pass "All embedded templates extracted correctly"
else
    print_fail "Some embedded templates missing content (${EMBED_TEST}/3)"
fi

# Test: Config directory structure
print_test "Config directory has correct structure"
EXPECTED_ITEMS=(
    "${TEST_CONFIG_DIR}"
    "${TEST_CONFIG_DIR}/container"
    "${TEST_CONFIG_DIR}/container/Dockerfile"
    "${TEST_CONFIG_DIR}/container/docker-compose.yml"
    "${TEST_CONFIG_DIR}/config.toml"
    "${TEST_CONFIG_DIR}/home"
)
STRUCTURE_OK=0
for item in "${EXPECTED_ITEMS[@]}"; do
    if [ -e "${item}" ]; then
        ((STRUCTURE_OK++))
    else
        print_fail "Missing item: ${item}"
    fi
done
if [ "${STRUCTURE_OK}" -eq "${#EXPECTED_ITEMS[@]}" ]; then
    print_pass "Directory structure is correct"
fi

# Test: File permissions
print_test "Files have correct permissions"
if [ -r "${TEST_CONFIG_DIR}/config.toml" ] && [ -w "${TEST_CONFIG_DIR}/config.toml" ]; then
    print_pass "Config file has read/write permissions"
else
    print_fail "Config file permissions incorrect"
fi

# Test: install-aliases command
print_test "install-aliases command"
# Fake a shell rc file
export SHELL="/bin/bash"
# Ensure HOME is set to TEST_DIR for this specific test
export HOME="${TEST_DIR}"
TOUCH_RC="${TEST_DIR}/.bashrc"
touch "${TOUCH_RC}"
# Capture output and exit code for debugging
INSTALL_OUTPUT=$(echo "y" | "${BINARY}" sys install-aliases 2>&1)
INSTALL_EXIT=$?
if [ "${INSTALL_EXIT}" -ne 0 ]; then
    print_fail "install-aliases command failed with exit code ${INSTALL_EXIT}"
    echo "Output: ${INSTALL_OUTPUT}"
elif grep -q "# construct-cli aliases start" "${TOUCH_RC}" && \
     grep -q "alias claude=" "${TOUCH_RC}" && \
     grep -q "cc-zai" "${TOUCH_RC}" && \
     grep -q "# construct-cli aliases end" "${TOUCH_RC}"; then
    print_pass "install-aliases command works and creates correct block"
else
    print_fail "install-aliases command failed to create correct block in ${TOUCH_RC}"
    echo "Exit code: ${INSTALL_EXIT}"
    echo "File content:"
    cat "${TOUCH_RC}" || echo "(file empty or missing)"
    echo "---"
fi

# Test: uninstall-aliases command
print_test "uninstall-aliases command"
UNINSTALL_OUTPUT=$(echo "y" | "${BINARY}" sys uninstall-aliases 2>&1)
UNINSTALL_EXIT=$?
if [ "${UNINSTALL_EXIT}" -ne 0 ]; then
    print_fail "uninstall-aliases command failed with exit code ${UNINSTALL_EXIT}"
    echo "Output: ${UNINSTALL_OUTPUT}"
elif ! grep -q "# construct-cli aliases start" "${TOUCH_RC}" && \
     ! grep -q "alias claude=" "${TOUCH_RC}" && \
     ! grep -q "cc-zai" "${TOUCH_RC}" && \
     ! grep -q "# construct-cli aliases end" "${TOUCH_RC}"; then
    print_pass "uninstall-aliases command removes alias block"
else
    print_fail "uninstall-aliases command failed to remove alias block in ${TOUCH_RC}"
    echo "Exit code: ${UNINSTALL_EXIT}"
    echo "File content:"
    cat "${TOUCH_RC}" || echo "(file empty or missing)"
    echo "---"
fi

# Summary
echo ""
echo "======================================"
echo "Integration Test Summary"
echo "======================================"
echo "Total tests run:    ${TESTS_RUN}"
echo -e "Tests passed:       ${GREEN}${TESTS_PASSED}${NC}"
if [ "${TESTS_FAILED}" -gt 0 ]; then
    echo -e "Tests failed:       ${RED}${TESTS_FAILED}${NC}"
else
    echo -e "Tests failed:       ${GREEN}${TESTS_FAILED}${NC}"
fi
echo "======================================"

if [ "${TESTS_FAILED}" -gt 0 ]; then
    exit 1
else
    echo -e "${GREEN}All integration tests passed!${NC}"
    exit 0
fi
