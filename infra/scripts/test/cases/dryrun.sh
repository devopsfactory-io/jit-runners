test_dryrun_keeps_top2_and_makes_no_mutations() {
  out="$("${SCRIPT}" --regions us-east-2 --keep-latest 2 2>&1)"
  # would delete the 3 oldest (ami-live, ami-old1, ami-old2), keep newest 2
  echo "$out" | grep -q "ami-old1" || fail "dryrun: ami-old1 not listed for deletion"
  echo "$out" | grep -q "ami-new1" && fail "dryrun: ami-new1 (kept) wrongly listed" || true
  assert_no_call "deregister-image" "dryrun: no deregister without --apply"
  assert_no_call "delete-snapshot"  "dryrun: no delete-snapshot without --apply"
  assert_call "ec2 describe-images" "dryrun: lists images"
  assert_call "Values=jit-runner*"  "dryrun: applies name-prefix filter"
}
