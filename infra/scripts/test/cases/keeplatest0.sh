# shellcheck shell=bash
# Sourced by run.sh; pass/fail/assert_* are defined there.
test_keep_latest_zero_prunes_all_in_apply() {
  "${SCRIPT}" --regions us-west-1 --keep-latest 0 --apply >/dev/null 2>&1
  rc=$?
  if [ "${rc}" -eq 0 ]; then pass "keep0: runs cleanly"; else fail "keep0: nonzero rc=${rc}"; fi
  assert_call    "deregister-image --image-id ami-new1" "keep0: prunes newest too"
  assert_call    "deregister-image --image-id ami-old2" "keep0: prunes oldest"
}
test_nonnumeric_keep_latest_rejected() {
  "${SCRIPT}" --regions us-west-1 --keep-latest foo >/dev/null 2>&1
  rc=$?
  if [ "${rc}" -eq 2 ]; then pass "validation: non-numeric --keep-latest exits 2"; else fail "validation: expected exit 2, got ${rc}"; fi
}
