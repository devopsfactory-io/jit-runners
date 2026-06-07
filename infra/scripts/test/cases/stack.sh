# shellcheck shell=bash
# Sourced by run.sh; pass/fail/assert_* are defined there.
test_stack_default_ami_is_kept_even_if_old() {
  export FAKE_DEFAULT_AMI="ami-live"   # older than newest-2, but live
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --stack-name jit-runners --apply >/dev/null 2>&1
  assert_no_call "deregister-image --image-id ami-live" "stack: live DefaultAMI ami-live is preserved"
  assert_call    "deregister-image --image-id ami-old1" "stack: still prunes ami-old1"
  assert_call    "describe-stacks --stack-name jit-runners" "stack: resolves DefaultAMI"
}

test_stack_lookup_none_falls_back_to_topN() {
  export FAKE_DEFAULT_AMI="None"
  "${SCRIPT}" --regions us-east-2 --keep-latest 2 --stack-name jit-runners --apply >/dev/null 2>&1
  rc=$?
  if [ "${rc}" -eq 0 ]; then pass "stack: None DefaultAMI → falls back to top-N, no error"; else fail "stack: None should not fail (rc=${rc})"; fi
  assert_call "deregister-image --image-id ami-live" "stack: ami-live pruned when not the DefaultAMI"
}
