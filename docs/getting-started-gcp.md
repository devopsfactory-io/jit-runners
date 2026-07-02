# Getting Started on GCP

This guide walks through deploying jit-runners on GCP using OpenTofu/Terraform. The Terraform module lives at `infra/terraform-gcp/`.

> **For AWS**: see [Getting Started on AWS](getting-started-aws.md).

## Prerequisites

- GCP project with permissions to create Cloud Run functions, Pub/Sub topics, Eventarc triggers, Cloud Scheduler jobs, Firestore Native, Secret Manager secrets, GCS buckets, Compute Engine instances, and IAM service accounts.
- gcloud CLI authenticated against the deploy project (`gcloud auth application-default login`).
- [OpenTofu](https://opentofu.org/) >= 1.6.0 or Terraform >= 1.6.0.
- A GitHub App configured for jit-runners (see [GitHub App Setup](github-app-setup.md)).
- A GCE image to use as the runner VM base. Either:
  - **Public image (recommended)** — use the maintainer-published `jit-runner-<version>` image. Set `var.runner_image` to its full resource path.
  - **Private image** — build your own with `make image.build-test GCP_PROJECT=my-project`. See [Building a private GCE image](#building-a-private-gce-image) below.
- A VPC subnet for runner instances. The `default` network in a fresh GCP project works; for production, use a dedicated VPC with appropriate firewall rules.

## 1. Configure variables

Copy the example tfvars and fill in your values:

```bash
cd infra/terraform-gcp
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars`:

- `gcp_project` — your GCP project ID.
- `gcp_region` — default `us-central1`.
- `prefix` — resource name prefix (default `jit-runners`).
- `release_tag` — the jit-runners GitHub release tag whose function zips this deploy uses (e.g. `v1.0.0-rc.5`). Bump and re-apply to deploy a new release.
- `create_firestore_database` — set `true` if your project has NO Firestore yet; `false` (default) if you already have one. Firestore Native is a project-level singleton.
- `runner_image` — full GCE image URI, e.g. `projects/<maintainer-project>/global/images/jit-runner-v1-...`.
- `runner_subnet` — full subnet path, e.g. `projects/<p>/regions/us-central1/subnetworks/default`.
- `github_app_id` — numeric GitHub App ID as a string.
- `github_installation_id` — numeric installation ID as a string.
- `webhook_secret_value` — webhook HMAC secret (sensitive).
- `github_app_private_key` — GitHub App private key PEM body (sensitive).

Optional:

- `runner_network` — VPC network name or full path for runner VMs (default `"default"`). Set this if you're not using the project's default network.
- `runner_zone` — GCE zone where runner VMs launch (default `"us-central1-a"`). Pin a different zone for capacity or spot pricing reasons.
- `firestore_collection` — Firestore collection name for runner records (default `"runners"`).
- `label_mappings` — JSON array of label-to-machine-type mappings (default `"[]"`, which uses `var.default_instance_type` for all labels). Each entry uses `instance_type` to specify the GCE machine type. The `instance_types` candidate list field (used for AWS spot diversification) is accepted in the JSON but GCP uses only the first entry — set `instance_type` explicitly instead.
- `max_runner_age_minutes`, `stale_threshold_minutes`, `max_re_enqueue_attempts` — operational thresholds (defaults mirror AWS module).
- `function_memory`, `function_timeout_seconds`, `max_instance_count` — Cloud Run scaling knobs.

## 2. Initialize and apply

```bash
cd infra/terraform-gcp
tofu init
tofu plan
tofu apply
```

**First-apply notes:**

- The first apply enables 12 GCP APIs (`cloudfunctions`, `cloudbuild`, `run`, `eventarc`, `pubsub`, `firestore`, `secretmanager`, `compute`, `cloudscheduler`, `storage`, `iam`, `iamcredentials`). This takes ~1-2 minutes.
- The first apply also triggers Cloud Build to build container images for all 5 Cloud Run functions (~5-10 min total). Subsequent applies with the same `release_tag` reuse the build and are much faster.
- Terraform state grows ~50MB per `release_tag` bump (5 zips × ~10MB base64-encoded in state). Use a GCS remote backend for production.

## 3. Webhook URL

```bash
tofu output webhook_url
```

Set the printed URL as the GitHub App's **Webhook URL**. The webhook function uses `ingress_settings = ALLOW_ALL` because GitHub's webhook delivery is from the public internet; HMAC verification in the function code is the trust boundary.

## 4. Verify Eventarc trigger binding (post-first-apply)

Per spec D13, after the first apply confirm that each Eventarc trigger uses the explicit Pub/Sub subscription declared by the module (with DLQ + retry policy) rather than an Eventarc-managed subscription:

```bash
gcloud eventarc triggers describe jit-runners-scaleup \
  --location=us-central1 \
  --format='value(transport.pubsub.subscription)'
```

Expected: `projects/<your-project>/subscriptions/jit-runners-jobs-scaleup`.

```bash
gcloud eventarc triggers describe jit-runners-lifecycle \
  --location=us-central1 \
  --format='value(transport.pubsub.subscription)'
```

Expected: `projects/<your-project>/subscriptions/jit-runners-lifecycle-lifecycle`.

If the printed subscription name is different (e.g. `eventarc-...` auto-generated), Eventarc has created its own managed subscription instead of binding to ours. The module's DLQ + retry policy then doesn't apply to the active delivery path. Operator workaround: failed messages can still be inspected via the inspector subscriptions on the topic (`gcloud pubsub subscriptions pull jit-runners-jobs-dlq-inspector`). File a follow-up issue.

## 5. Test the setup

1. Create a workflow in a repository where the GitHub App is installed:

```yaml
name: test-jit-runner
on: workflow_dispatch

jobs:
  test:
    runs-on: [self-hosted, linux, x64]
    steps:
      - run: echo "Hello from jit-runner on GCP!"
      - run: uname -a
```

2. Trigger the workflow manually.

3. Watch Cloud Run function logs:

```bash
gcloud beta run services logs read jit-runners-webhook --region=us-central1 --limit=20
gcloud beta run services logs read jit-runners-scaleup --region=us-central1 --limit=20
gcloud beta run services logs read jit-runners-rebalancer --region=us-central1 --limit=20
```

The rebalancer should fire every minute (Cloud Scheduler `* * * * *`) and emit per-repo `cycle complete repo=<owner/repo> demand=N supply=M published=K label_sets=L` lines. See [troubleshooting.md](troubleshooting.md#stranded-queued-jobs) for what these mean.

## 6. Updating to a new release

GCP function source zips are fetched declaratively from the GitHub Release matching `var.release_tag`. To upgrade:

1. Edit `terraform.tfvars`: bump `release_tag` to the new version.
2. `tofu apply`.

The module re-fetches the 5 function zips, re-uploads to GCS, and Cloud Functions Gen 2 rebuilds the container images. No manual `gsutil cp` step is needed.

For the canonical production rollout flow including AWS + GCP in the same release cycle, see [release.md](release.md).

## Building a private GCE image

If you don't want to use the maintainer's public image, build your own:

```bash
# Validate the Packer template
make image.validate

# Build a private (single-region) image — uses ami_name_prefix=jit-runner-pr
make image.build-test GCP_PROJECT=my-jit-runners-project

# Build a multi-region image (us, eu, asia)
make image.build-distribute GCP_PROJECT=my-jit-runners-project
```

The Packer template is shared between AWS and GCP at `infra/packer/`. The GCP-specific provisioning scripts live under `infra/packer/scripts/gcp/` and target Ubuntu 24.04 LTS (vs `scripts/aws/` which targets Amazon Linux 2023).

After the build completes, point `var.runner_image` at the new image:

```hcl
runner_image = "projects/my-jit-runners-project/global/images/jit-runner-pr-v1-...-1700000000"
```

## Destroying the stack

```bash
cd infra/terraform-gcp
tofu destroy
```

This terminates all managed resources except the Firestore database (`delete_protection_state` is intentionally `DISABLED` so destroy works, but Firestore deletion has historically been flaky in some accounts; if it errors, delete via the GCP console manually).

## Differences from the AWS path

- **Function deploy mechanism**: GCP uses Cloud Functions Gen 2 (source zip → Buildpacks → Cloud Run revision). AWS uses Lambda (zip → direct execution).
- **Operator deploy UX**: GCP operators set `var.release_tag` and `tofu apply`. AWS operators run `aws s3 cp` then `aws cloudformation update-stack` (or `terraform apply` after editing tfvars). The GCP fetch is declarative; the AWS upload is imperative.
- **Image-build IAM**: GCE image builds (`make image.build-distribute`) require Workload Identity Federation set up out-of-band in the maintainer's personal GCP project. The deploy module does NOT provision image-build IAM. AWS uses an analogous pattern (`AMI_BUILD_ROLE_ARN` OIDC role).
- **No `compute.instanceAdmin.v1` scope-down**: the scaleup function has project-level `compute.instanceAdmin.v1` for v1; custom-role + IAM-conditioned scope-down is a future hardening pass. AWS has a similar shape.

## Next Steps

- [GitHub App Setup](github-app-setup.md) — if you haven't set up the GitHub App yet.
- [Release procedure](release.md) — production rollout flow with AWS + GCP both updated.
- [Troubleshooting](troubleshooting.md) — common operational issues + diagnosis recipes (GCP-specific section near the end).
- [Getting Started on AWS](getting-started-aws.md) — AWS path is parallel; same architecture, AWS-native services.
