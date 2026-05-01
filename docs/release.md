# Release Procedure

This document captures the manual procedure for releasing a new version of
`jit-runners` and rolling the production CloudFormation stack onto the new
Lambda code. It mirrors the steps used for the v0.3.0 release.

> **Scope:** stack `jit-runners` in account `767000629676`, region `us-east-2`,
> bucket `jit-runners-lambda-s3`, three functions
> (`jit-runners-{webhook,scaleup,scaledown}`).

> **Out of scope:** Terraform deployment (see `docs/getting-started-terraform.md`)
> and the AMI build (see `infra/packer/`).

## Prerequisites

- Clean working tree on `main` with the desired changes merged.
- AWS credentials for account `767000629676`.
- `gh` and `aws` CLIs authenticated.
- DCO sign-off configured (`git config user.name` / `user.email`).

## 1. Pre-flight

Capture a baseline so you can prove the rollout actually changed code:

```bash
aws sts get-caller-identity                 # must show 767000629676
aws cloudformation describe-stacks \
  --stack-name jit-runners --region us-east-2 \
  --query 'Stacks[0].StackStatus'           # must be UPDATE_COMPLETE / CREATE_COMPLETE

for fn in jit-runners-webhook jit-runners-scaleup jit-runners-scaledown; do
  aws lambda get-function --function-name "$fn" --region us-east-2 \
    --query 'Configuration.CodeSha256' --output text
done
```

## 2. Tag and let GoReleaser publish

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
gh run watch --repo devopsfactory-io/jit-runners
gh release view vX.Y.Z --repo devopsfactory-io/jit-runners
```

## 3. Fetch and rename release assets

GoReleaser names the zips `<fn>-linux-amd64.zip`, but the live stack expects
`<fn>.zip`. Rename locally before uploading.

```bash
mkdir -p /tmp/jit-runners-vX.Y.Z
gh release download vX.Y.Z \
  --repo devopsfactory-io/jit-runners \
  --pattern '*-linux-amd64.zip' \
  --dir /tmp/jit-runners-vX.Y.Z

cd /tmp/jit-runners-vX.Y.Z
mv webhook-linux-amd64.zip   webhook.zip
mv scaleup-linux-amd64.zip   scaleup.zip
mv scaledown-linux-amd64.zip scaledown.zip
```

## 4. Upload to S3

```bash
for f in webhook scaleup scaledown; do
  aws s3 cp "/tmp/jit-runners-vX.Y.Z/${f}.zip" \
    "s3://jit-runners-lambda-s3/vX.Y.Z/${f}.zip"
done
```

## 5. Update the stack

```bash
aws cloudformation update-stack \
  --stack-name jit-runners --region us-east-2 \
  --use-previous-template \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameters \
    ParameterKey=WebhookLambdaS3Key,ParameterValue=vX.Y.Z/webhook.zip \
    ParameterKey=ScaleUpLambdaS3Key,ParameterValue=vX.Y.Z/scaleup.zip \
    ParameterKey=ScaleDownLambdaS3Key,ParameterValue=vX.Y.Z/scaledown.zip \
    ParameterKey=GitHubAppId,UsePreviousValue=true \
    ParameterKey=LambdaS3Bucket,UsePreviousValue=true \
    ParameterKey=WebhookSecretArn,UsePreviousValue=true \
    ParameterKey=PrivateKeySecretArn,UsePreviousValue=true \
    ParameterKey=VpcId,UsePreviousValue=true \
    ParameterKey=SubnetIds,UsePreviousValue=true \
    ParameterKey=DefaultAMI,UsePreviousValue=true \
    ParameterKey=LabelMappings,UsePreviousValue=true \
    ParameterKey=StaleThresholdMinutes,UsePreviousValue=true \
    ParameterKey=MaxRunnerAgeMinutes,UsePreviousValue=true

aws cloudformation wait stack-update-complete \
  --stack-name jit-runners --region us-east-2
```

The live stack reports `Capabilities: ["CAPABILITY_NAMED_IAM"]`, so the
`--capabilities` flag is required. Drop it only if a future template removes
named-IAM resources.

## 6. Verify

Each Lambda's `CodeSha256` must differ from the pre-flight capture:

```bash
for fn in jit-runners-webhook jit-runners-scaleup jit-runners-scaledown; do
  aws lambda get-function --function-name "$fn" --region us-east-2 \
    --query 'Configuration.CodeSha256' --output text
done
```

Then trigger an end-to-end check by opening any PR — the `labeler` and `test`
workflows route through `[self-hosted, medium]` / `[self-hosted, large]`, so a
green CI run proves the new lambdas dispatch real runners.

## 7. Rollback

If anything misbehaves, re-run `update-stack` with the previous version's keys
and let CloudFormation roll the Lambda code back. `vPREV` is whatever the
previous successful release was (currently `v0.2.0`).

```bash
aws cloudformation update-stack \
  --stack-name jit-runners --region us-east-2 \
  --use-previous-template \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameters \
    ParameterKey=WebhookLambdaS3Key,ParameterValue=vPREV/webhook.zip \
    ParameterKey=ScaleUpLambdaS3Key,ParameterValue=vPREV/scaleup.zip \
    ParameterKey=ScaleDownLambdaS3Key,ParameterValue=vPREV/scaledown.zip \
    ParameterKey=GitHubAppId,UsePreviousValue=true \
    ParameterKey=LambdaS3Bucket,UsePreviousValue=true \
    ParameterKey=WebhookSecretArn,UsePreviousValue=true \
    ParameterKey=PrivateKeySecretArn,UsePreviousValue=true \
    ParameterKey=VpcId,UsePreviousValue=true \
    ParameterKey=SubnetIds,UsePreviousValue=true \
    ParameterKey=DefaultAMI,UsePreviousValue=true \
    ParameterKey=LabelMappings,UsePreviousValue=true \
    ParameterKey=StaleThresholdMinutes,UsePreviousValue=true \
    ParameterKey=MaxRunnerAgeMinutes,UsePreviousValue=true
```

The previous-version objects must still exist in S3 for rollback to work — do
**not** delete old version prefixes from `jit-runners-lambda-s3`.

## Notes / known gaps

- The S3 key convention used by the live stack is `vX.Y.Z/<fn>.zip` (no
  `jit-runners/` prefix). Older docs (`docs/getting-started-cloudformation.md`,
  `docs/getting-started-terraform.md`) say `jit-runners/${VERSION}/<fn>.zip` —
  this drift is tracked separately and not corrected here.
- This procedure is currently manual. A future enhancement is to extend
  `release.yml` with an OIDC-authenticated job that uploads the zips and runs
  `update-stack` after the GitHub release is published.
- The AMI build pipeline (`ami-build.yml`) auto-triggers on the same tag push
  but is independent — its success or failure does not affect the Lambda
  release. Track AMI build issues separately.
