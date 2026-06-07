# AMI prune — required IAM additions (build role)

The AMI build OIDC role referenced by the `AMI_BUILD_ROLE_ARN` repo secret is provisioned
out-of-band (not in this repo). Before the `ami-build.yml` guard/prune jobs can run, attach
the following statement to that role. The role already has the Packer + copy permissions
(`ec2:DescribeImages`, `ec2:CopyImage`, `ec2:ModifyImageAttribute`,
`ec2:DisableImageBlockPublicAccess`, register/snapshot-create).

```json
{
  "Sid": "AmiPrune",
  "Effect": "Allow",
  "Action": [
    "ec2:DeregisterImage",
    "ec2:DeleteSnapshot",
    "cloudformation:DescribeStacks",
    "servicequotas:GetServiceQuota"
  ],
  "Resource": "*"
}
```

`ec2:*` actions do not support resource-level scoping for deregister/delete-snapshot in a way
that helps here; `Resource: "*"` is standard for image-lifecycle automation. Scope by region
with a `Condition` on `aws:RequestedRegion` (`us-east-1`, `us-east-2`) if your account policy
requires it.

## Verify after attaching

```bash
ROLE_NAME=<the build role name>
aws iam simulate-principal-policy \
  --policy-source-arn "arn:aws:iam::767000629676:role/${ROLE_NAME}" \
  --action-names ec2:DeregisterImage ec2:DeleteSnapshot \
                 cloudformation:DescribeStacks servicequotas:GetServiceQuota \
  --query 'EvaluationResults[].{Action:EvalActionName,Decision:EvalDecision}' --output table
```

Every row must show `allowed`.
