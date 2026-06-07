# AMI prune — required IAM additions (build role)

The AMI build OIDC role referenced by the `AMI_BUILD_ROLE_ARN` repo secret is provisioned
out-of-band (not in this repo). It is the IAM role **`jit-runners-ami-build`** in account
`767000629676` (trust: GitHub OIDC for `repo:devopsfactory-io/jit-runners:*`).

Its existing inline policy `PackerAMIBuild` already grants the Packer build + copy +
deregister permissions the prune relies on: `ec2:DescribeImages`, `ec2:CopyImage`,
`ec2:ModifyImageAttribute`, `ec2:RegisterImage`, `ec2:CreateSnapshot`,
**`ec2:DeregisterImage`**, **`ec2:DeleteSnapshot`**, `ec2:DescribeSnapshots`, etc.

The prune/guard/copy steps additionally need the four actions below, which `PackerAMIBuild`
does **not** grant. Attach them as a separate inline policy `AmiPrune`:

```json
{
  "Sid": "AmiPrune",
  "Effect": "Allow",
  "Action": [
    "cloudformation:DescribeStacks",
    "servicequotas:GetServiceQuota",
    "ec2:DisableImageBlockPublicAccess",
    "ec2:EnableImageBlockPublicAccess"
  ],
  "Resource": "*"
}
```

- `cloudformation:DescribeStacks` — the post-build prune resolves the live `DefaultAMI` from
  the `jit-runners` CloudFormation stack via `--stack-name`.
- `servicequotas:GetServiceQuota` — the pre-build `--ensure-free` guard reads the Public AMIs
  quota (`L-0E3CBAB9`).
- `ec2:Disable/EnableImageBlockPublicAccess` — the explicit us-east-1 copy step disables BPA
  before publishing; the one-time region purge re-enables it.

`ec2:*` actions do not support useful resource-level scoping for these; `Resource: "*"` is
standard for image-lifecycle automation. Scope by region with a `Condition` on
`aws:RequestedRegion` (`us-east-1`, `us-east-2`) if your account policy requires it.

## Apply

```bash
aws iam put-role-policy --role-name jit-runners-ami-build --policy-name AmiPrune \
  --policy-document file://ami-prune-policy.json   # the JSON above
```

## Verify after attaching

```bash
aws iam simulate-principal-policy \
  --policy-source-arn "arn:aws:iam::767000629676:role/jit-runners-ami-build" \
  --action-names ec2:DeregisterImage ec2:DeleteSnapshot ec2:CopyImage ec2:ModifyImageAttribute \
                 ec2:DisableImageBlockPublicAccess ec2:EnableImageBlockPublicAccess \
                 cloudformation:DescribeStacks servicequotas:GetServiceQuota \
  --query 'EvaluationResults[].{Action:EvalActionName,Decision:EvalDecision}' --output table
```

Every row must show `allowed`.

> **Status:** applied to `jit-runners-ami-build` (inline policy `AmiPrune`) and verified — all
> rows `allowed`.
