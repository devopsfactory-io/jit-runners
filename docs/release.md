# Release Procedure

This document captures the manual procedure for releasing a new version of
`jit-runners` and rolling the production CloudFormation stack onto the new
Lambda code. It mirrors the steps used for the v0.3.0 release.

> **Scope:** stack `jit-runners` in account `767000629676`, region `us-east-2`,
> bucket `jit-runners-lambda-s3`, four functions
> (`jit-runners-{webhook,scaleup,scaledown,lifecycle}`).

> **Out of scope:** GCP deployment (see `docs/getting-started-gcp.md` and the
> "GCP path" section at the end of this file), the AWS Terraform path (see
> `docs/getting-started-aws.md`), and the AMI build (see `infra/packer/`).

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

for fn in jit-runners-webhook jit-runners-scaleup jit-runners-scaledown jit-runners-lifecycle; do
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
mv lifecycle-linux-amd64.zip lifecycle.zip
```

## 4. Upload to S3

```bash
for f in webhook scaleup scaledown lifecycle; do
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
    ParameterKey=LifecycleLambdaS3Key,ParameterValue=vX.Y.Z/lifecycle.zip \
    ParameterKey=MaxReEnqueueAttempts,ParameterValue=3 \
    ParameterKey=GitHubAppId,UsePreviousValue=true \
    ParameterKey=GitHubInstallationId,UsePreviousValue=true \
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
for fn in jit-runners-webhook jit-runners-scaleup jit-runners-scaledown jit-runners-lifecycle; do
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
    ParameterKey=LifecycleLambdaS3Key,ParameterValue=vPREV/lifecycle.zip \
    ParameterKey=MaxReEnqueueAttempts,UsePreviousValue=true \
    ParameterKey=GitHubAppId,UsePreviousValue=true \
    ParameterKey=GitHubInstallationId,UsePreviousValue=true \
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
  `jit-runners/` prefix). Pre-Phase-E docs used `jit-runners/${VERSION}/<fn>.zip`;
  the consolidated [`docs/getting-started-aws.md`](getting-started-aws.md)
  reflects the no-prefix convention.
- This procedure is currently manual. A future enhancement is to extend
  `release.yml` with an OIDC-authenticated job that uploads the zips and runs
  `update-stack` after the GitHub release is published.
- The AMI build pipeline (`ami-build.yml`) auto-triggers on the same tag push
  but is independent — its success or failure does not affect the Lambda
  release. Track AMI build issues separately.
- The lifecycle Lambda is new in v1.0.0-rc.1+. Releases prior to that do not
  include `lifecycle.zip` and do not need the `LifecycleLambdaS3Key` or
  `MaxReEnqueueAttempts` parameters. When rolling back to a pre-v1.0.0-rc.1
  release, omit those two parameters from the `update-stack` command.

## 8. GCP path (parallel to steps 3-7 on AWS)

The GCP rollout uses the `infra/terraform-gcp/` Terraform module deployed against a GCP project, NOT the same CloudFormation stack. AWS and GCP deployments are independent — a release applies to whichever clouds the operator chooses to upgrade.

### Pre-flight

```bash
gcloud auth application-default login          # if not already done
gcloud config set project <my-jit-runners-project>
```

### Deploy

The GCP module fetches function source zips declaratively from the GitHub Release matching `var.release_tag`. There is no manual `gsutil cp` step.

```bash
cd infra/terraform-gcp
# Edit terraform.tfvars: bump release_tag to vX.Y.Z
tofu plan
tofu apply
```

The first apply with a new `release_tag` triggers Cloud Build to rebuild the 5 Cloud Run function images (~5-10 min total). Subsequent applies with the same tag reuse the build.

### Verify

```bash
for fn in jit-runners-webhook jit-runners-scaleup jit-runners-scaledown jit-runners-lifecycle jit-runners-rebalancer; do
  gcloud beta run services describe "${fn}" \
    --region=us-central1 \
    --format='value(metadata.annotations."run.googleapis.com/lastModifier",spec.template.metadata.labels."cloud.googleapis.com/revision-name")'
done
```

Each function should show a fresh revision-name post-apply. The rebalancer should fire every minute and emit `cycle complete repo=<owner/repo> demand=N supply=M published=K` in Cloud Logging.

### Rollback

If the new release misbehaves, edit `terraform.tfvars` to revert `release_tag` to the previous version and re-run `tofu apply`. The previous release's function zips are still in the GitHub Release, so the fetch chain re-downloads them. Cloud Build rebuilds the images from those zips.

```bash
# In terraform.tfvars: release_tag = "vPREV"
tofu apply
```

### AWS + GCP combined release

When releasing to BOTH clouds in the same cycle:

1. Push the version tag (steps 1-2 above) once.
2. Watch GoReleaser publish the release.
3. Run the AWS rollout (steps 3-6 above) to update the AWS CloudFormation stack.
4. Run the GCP rollout (this section) to update the GCP Terraform deployment.
5. Verify each cloud independently.

The two clouds can be at different `release_tag` versions during a phased rollout. There's no global ordering constraint between them.
