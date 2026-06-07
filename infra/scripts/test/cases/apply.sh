# shellcheck shell=bash
# Sourced by run.sh; pass/fail/assert_* are defined there.
test_apply_deregisters_all_but_top2() {
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --apply >/dev/null 2>&1
  assert_call    "deregister-image --image-id ami-old1" "apply: deregisters ami-old1"
  assert_call    "deregister-image --image-id ami-live" "apply: deregisters ami-live (no stack)"
  assert_no_call "deregister-image --image-id ami-new1" "apply: keeps ami-new1"
  assert_no_call "deregister-image --image-id ami-new2" "apply: keeps ami-new2"
  assert_call    "delete-snapshot --snapshot-id snap-old1" "apply: deletes snap-old1"
}

test_apply_snapshot_inuse_is_nonfatal() {
  export FAKE_SNAPSHOT_INUSE=1
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --apply >/dev/null 2>&1
  rc=$?
  if [ "${rc}" -eq 0 ]; then pass "apply: InvalidSnapshot.InUse does not fail the run"; else fail "apply: InUse should be non-fatal (rc=${rc})"; fi
}
