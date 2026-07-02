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
gh workflow run ami-build.yml
```

### Prevention

- Update the AMI parameter promptly after each AMI build.
- Use the `ami-build.yml` workflow which prints the new AMI ID in the job summary.
- Consider automating the AMI parameter update as part of the build pipeline.

---

## 7. Re-enqueue and DLQ inspection

### Symptom: jobs stuck in GitHub's queued state for > StaleThresholdMinutes

The lifecycle handler updates DynamoDB on `in_progress` and `completed` events. If GitHub's
scheduler hiccups and never delivers an `in_progress` event for a queued job,
scaledown reaps the stale-pending record after `StaleThresholdMinutes` (default
10), terminates the orphaned spot instance, deregisters the GitHub runner
registration, and re-publishes the original `ScaleUpMessage` with
`re_enqueue_attempts` incremented. After `MaxReEnqueueAttempts` (default 3), the
message is left as terminal `failed` and an ERROR log line is emitted.

### Inspection commands

```bash
# Lifecycle queue depth
aws sqs get-queue-attributes \
  --queue-url $(aws cloudformation describe-stack-resources \
    --stack-name jit-runners --region us-east-2 \
    --query 'StackResources[?LogicalResourceId==`LifecycleQueue`].PhysicalResourceId | [0]' \
    --output text) \
  --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible \
  --region us-east-2

# Lifecycle DLQ contents
aws sqs receive-message \
  --queue-url $(aws cloudformation describe-stack-resources \
    --stack-name jit-runners --region us-east-2 \
    --query 'StackResources[?LogicalResourceId==`LifecycleQueueDLQ`].PhysicalResourceId | [0]' \
    --output text) \
  --max-number-of-messages 10 \
  --region us-east-2

# Recent re-enqueue exhaustion log lines
aws logs filter-log-events \
  --log-group-name /aws/lambda/jit-runners-scaledown \
  --filter-pattern '"re-enqueue exhausted"' \
  --region us-east-2 --max-items 20
```

### Manual reset procedure

If a job is in DynamoDB with `Status=failed` after re-enqueue exhaustion AND the
GitHub job is still `queued`, you can manually re-enqueue by sending a fresh
`ScaleUpMessage` with `re_enqueue_attempts` reset to 0:

```bash
aws sqs send-message \
  --queue-url $(aws cloudformation describe-stack-resources \
    --stack-name jit-runners --region us-east-2 \
    --query 'StackResources[?LogicalResourceId==`ScaleUpQueue`].PhysicalResourceId | [0]' \
    --output text) \
  --message-body '{"job_id":<JOB_ID>,"repo":"owner/repo","labels":["self-hosted","large"],"re_enqueue_attempts":0}' \
  --region us-east-2
```

Under the runner_id identity model (issue #52), each scaleup invocation registers a fresh JIT runner with a unique `runner_id` and launches a new EC2 instance — there is no `(repo, job_id)`-keyed idempotency check that could short-circuit a manual re-enqueue. If the prior runner is still alive when you re-enqueue, scaledown's `Scan`-based stale-runner sweep reaps the abandoned instance after `StaleThresholdMinutes`. This is generally what you want for a manual reset.

## How runners bind to jobs

This subsection documents what GitHub actually guarantees about JIT runner-to-job binding, so future maintainers do not re-make the assumption that produced issue #52.

### What GitHub guarantees

The endpoint `POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig` accepts only:

- `name` — a cosmetic identifier (any string, no semantic binding).
- `runner_group_id` — group membership.
- `labels` — array of labels the runner advertises.
- `work_folder` — optional working directory hint for the runner agent.

There is **no** `job_id` parameter and **no** `workflow_run_id` parameter. The endpoint guarantees: *"when a runner registers using this API it will only be allowed to run a single job before being automatically removed from the repository, organization, or enterprise."*

That single job is **whichever queued job whose labels match first** — not job-locked, not workflow_run-locked.

### What this means for jit-runners

- Each `workflow_job=queued` event triggers one `generate-jitconfig` call and one EC2 spot launch. GitHub returns a unique int64 `runner_id` per call.
- DynamoDB records are keyed on the stringified `runner_id` (the only stable identifier post-registration). `job_id` and `workflow_run_id` are stored as observability metadata only.
- When a runner comes online, GitHub assigns it one of the queued matching jobs **non-deterministically**. The runner whose name happens to encode `job_id=A` may execute `job_id=B`. Total job count drains, but pairing is set-level, not name-level.
- Lifecycle webhooks (`workflow_job=in_progress`, `workflow_job=completed`) carry the `runner_id` of the runner that actually executed the job. Because we key DDB on `runner_id`, every webhook resolves its own record exactly once.

### Why the runner name is `jit-<uuidv4>`

The previous form `jit-<job_id>` implied a binding GitHub does not enforce. Future readers must not assume the trailing component of a JIT runner name is meaningful. A UUID is opaque by construction.

### References

- [REST API: self-hosted runners](https://docs.github.com/en/rest/actions/self-hosted-runners) — `generate-jitconfig` request body.
- [GitHub Changelog: Just-in-time self-hosted runners (2023-06-02)](https://github.blog/changelog/2023-06-02-github-actions-just-in-time-self-hosted-runners/).
- Issue [devopsfactory-io/jit-runners#52](https://github.com/devopsfactory-io/jit-runners/issues/52) and the design spec at `repositories/zettelkasten/Projects/jit-runners/specs/2026-05-02-runner-id-realignment-design.md`.

## Debugging silent runner failures

A "silent runner failure" is the case where an EC2 spot instance launches, the runner agent exits within seconds, and the instance terminates without ever picking up its assigned job. The first symptom is usually a CI job stuck in `queued` for minutes despite `scaleup` reporting a successful launch.

### Quick check: did silent failures fire?

```bash
aws cloudwatch get-metric-statistics \
  --namespace JitRunners/RunnerAgent \
  --metric-name SilentFailures \
  --start-time $(date -u -v-1H +%FT%TZ) \
  --end-time $(date -u +%FT%TZ) \
  --period 300 \
  --statistics Sum
```

A non-zero `Sum` means at least one runner emitted `JIT_NO_JOB_PICKUP` in the past hour.

### Find the affected runner_id

```bash
aws logs filter-log-events \
  --log-group-name /jit-runners/userdata \
  --filter-pattern '"JIT_NO_JOB_PICKUP"' \
  --start-time $(($(date +%s) - 3600))000 \
  --query 'events[].{ts:timestamp,msg:message}' \
  --output table
```

Each match contains the `runner_id` and the `runtime` in seconds.

### Inspect the runner-agent diag logs

The CloudWatch agent ships `_diag/Runner_*.log` and `_diag/Worker_*.log` to `/jit-runners/runner-agent`, stream `<runner_id>/<instance_id>`:

```bash
aws logs tail /jit-runners/runner-agent --since 1h --filter-pattern '"<runner_id>"'
```

Or open the AWS Console: CloudWatch → Log groups → `/jit-runners/runner-agent` → log stream prefixed `<runner_id>/`.

### Enabling runner-agent debug logs

The runner agent (`actions/runner`) reads `ACTIONS_RUNNER_DEBUG` and `ACTIONS_STEP_DEBUG` from the GitHub repo/org **secrets or variables** at job-pickup time, NOT from the runner instance's process environment. As a result, setting these env vars in the EC2 userdata or via SSM has no effect on the runner agent's log level.

To get debug-level logs from a JIT runner, use one of:

#### Option A — Workflow-level env injection (recommended, no infra)

Add to the workflow YAML:

```yaml
jobs:
  my-job:
    runs-on: [self-hosted, large]
    env:
      ACTIONS_RUNNER_DEBUG: "true"
      ACTIONS_STEP_DEBUG: "true"
    steps:
      - run: ...
```

The runner agent picks these up from the job context at job start.

#### Option B — Repository-level secret/variable

In the GitHub UI: **Settings → Secrets and variables → Actions → Variables**, add `ACTIONS_RUNNER_DEBUG=true` and `ACTIONS_STEP_DEBUG=true`. Effective for ALL workflow runs in the repo. Remove when done — these are noisy.

The CloudWatch agent in the AMI continues to forward `_diag/*.log` regardless of the runner's log level, so option A's debug output reaches your operator dashboard the same way INFO output does.

**See issue [#61](https://github.com/devopsfactory-io/jit-runners/issues/61) for the historical context.** The previous SSM-based toggle (`/jit-runners/runner-log-level`) was removed in PR #46 because the underlying mechanism never worked.

### Reproducing a silent failure for testing

To exercise the silent-failure path on demand without a real workload:

1. Generate a JIT config: `gh api -X POST repos/<owner>/<repo>/actions/runners/generate-jitconfig -f name=test -f labels[]=self-hosted -F runner_group_id=1`. Capture the `runner.id`.
2. Immediately revoke it: `gh api -X DELETE repos/<owner>/<repo>/actions/runners/<runner.id>`.
3. Send a synthetic SQS message to the scaleup queue carrying the revoked `encoded_jit_config`. A new spot instance launches, registers nothing, exits, and emits `JIT_NO_JOB_PICKUP`.

## Stranded queued jobs

A "stranded queued job" is a workflow_job stuck in `queued` status indefinitely. Pre-issue #62, this happened because GitHub's matcher pairs runners with any queued matching job (often older stranded ones, not the specific job whose `queued` event triggered the runner's launch). The rebalancer Lambda closes this gap by periodically re-publishing `ScaleUpMessage`s for any queue depth not covered by pending runners.

The rebalancer iterates over **repos with recent activity**: it scans the DynamoDB runner records and selects every repo whose latest record was launched in the past 7 days. A repo with no recent record cannot have stranded queued jobs in our system (the drift cycle requires that scaleup already attempted to launch, which writes a record). This bounds per-cycle GitHub API calls to ~1 per active repo, keeping us well under the installation rate limit even for orgs with hundreds of repos.

### Quick check: is the rebalancer healthy?

```bash
aws logs tail /aws/lambda/jit-runners-rebalancer --since 10m --filter-pattern '"cycle complete"' --format short
```

Expected: per repo, a `cycle complete repo=<owner/repo> demand=N supply=M published=K label_sets=L` line, plus a single `tick complete repos=R errors=E` summary line, every minute. `published=K` should be 0 most of the time per repo and non-zero only when there's actual drift to recover. `errors=E` should normally be 0; non-zero means one or more repos hit a transient GitHub outage and the next tick will retry.

### Find currently stranded jobs (operator triage)

```bash
gh api repos/devopsfactory-io/jit-runners/actions/runs?status=queued --jq '.workflow_runs[] | "\(.id)  \(.name)  \(.created_at)"'
```

For each queued run, list its queued jobs:

```bash
gh api repos/devopsfactory-io/jit-runners/actions/runs/<RUN_ID>/jobs --jq '.jobs[] | select(.status == "queued") | "  \(.id)  \(.name)  labels=\(.labels)"'
```

If the rebalancer is healthy and there are still stranded jobs older than ~5 minutes, escalate.

### Verify scaleup is making demand-aware decisions

```bash
aws logs tail /aws/lambda/jit-runners-scaleup --since 10m --filter-pattern '"scaleup: skip"' --format short
```

A small number of skips per CI cycle is normal — they mean the webhook path correctly decided not to over-launch. A large number (every webhook skipping) suggests supply is already saturated; check whether legitimate demand is being met.

### Manually fire a rebalance

```bash
aws lambda invoke --function-name jit-runners-rebalancer --invocation-type RequestResponse /tmp/rebalancer-out.json
cat /tmp/rebalancer-out.json
```

Useful when investigating stranded jobs and you don't want to wait for the next 1-minute cycle.

One invocation rebalances every repo the App installation can access, not just one.

### Tuning the cadence

If drift recovery feels too slow, the cadence can be tightened (subject to AWS::Events::Rule's `rate(1 minute)` minimum). Going below 1 minute requires `AWS::Scheduler::Schedule` (newer EventBridge Scheduler service); see issue #62 for the trade-off discussion.

### References

- Issue [#62](https://github.com/devopsfactory-io/jit-runners/issues/62) — diagnosis and design.
- Spec: `repositories/zettelkasten/Projects/jit-runners/specs/2026-05-02-effective-scaleup-design.md`.

## GCP-specific issues

The following symptoms are GCP-specific. AWS-only operators can skip this section.

### Cloud Build stuck in BUILDING state

#### Symptom

After `tofu apply`, one or more Cloud Run functions show `LastUpdateStatus: Pending` or never reach `Ready`. Cloud Build is rebuilding the function image and either hanging or failing.

#### Diagnosis

```bash
gcloud builds list --filter="source.storageSource.bucket~jit-runners-functions" --limit=5
gcloud builds log <build-id>
```

#### Common causes

- **First-apply timing**: Buildpacks build can take 5-10 minutes per function on first deploy. Wait it out before declaring failure.
- **Insufficient quota**: Cloud Build has per-project concurrency limits. Check quotas at `https://console.cloud.google.com/iam-admin/quotas` filtered to `cloudbuild.googleapis.com`.
- **Bad Go module fetch**: Cloud Build cannot reach the Go module proxy from the build VM if egress is heavily restricted. Default Cloud Build VPC egress works fine; investigate only if you've customized.

#### Resolution

If a build is genuinely hung (>20 min), cancel it and re-trigger:

```bash
gcloud builds cancel <build-id>
cd infra/terraform-gcp
tofu apply -replace='google_cloudfunctions2_function.<func>'
```

### Eventarc trigger not delivering messages

#### Symptom

Pub/Sub messages publish to `${prefix}-jobs` or `${prefix}-lifecycle` topics, but the corresponding scaleup/lifecycle Cloud Run functions never invoke. Per-topic message backlog grows.

#### Diagnosis

```bash
# Check trigger health
gcloud eventarc triggers describe ${prefix}-scaleup --location=us-central1
gcloud eventarc triggers describe ${prefix}-lifecycle --location=us-central1

# Check the bound subscription's backlog
gcloud pubsub subscriptions describe ${prefix}-jobs-scaleup \
  --format='value(numUndeliveredMessages,deliveryAttempts)'
```

#### Common causes

- **Eventarc bound to a different subscription** (D13 verification gap). The trigger may be using its own auto-created managed subscription, not the explicit one declared in `pubsub.tf`. The DLQ + retry policy on the explicit sub doesn't apply.
- **Eventarc invoker SA missing `roles/run.invoker`**: usually self-correcting since `eventarc.tf` declares the binding. Verify with:

  ```bash
  gcloud run services get-iam-policy ${prefix}-scaleup --region=us-central1
  ```

#### Resolution

If Eventarc is using its own managed subscription, the workaround is to inspect failed messages via the inspector subscription on the topic and accept that the explicit DLQ + retry policy isn't on the active path:

```bash
gcloud pubsub subscriptions pull ${prefix}-jobs-dlq-inspector --auto-ack --limit=10
```

For a fix, file a follow-up issue and consider switching to bare push subscriptions (the alternative D3 path).

### Firestore singleton conflict

#### Symptom

`tofu apply` fails on `google_firestore_database.default` with a 409 Conflict or "database already exists" error.

#### Root cause

Firestore Native is a project-level singleton. The module's `var.create_firestore_database` flag was left at the default `false`, but no Firestore database actually exists in the project. OR the flag is `true` but a database already exists.

#### Resolution

If the project has no Firestore yet:

```bash
# In terraform.tfvars: create_firestore_database = true
tofu apply
```

If the project already has Firestore:

```bash
# In terraform.tfvars: create_firestore_database = false
tofu apply
```

The `google_firestore_field` TTL policy works against the singleton database regardless of who created it, so flipping this flag in either direction is safe.

### Function source bucket out of date

#### Symptom

After bumping `var.release_tag`, the Cloud Run functions still serve the old version. `gcloud beta run services describe` shows the prior revision.

#### Diagnosis

```bash
gcloud storage ls gs://${prefix}-functions-*/${var.release_tag}/
```

If this returns "no objects found", the `data.http` → `local_file` → `google_storage_bucket_object` chain failed to populate the new tag's prefix.

#### Common causes

- **GitHub Release missing the zip**: the release's assets must include `webhook.zip`, `scaleup.zip`, `scaledown.zip`, `lifecycle.zip`, `rebalancer.zip`. Verify at `https://github.com/devopsfactory-io/jit-runners/releases/tag/<tag>`.
- **Network failure during `tofu plan`**: `data.http` re-fetches each plan; transient network errors fail plan. Re-run.
- **GitHub rate limit**: unauthenticated GitHub fetches are rate-limited (currently 60 requests/hour from a given IP). If apply runs many times in a short window, you may hit the limit. Wait for the limit window to reset, or run apply from a different egress IP.

#### Resolution

Force re-fetch:

```bash
cd infra/terraform-gcp
rm -rf .cache/<release_tag>      # clear the local Terraform-managed cache
tofu apply -replace='local_file.function_zips["webhook"]'
# Repeat for the 4 other functions if needed
```

### Workload Identity Federation auth failure in CI

#### Symptom

`.github/workflows/gce-image-build.yml` fails at the "Authenticate to Google Cloud" step with `permission_denied` or `invalid_grant`.

#### Root cause

Per spec D14, image-build CI auth uses Workload Identity Federation in the maintainer's personal GCP project, set up out-of-band. The repo secrets `GCE_BUILD_WIF_PROVIDER` and `GCE_BUILD_SA_EMAIL` must be populated for the workflow to authenticate.

#### Resolution

If you're a maintainer who hasn't run the gcloud bootstrap yet:

1. Set up a Workload Identity Pool + Provider in your personal GCP project.
2. Create a service account with `compute.imageUser`, `compute.instanceAdmin.v1`, `iam.serviceAccountUser` roles bound to the Pool.
3. Set repo secrets:

   ```bash
   gh secret set GCE_BUILD_WIF_PROVIDER --body "projects/<num>/locations/global/workloadIdentityPools/jit-runners-build/providers/github"
   gh secret set GCE_BUILD_SA_EMAIL --body "[email protected]"
   ```

If you're a fork maintainer who wants their own image build pipeline: do the same in your fork's project, set your fork's repo secrets, and the workflow runs against your project.

### References

- Spec D3 (Eventarc + CloudEvents internal-only ingress).
- Spec D11 (GitHub Releases as source of truth via `data.http`).
- Spec D12 (Firestore singleton feature flag).
- Spec D13 (Pre-created Pub/Sub subscriptions verification step).
- Spec D14 (Image-build CI identity is out-of-band).
