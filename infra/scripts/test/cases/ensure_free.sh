# shellcheck shell=bash
test_ensure_free_noop_below_threshold() {
  # 5 images; raise quota so max_allowed (q-1) >= count -> no-op
  export FAKE_QUOTA="10.0"
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --ensure-free 1 --apply >/dev/null 2>&1
  assert_no_call "deregister-image" "ensure-free: no-op when count < quota-K"
}

test_ensure_free_frees_exactly_one_at_ceiling() {
  export FAKE_QUOTA="5.0"   # 5 images, max_allowed=4, need=1
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --ensure-free 1 --apply >/dev/null 2>&1
  n="$(grep -c 'deregister-image' "${FAKE_AWS_CALLS}")"
  if [ "${n}" -eq 1 ]; then pass "ensure-free: frees exactly 1"; else fail "ensure-free: expected 1 deregister, got ${n}"; fi
  assert_call    "deregister-image --image-id ami-old2" "ensure-free: frees the oldest (ami-old2)"
  assert_no_call "deregister-image --image-id ami-old1" "ensure-free: leaves ami-old1"
}

test_quota_override_used() {
  export FAKE_QUOTA="999.0"  # would make it a no-op if used...
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --ensure-free 1 --quota 5 --apply >/dev/null 2>&1
  assert_call    "deregister-image --image-id ami-old2" "ensure-free: --quota override beats service-quotas"
  assert_no_call "service-quotas get-service-quota" "ensure-free: --quota skips the lookup"
}
