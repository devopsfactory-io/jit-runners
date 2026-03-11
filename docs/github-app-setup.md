# GitHub App Setup

## Overview
jit-runners uses a GitHub App to receive `workflow_job` webhook events. The app needs specific permissions and event subscriptions to function correctly.

## Prerequisites
- A GitHub account with permission to create GitHub Apps (org admin or user account)
- An AWS account with access to Secrets Manager

## 1. Create the GitHub App

Steps:
1. Go to GitHub Settings -> Developer settings -> GitHub Apps -> New GitHub App
2. Fill in:
   - **GitHub App name**: e.g. `jit-runners` or `my-org-runners`
   - **Homepage URL**: your org/repo URL
   - **Webhook**: Check "Active"
   - **Webhook URL**: Leave blank for now (you'll set this after deploying infrastructure)
   - **Webhook secret**: Generate a strong random secret (e.g. `openssl rand -hex 32`) -- save this value, you'll need it for AWS Secrets Manager
3. Permissions (Repository):
   - **Actions**: Read-only (to query runner registration)
   - **Administration**: Read and write (required to create JIT runner configurations)
   - **Metadata**: Read-only (default)
4. Subscribe to events:
   - **Workflow job** (this is the only event jit-runners needs)
5. Where can this GitHub App be installed? -> "Only on this account" (or "Any account" if you plan to share)
6. Click "Create GitHub App"

## 2. Generate a Private Key

1. On the App's settings page, scroll to "Private keys"
2. Click "Generate a private key"
3. Download the `.pem` file -- keep it secure

## 3. Note the App ID

- On the App's settings page, find the **App ID** (numeric). You'll need this as a parameter when deploying.

## 4. Install the App

1. Go to the App's settings -> "Install App"
2. Select your organization or user account
3. Choose "All repositories" or select specific repositories that will use self-hosted runners
4. Click "Install"

Note the **Installation ID** from the URL after installation (e.g. `https://github.com/settings/installations/12345678` -> installation ID is `12345678`). This is used internally by jit-runners but is automatically detected from webhook payloads.

## 5. Store Secrets in AWS Secrets Manager

### Webhook Secret

```bash
aws secretsmanager create-secret \
  --name jit-runners/github-webhook-secret \
  --description "GitHub App webhook secret for jit-runners" \
  --secret-string "YOUR_WEBHOOK_SECRET_HERE"
```

Note the ARN from the output -- you'll need it as a deployment parameter.

### Private Key

```bash
aws secretsmanager create-secret \
  --name jit-runners/github-app-private-key \
  --description "GitHub App private key (PEM) for jit-runners" \
  --secret-string file://path/to/your-app.pem
```

Note the ARN from the output -- you'll need it as a deployment parameter.

## 6. Set the Webhook URL

After deploying jit-runners infrastructure (via Terraform or CloudFormation), you'll get a Webhook URL as an output. Go back to the GitHub App settings and set the **Webhook URL** to this value.

## Next Steps

- Deploy with Terraform/OpenTofu: [Getting Started with Terraform](getting-started-terraform.md)
- Deploy with CloudFormation: [Getting Started with CloudFormation](getting-started-cloudformation.md)

## Troubleshooting

### Webhook not being received
- Verify the Webhook URL is correct in the GitHub App settings
- Check that "Active" is enabled for webhooks
- Go to the App's "Advanced" tab to see recent webhook deliveries and their status

### Permission errors
- Ensure the App has **Administration: Read and write** permission -- this is required for the JIT runner config generation API
- Ensure the App is installed on the target repository

### Secrets Manager access
- The Lambda functions need `secretsmanager:GetSecretValue` permission on both secret ARNs -- this is configured in the infrastructure templates
