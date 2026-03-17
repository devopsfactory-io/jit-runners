# Troubleshooting

Operational troubleshooting guide for jit-runners. Covers the most common issues seen in production and their resolutions.

For background on the architecture, see the [README](../README.md). The system flow is:

```
GitHub webhook --> API Gateway --> Webhook Lambda --> SQS Queue --> Scale-Up Lambda --> EC2 Spot (JIT Runner)
EventBridge (every 5 min) --> Scale-Down Lambda --> cleanup stale instances
```

State is tracked in DynamoDB. Runners are ephemeral -- they self-terminate after the GitHub Actions job completes.

---

## Quick Health Check

Run these commands first when investigating any issue. They cover the most common failure points.

```bash
# Check scaleup Lambda for recent errors
aws logs filter-log-events \
  --log-group-name "/aws/lambda/jit-runners-scaleup" \
  --start-time "$(date -d '1 hour ago' +%s000 2>/dev/null || date -v-1H +%s000)" \
  --filter-pattern "ERROR"

# Check scaledown Lambda for recent errors
aws logs filter-log-events \
  --log-group-name "/aws/lambda/jit-runners-scaledown" \
  --start-time "$(date -d '1 hour ago' +%s000 2>/dev/null || date -v-1H +%s000)" \
  --filter-pattern "ERROR"

# Check DLQ depth (messages that failed all retries)
aws sqs get-queue-attributes \
  --queue-url "$(aws sqs get-queue-url --queue-name jit-runners-scaleup-dlq --query QueueUrl --output text)" \
  --attribute-names ApproximateNumberOfMessages

# Check for stale runners in DynamoDB
aws dynamodb scan \
  --table-name jit-runners-runners \
  --filter-expression "runner_status IN (:p, :f)" \
  --expression-attribute-values '{":p":{"S":"pending"},":f":{"S":"failed"}}' \
  --select COUNT

# Check EC2 spot vCPU quota (us-east-2)
aws service-quotas get-service-quota \
  --service-code ec2 \
  --quota-code L-34B43A08 \
  --query 'Quota.Value'

# List offline GitHub runners (replace owner/repo)
gh api repos/{owner}/{repo}/actions/runners --jq '.runners[] | select(.status == "offline") | {id, name, status}'
```

---

## 1. Zombie Runners Blocking Registration (409 Conflict)

### Symptom

The scaleup Lambda logs show `409 Already exists` errors when calling the GitHub API to generate a JIT runner configuration.

### Root Cause

Previously launched runners that failed or were terminated without deregistering from GitHub remain as "offline" entries in the GitHub org or repo runner list. When a new runner attempts to register with a name that collides with an existing offline runner, the GitHub API returns a 409 conflict.

### Diagnosis

List runners and filter for offline entries:

```bash
# Repository-level runners
gh api repos/{owner}/{repo}/actions/runners \
  --jq '.runners[] | select(.status == "offline") | {id, name, status, busy}'

# Organization-level runners (if using org-level registration)
gh api orgs/{org}/actions/runners \
  --jq '.runners[] | select(.status == "offline") | {id, name, status, busy}'
```

Cross-reference with the scaleup Lambda CloudWatch logs:

```bash
aws logs filter-log-events \
  --log-group-name "/aws/lambda/jit-runners-scaleup" \
  --start-time "$(date -d '1 hour ago' +%s000 2>/dev/null || date -v-1H +%s000)" \
  --filter-pattern "409"
```

### Resolution

Delete each offline zombie runner from GitHub:

```bash
# Delete a specific runner by ID
gh api -X DELETE repos/{owner}/{repo}/actions/runners/{runner_id}

# Bulk delete all offline runners (repository-level)
gh api repos/{owner}/{repo}/actions/runners \
  --jq '.runners[] | select(.status == "offline") | .id' \
  | xargs -I{} gh api -X DELETE repos/{owner}/{repo}/actions/runners/{}
```

### Prevention

The scaledown Lambda runs every 5 minutes and deregisters stale runners as part of its cleanup cycle. Zombie runners accumulate when:

- The scaledown Lambda was disabled, misconfigured, or failing (check its CloudWatch logs).
- The Lambda's IAM role lost permission to call the GitHub API.
- A large burst of failures occurred faster than the 5-minute cleanup interval.

Monitor the scaledown Lambda error rate and DLQ depth to catch these situations early.

---

## 2. EC2 vCPU Limit Exceeded During Burst

### Symptom

The scaleup Lambda logs show `MaxSpotInstanceCountExceeded` or `VcpuLimitExceeded` errors. Workflow jobs remain queued in GitHub Actions.

### Root Cause

A burst of concurrent `workflow_job` events requests more EC2 instances than the account's vCPU quota allows. Default quotas in most regions:

- Spot vCPUs: 32 (quota code `L-34B43A08`)
- On-demand standard vCPUs: 16 (quota code `L-1216C47A`)

A single `t3.large` instance consumes 2 vCPUs, so the default spot quota supports at most 16 concurrent runners.

### Diagnosis

Check current quota usage and limits:

```bash
# Spot vCPU quota
aws service-quotas get-service-quota \
  --service-code ec2 \
  --quota-code L-34B43A08 \
  --query 'Quota.{Name:QuotaName, Value:Value}'

# On-demand standard vCPU quota
aws service-quotas get-service-quota \
  --service-code ec2 \
  --quota-code L-1216C47A \
  --query 'Quota.{Name:QuotaName, Value:Value}'

# Currently running jit-runner instances
aws ec2 describe-instances \
  --filters "Name=tag:jit-runners,Values=true" "Name=instance-state-name,Values=running,pending" \
  --query 'Reservations[].Instances[].{Id:InstanceId, Type:InstanceType, State:State.Name, Launch:LaunchTime}'
```

Check the scaleup Lambda logs for the specific error:

```bash
aws logs filter-log-events \
  --log-group-name "/aws/lambda/jit-runners-scaleup" \
  --start-time "$(date -d '1 hour ago' +%s000 2>/dev/null || date -v-1H +%s000)" \
  --filter-pattern "VcpuLimitExceeded MaxSpotInstanceCountExceeded"
```

### Resolution

The scaleup Lambda automatically falls back from spot to on-demand when spot capacity is unavailable. If both quotas are exhausted:

1. Failed SQS messages retry up to 3 times, then land in the DLQ.
2. Check if the workflow runs are still waiting -- if so, redrive the DLQ once capacity is available (see [SQS DLQ Accumulation](#3-sqs-dead-letter-queue-dlq-accumulation)).
3. If immediate capacity is needed, manually terminate idle or stuck instances to free vCPUs.

### Prevention

Request vCPU limit increases through the AWS Service Quotas console. Recommended minimums for moderate workloads:

- Spot vCPUs: **64** (32 concurrent `t3.large` runners)
- On-demand standard vCPUs: **32** (16 concurrent fallback runners)

```bash
# Request a spot vCPU increase to 64
aws service-quotas request-service-quota-increase \
  --service-code ec2 \
  --quota-code L-34B43A08 \
  --desired-value 64

# Request an on-demand vCPU increase to 32
aws service-quotas request-service-quota-increase \
  --service-code ec2 \
  --quota-code L-1216C47A \
  --desired-value 32
```

Increases are typically approved within minutes for reasonable values.

---

## 3. SQS Dead Letter Queue (DLQ) Accumulation

### Symptom

Messages accumulate in the `jit-runners-scaleup-dlq` queue. Workflow jobs may be stuck or already timed out.

### Root Cause

Scaleup failures that exhaust the 3-retry limit on the main SQS queue cause messages to move to the DLQ. Common upstream causes:

- vCPU limits exceeded (see [issue 2](#2-ec2-vcpu-limit-exceeded-during-burst))
- 409 runner conflicts (see [issue 1](#1-zombie-runners-blocking-registration-409-conflict))
- Transient AWS API errors
- GitHub API rate limiting or outages

### Diagnosis

```bash
# Check DLQ message count
DLQ_URL=$(aws sqs get-queue-url --queue-name jit-runners-scaleup-dlq --query QueueUrl --output text)
aws sqs get-queue-attributes \
  --queue-url "$DLQ_URL" \
  --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible

# Peek at DLQ messages (does not delete them)
aws sqs receive-message \
  --queue-url "$DLQ_URL" \
  --max-number-of-messages 5 \
  --visibility-timeout 0
```

For each message, extract the workflow run ID and check if the job is still active:

```bash
gh run view {run_id} --json status --jq '.status'
```

### Resolution

**If the corresponding workflow runs have completed** (succeeded, failed, or cancelled): purge the DLQ since the messages are no longer actionable.

```bash
aws sqs purge-queue --queue-url "$DLQ_URL"
```

**If workflow runs are still active** and you have resolved the upstream issue (freed vCPU capacity, cleaned zombie runners): redrive the messages back to the main queue.

```bash
MAIN_QUEUE_ARN=$(aws sqs get-queue-attributes \
  --queue-url "$(aws sqs get-queue-url --queue-name jit-runners-scaleup --query QueueUrl --output text)" \
  --attribute-names QueueArn --query 'Attributes.QueueArn' --output text)

aws sqs start-message-move-task \
  --source-arn "$(aws sqs get-queue-attributes --queue-url "$DLQ_URL" --attribute-names QueueArn --query 'Attributes.QueueArn' --output text)" \
  --destination-arn "$MAIN_QUEUE_ARN"
```

### Prevention

Address the upstream root causes (vCPU limits, zombie runners) to prevent scaleup failures from reaching the retry limit. Set up a CloudWatch alarm on the DLQ `ApproximateNumberOfMessages` metric so accumulation is caught early:

```bash
aws cloudwatch put-metric-alarm \
  --alarm-name jit-runners-dlq-depth \
  --metric-name ApproximateNumberOfMessages \
  --namespace AWS/SQS \
  --dimensions Name=QueueName,Value=jit-runners-scaleup-dlq \
  --statistic Maximum \
  --period 300 \
  --threshold 5 \
  --comparison-operator GreaterThanThreshold \
  --evaluation-periods 1 \
  --alarm-actions "{sns_topic_arn}"
```

---

## 4. Stale DynamoDB Runner Entries

### Symptom

The DynamoDB table `jit-runners-runners` contains runner entries stuck in `pending` or `failed` status for extended periods (more than 30 minutes).

### Root Cause

Runner instances were terminated (spot reclamation, vCPU limits preventing launch, manual termination, or self-termination) but the DynamoDB state was not updated to reflect the termination. This can happen when:

- The instance was terminated before the user-data script could update DynamoDB.
- The scaleup Lambda recorded the instance but the EC2 `RunInstances` call ultimately failed.
- The scaledown Lambda has not yet run its cleanup cycle.

### Diagnosis

```bash
# Count stale entries by status
aws dynamodb scan \
  --table-name jit-runners-runners \
  --filter-expression "runner_status IN (:p, :f)" \
  --expression-attribute-values '{":p":{"S":"pending"},":f":{"S":"failed"}}' \
  --select COUNT

# List stale entries with details
aws dynamodb scan \
  --table-name jit-runners-runners \
  --filter-expression "runner_status IN (:p, :f)" \
  --expression-attribute-values '{":p":{"S":"pending"},":f":{"S":"failed"}}' \
  --projection-expression "runner_id, runner_status, instance_id, created_at"
```

Cross-reference instance IDs with EC2 to confirm they no longer exist:

```bash
aws ec2 describe-instances \
  --instance-ids {instance_id} \
  --query 'Reservations[].Instances[].State.Name'
```

### Resolution

The scaledown Lambda automatically cleans up stale entries every 5 minutes using a 30-minute staleness threshold. Under normal operation, no manual intervention is needed.

If immediate cleanup is required, invoke the scaledown Lambda manually:

```bash
aws lambda invoke \
  --function-name jit-runners-scaledown \
  --invocation-type Event \
  /dev/null
```

### Prevention

This is expected behavior during normal operation -- the scaledown Lambda handles it. If stale entries persist beyond 30 minutes, investigate the scaledown Lambda:

```bash
# Check scaledown Lambda recent invocations
aws logs filter-log-events \
  --log-group-name "/aws/lambda/jit-runners-scaledown" \
  --start-time "$(date -d '30 minutes ago' +%s000 2>/dev/null || date -v-30M +%s000)"

# Check EventBridge schedule is active
aws events describe-rule --name jit-runners-scaledown-schedule
```

---

## 5. Instance Self-Termination (Expected Behavior)

### Symptom

EC2 instances launched by jit-runners terminate quickly after launch (typically 1-5 minutes). Operators may see `TerminateInstances` events in CloudTrail and suspect a problem.

### This Is NOT a Bug

jit-runners uses ephemeral JIT runners by design. The lifecycle is:

1. Scale-up Lambda launches the EC2 instance with a user-data script.
2. The user-data script registers the runner with GitHub using the JIT config token.
3. The GitHub Actions runner agent picks up a single job and executes it.
4. After the job completes, the runner agent exits and the user-data script terminates the instance.

If a runner starts but has no queued job (e.g., the webhook was stale or the job was picked up by another runner), the runner agent exits immediately and the instance self-terminates.

### Diagnosis

If you need to confirm the termination was intentional, check CloudTrail:

```bash
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventName,AttributeValue=TerminateInstances \
  --start-time "$(date -d '1 hour ago' +%s 2>/dev/null || date -v-1H +%s)" \
  --query 'Events[].{Time:EventTime, User:Username, Resources:Resources[0].ResourceName}'
```

The `userIdentity` in the event will indicate who terminated the instance:

- **Instance itself** (via instance profile role): Normal self-termination after job completion.
- **Scale-down Lambda role**: Cleanup of a stale or orphaned instance.
- **Human IAM user/role**: Manual termination.

### When to Investigate

Only investigate if:

- Instances terminate before the runner agent starts (check user-data logs in `/var/log/cloud-init-output.log` via SSM or before the instance is gone).
- The GitHub Actions job shows as "queued" indefinitely despite instances launching (suggests the runner is failing to register).
- The termination is happening within seconds (before user-data can execute), which may indicate a spot reclamation or instance health issue.

---

## 6. AMI Version Mismatch

### Symptom

Runners launch but fail to connect to GitHub, report missing tools during job execution, or the user-data script falls back to installing dependencies from scratch (slow cold starts).

### Root Cause

The CloudFormation stack or Terraform configuration references an outdated or incorrect AMI ID. This can happen when:

- A new AMI was built but the stack parameters were not updated.
- The AMI was deregistered or is not available in the target region.
- The runner agent version in the AMI does not match the version expected by GitHub.

### Diagnosis

Check which AMI the stack is currently using:

```bash
# CloudFormation
aws cloudformation describe-stacks \
  --stack-name jit-runners \
  --query 'Stacks[0].Parameters[?ParameterKey==`DefaultAMI`].ParameterValue'

# Terraform
cd infra/terraform && tofu output default_ami
```

Verify the AMI exists and check its tags:

```bash
aws ec2 describe-images \
  --image-ids {ami_id} \
  --query 'Images[].{Id:ImageId, Name:Name, State:State, Tags:Tags}'
```

Check the latest AMI builds:

```bash
# List recent jit-runner AMIs in the region
aws ec2 describe-images \
  --owners self \
  --filters "Name=name,Values=jit-runner-*" \
  --query 'sort_by(Images, &CreationDate)[-5:].{Id:ImageId, Name:Name, Created:CreationDate}'

# Check recent AMI build workflow runs
gh run list --workflow ami-build.yml --limit 5
```

### Resolution

Update the AMI parameter to the latest build:

```bash
# CloudFormation
aws cloudformation update-stack \
  --stack-name jit-runners \
  --use-previous-template \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameters ParameterKey=DefaultAMI,ParameterValue={new_ami_id}

# Terraform
# Update the default_ami variable in terraform.tfvars, then:
cd infra/terraform && tofu plan && tofu apply
```

If no recent AMI exists, build a fresh one:

```bash
make ami.build
```

Or trigger a build from CI:

```bash
gh workflow run ami-build.yml -f distribute=true
```

### Prevention

- Update the AMI parameter promptly after each AMI build.
- Use the `ami-build.yml` workflow which prints the new AMI ID in the job summary.
- Consider automating the AMI parameter update as part of the build pipeline.
