#!/usr/bin/env bash
# Prune self-owned public jit-runner AMIs: keep newest-N (+ safe-list + live DefaultAMI),
# deregister the rest and delete their snapshots. Dry-run by default.
set -euo pipefail

REGIONS=""; KEEP_LATEST=2; KEEP_AMI=""; STACK_NAME=""; NAME_PREFIX="jit-runner"
ENSURE_FREE=""; QUOTA=""; APPLY=0
QUOTA_CODE="L-0E3CBAB9"   # EC2 "Public AMIs" service quota

usage() { sed -n '2,4p' "$0"; echo "Usage: $0 --regions r1,r2 [--keep-latest N] [--keep-ami a,b] [--stack-name S] [--name-prefix P] [--ensure-free K] [--quota Q] [--apply]"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --regions)      REGIONS="$2"; shift 2;;
    --keep-latest)  KEEP_LATEST="$2"; shift 2;;
    --keep-ami)     KEEP_AMI="$2"; shift 2;;
    --stack-name)   STACK_NAME="$2"; shift 2;;
    --name-prefix)  NAME_PREFIX="$2"; shift 2;;
    --ensure-free)  ENSURE_FREE="$2"; shift 2;;
    --quota)        QUOTA="$2"; shift 2;;
    --apply)        APPLY=1; shift;;
    -h|--help)      usage; exit 0;;
    *) echo "unknown arg: $1" >&2; usage; exit 2;;
  esac
done
[ -n "${REGIONS}" ] || { echo "--regions is required" >&2; exit 2; }

# Validate numeric args (empty ensure-free/quota are allowed — they're optional).
is_uint() { [[ "$1" =~ ^[0-9]+$ ]]; }
is_uint "${KEEP_LATEST}" || { echo "--keep-latest must be a non-negative integer" >&2; exit 2; }
if [ -n "${ENSURE_FREE}" ]; then
  is_uint "${ENSURE_FREE}" || { echo "--ensure-free must be a non-negative integer" >&2; exit 2; }
fi
if [ -n "${QUOTA}" ]; then
  is_uint "${QUOTA}" || { echo "--quota must be a non-negative integer" >&2; exit 2; }
fi

ERRORS=0
ERRFILE="$(mktemp)"
trap 'rm -f "${ERRFILE}"' EXIT
log() { echo "[ami-prune] $*"; }

# resolve_stack_ami <region> -> echoes the stack DefaultAMI id, or empty
resolve_stack_ami() {
  local region="$1"
  [ -n "${STACK_NAME}" ] || { echo ""; return 0; }
  local ami
  if ! ami="$(aws cloudformation describe-stacks --stack-name "${STACK_NAME}" --region "${region}" \
        --query "Stacks[0].Parameters[?ParameterKey=='DefaultAMI'].ParameterValue | [0]" \
        --output text 2>/dev/null)"; then
    log "WARN: could not resolve DefaultAMI from stack ${STACK_NAME} in ${region}; proceeding without it"
    echo ""; return 0
  fi
  [ "${ami}" = "None" ] && ami=""
  echo "${ami}"
}

prune_region() {
  local region="$1"
  # Newest-first list of self-owned public AMIs matching the name prefix.
  local images
  images="$(aws ec2 describe-images --region "${region}" --owners self \
    --filters "Name=is-public,Values=true" "Name=name,Values=${NAME_PREFIX}*" \
    --query "reverse(sort_by(Images,&CreationDate))[].{Id:ImageId,Snaps:BlockDeviceMappings[].Ebs.SnapshotId}" \
    --output json)"

  # ids in newest-first order (the projected array lists Id before its Snaps per object)
  local ids; ids="$(echo "${images}" | grep -oE 'ami-[a-z0-9]+' | awk '!seen[$0]++')"

  # Build keep-set: newest-N + --keep-ami + stack DefaultAMI (filter blanks).
  # KEEP_LATEST=0 is the one-time-purge path: empty newest-N without `head -n 0`
  # (BSD/macOS head treats 0 as "all lines").
  local keep=""
  if [ "${KEEP_LATEST}" -gt 0 ]; then keep="$(echo "${ids}" | head -n "${KEEP_LATEST}")"; fi
  if [ -n "${KEEP_AMI}" ]; then keep="${keep}"$'\n'"$(echo "${KEEP_AMI}" | tr ',' '\n')"; fi
  local stack_ami; stack_ami="$(resolve_stack_ami "${region}")"
  [ -n "${stack_ami}" ] && keep="${keep}"$'\n'"${stack_ami}"
  keep="$(echo "${keep}" | grep -v '^$' || true)"

  # Candidates = ids not in keep-set, OLDEST first (reverse of newest-first)
  local candidates; candidates="$(comm -23 <(echo "${ids}" | grep -v '^$' | sort -u) <(echo "${keep}" | sort -u) || true)"
  # order candidates oldest-first using the original newest-first list
  candidates="$(echo "${ids}" | grep -Fxf <(echo "${candidates}") | awk '{lines[NR]=$0} END{for(i=NR;i>=1;i--) print lines[i]}' || true)"

  # --ensure-free: only delete enough oldest candidates to leave K free slots; else delete all candidates
  local to_delete="${candidates}"
  if [ -n "${ENSURE_FREE}" ]; then
    local q; q="$(effective_quota "${region}")"
    # NOTE: counts only our jit-runner* public AMIs; assumes no unrelated public AMIs
    # share this region's Public-AMIs quota (true for the dedicated build account).
    local count; count="$(echo "${ids}" | grep -c 'ami-' || true)"
    local max_allowed=$(( q - ENSURE_FREE ))
    local need=$(( count - max_allowed ))
    if [ "${need}" -le 0 ]; then
      log "${region}: ${count} public AMIs, quota ${q}, ensure-free ${ENSURE_FREE} → nothing to free"
      return 0
    fi
    to_delete="$(echo "${candidates}" | head -n "${need}")"
    local freed; freed="$(echo "${to_delete}" | grep -c 'ami-' || true)"
    log "${region}: at/near quota (${count}/${q}); freeing ${freed} slot(s)"
  fi

  [ -z "${to_delete}" ] && { log "${region}: nothing to prune"; return 0; }

  local ami
  while IFS= read -r ami; do
    [ -z "${ami}" ] && continue
    local snaps; snaps="$(echo "${images}" | python3 -c "import sys,json;d=json.load(sys.stdin);a=sys.argv[1];print('\n'.join(s for o in d if o['Id']==a for s in (o.get('Snaps') or [])))" "${ami}" 2>/dev/null || true)"
    if [ "${APPLY}" -eq 1 ]; then
      log "${region}: deregistering ${ami}"
      if ! aws ec2 deregister-image --image-id "${ami}" --region "${region}"; then
        log "ERROR: deregister ${ami} failed"; ERRORS=1; continue
      fi
      local s
      for s in ${snaps}; do
        log "${region}: deleting ${s}"
        if ! aws ec2 delete-snapshot --snapshot-id "${s}" --region "${region}" 2>"${ERRFILE}"; then
          if grep -q "InvalidSnapshot.InUse" "${ERRFILE}"; then
            log "WARN: ${s} still in use by a retained AMI; skipping"
          else
            log "ERROR: delete ${s} failed"; ERRORS=1
          fi
        fi
      done
    else
      log "${region}: [dry-run] would deregister ${ami} (snapshots: ${snaps//$'\n'/ })"
    fi
  done <<< "${to_delete}"
}

# effective_quota <region> -> the Public AMIs quota: --quota override, else Service Quotas, else 5
effective_quota() {
  local region="$1"
  if [ -n "${QUOTA}" ]; then echo "${QUOTA}"; return 0; fi
  local v
  if v="$(aws service-quotas get-service-quota --service-code ec2 --quota-code "${QUOTA_CODE}" \
        --region "${region}" --query 'Quota.Value' --output text 2>/dev/null)" && [ -n "${v}" ] && [ "${v}" != "None" ]; then
    printf '%.0f\n' "${v}"   # 5.0 -> 5
  else
    log "WARN: Service Quotas lookup failed in ${region}; defaulting Public AMIs quota to 5"
    echo 5
  fi
}

IFS=',' read -ra RLIST <<< "${REGIONS}"
for r in "${RLIST[@]}"; do prune_region "${r}"; done
exit "${ERRORS}"
