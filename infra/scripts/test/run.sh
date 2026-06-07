#!/usr/bin/env bash
# Test runner for ami-prune.sh. Puts fake-aws on PATH and asserts on recorded calls / output.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC2034  # used by sourced test cases
SCRIPT="${HERE}/../ami-prune.sh"
FIXTURES="${HERE}/fixtures"
FAILED=0

# Each test runs in a temp dir with its own calls log.
setup() {
  WORK="$(mktemp -d)"
  export FAKE_AWS_CALLS="${WORK}/calls.log"
  : >"${FAKE_AWS_CALLS}"
  export PATH="${HERE}:${PATH}"   # fake-aws shadows real aws
  export FAKE_DESCRIBE_IMAGES="${FIXTURES}/describe-images-5.json"
  export FAKE_DEFAULT_AMI="None"
  export FAKE_QUOTA="5.0"
  export FAKE_SNAPSHOT_INUSE="0"
}
teardown() { rm -rf "${WORK}"; }

# shellcheck disable=SC2329  # invoked indirectly by sourced test cases
pass() { echo "ok   - $1"; }
# shellcheck disable=SC2329  # invoked indirectly by sourced test cases
fail() { echo "FAIL - $1"; FAILED=1; }

# assert_no_call <needle>  -> fails if needle appears in calls log
# shellcheck disable=SC2329  # invoked indirectly by sourced test cases
assert_no_call() { if grep -q -- "$1" "${FAKE_AWS_CALLS}"; then fail "$2 (unexpected call: $1)"; else pass "$2"; fi; }
# assert_call <needle>     -> fails if needle absent from calls log
# shellcheck disable=SC2329  # invoked indirectly by sourced test cases
assert_call() { if grep -q -- "$1" "${FAKE_AWS_CALLS}"; then pass "$2"; else fail "$2 (missing call: $1)"; fi; }

# Source test files (added by later tasks)
# shellcheck source=/dev/null
for t in "${HERE}"/cases/*.sh; do [ -e "$t" ] && source "$t"; done

run_all() {
  for fn in $(declare -F | awk '{print $3}' | grep '^test_'); do
    setup; "$fn"; teardown
  done
  exit "${FAILED}"
}
run_all
