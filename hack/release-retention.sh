#!/usr/bin/env bash
#
# release-retention.sh
#
# After a successful release, prune everything that accumulates over time so
# the Releases page, container registry, Helm index, Pages deployments, and
# Dependabot branches stay tidy. Designed to run as the final job in the
# release workflow, AFTER github-release has succeeded — so the just-shipped
# release is verifiably reachable before we touch anything older.
#
# What gets pruned (keep the latest N, default 5):
#  * GitHub Release entries older than the latest N GA → DELETED (the git tag
#    is preserved, so `go install ...@vX` and `helm`/`docker` by-tag still
#    resolve; only the Release page + its CLI-binary assets go).
#  * Container package versions on ghcr.io tagged for releases that fell out
#    of the latest-N window → deleted.
#  * Untagged container manifests older than 30 days → deleted.
#  * `gh-pages` charts/index.yaml entries older than the latest N → trimmed
#    (the .tgz blobs are kept for deep-link safety).
#  * `github-pages` environment deployments older than the latest 10 →
#    deleted (marked inactive first, as the API requires).
#  * `dependabot/*` branches with no open PR → deleted (closed/superseded
#    Dependabot PRs leave their branches behind otherwise).
#
# What is NEVER pruned:
#  * Git tags. Forever — the immutable version record.
#  * PyPI versions of the agenttier wheel. PEP 449 antisocial-deletion.
#  * Cosign signatures + SBOM attestations whose target manifests are kept.
#  * The `main` and `gh-pages` branches.
#
# Usage:
#   hack/release-retention.sh <current-tag> [--dry-run]
#
# Requirements: gh (authenticated), jq, yq v4+, git with gh-pages push access.
# Idempotent — running twice is a no-op (already-deleted resources 404, which
# we tolerate).

set -euo pipefail

CURRENT_TAG="${1:-}"
DRY_RUN=false
if [[ "${2:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi
if [[ -z "${CURRENT_TAG}" ]]; then
  echo "usage: $0 <current-tag> [--dry-run]" >&2
  exit 2
fi

ORG="${ORG:-agenttier}"
REPO="${REPO:-agenttier/agenttier}"
PACKAGES=(
  controller
  router
  web-ui
  sandbox-general
  sandbox-claude-code
  sandbox-minimal
  sandbox-langgraph
  sandbox-openclaw
  sandbox-strands-bedrock
  sandbox-rl
  sandbox-openhands
  otel-collector
)

KEEP_COUNT="${KEEP_COUNT:-5}"
KEEP_DEPLOYMENTS="${KEEP_DEPLOYMENTS:-10}"
UNTAGGED_PRUNE_AGE_SECONDS="${UNTAGGED_PRUNE_AGE_SECONDS:-$((30 * 24 * 3600))}"

log() { printf '\033[1;36m[retention]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[retention]\033[0m %s\n' "$*"; }
run_or_dry() {
  if $DRY_RUN; then
    printf '\033[1;35m[dry-run]\033[0m %s\n' "$*"
  else
    eval "$@"
  fi
}

log "starting retention; current=${CURRENT_TAG} keep-releases=${KEEP_COUNT} keep-deployments=${KEEP_DEPLOYMENTS} dry-run=${DRY_RUN}"

# -------- Step 1: identify GA releases, newest first --------

ALL_GA=$(gh release list --repo "${REPO}" --limit 200 \
  --json tagName,publishedAt,isDraft,isPrerelease \
  --jq 'map(select(.isDraft==false and .isPrerelease==false)) | sort_by(.publishedAt) | .[].tagName')

mapfile -t GA_NEWEST_FIRST < <(echo "${ALL_GA}" | tac)

if (( ${#GA_NEWEST_FIRST[@]} == 0 )); then
  warn "no GA releases found; nothing to prune"
  exit 0
fi

KEEP_TAGS=("${GA_NEWEST_FIRST[@]:0:${KEEP_COUNT}}")
PRUNE_TAGS=("${GA_NEWEST_FIRST[@]:${KEEP_COUNT}}")

log "keeping latest ${KEEP_COUNT}: ${KEEP_TAGS[*]}"
if (( ${#PRUNE_TAGS[@]} == 0 )); then
  log "no releases beyond the keep window (${#GA_NEWEST_FIRST[@]} GA total)"
else
  log "deleting release entries (tags preserved): ${PRUNE_TAGS[*]}"
fi

# -------- Step 2: DELETE older Release entries (git tag preserved) --------

for tag in "${PRUNE_TAGS[@]}"; do
  log "deleting GitHub Release ${tag} (keeping the git tag)"
  # --cleanup-tag is intentionally omitted so the tag survives.
  run_or_dry "gh release delete ${tag} --repo ${REPO} --yes"
done

# -------- Step 3: prune untagged + out-of-window container versions --------

now=$(date -u +%s)
cutoff=$((now - UNTAGGED_PRUNE_AGE_SECONDS))

for pkg in "${PACKAGES[@]}"; do
  if ! gh api "/orgs/${ORG}/packages/container/${pkg}" >/dev/null 2>&1; then
    warn "package ${pkg} not found on ghcr.io; skipping"
    continue
  fi
  log "scanning ghcr.io/${ORG}/${pkg}"

  # 3a. Untagged manifests older than 30 days.
  while IFS= read -r id; do
    [[ -z "${id}" ]] && continue
    log "  untagged ${pkg} version ${id} → delete"
    run_or_dry "gh api -X DELETE /orgs/${ORG}/packages/container/${pkg}/versions/${id}"
  done < <(gh api --paginate "/orgs/${ORG}/packages/container/${pkg}/versions" \
    --jq ".[] | select(.metadata.container.tags | length == 0)
              | select((.created_at | fromdateiso8601) < ${cutoff})
              | .id" 2>/dev/null)

  # 3b. Tagged versions for releases that fell out of the keep window.
  for tag in "${PRUNE_TAGS[@]}"; do
    while IFS= read -r id; do
      [[ -z "${id}" ]] && continue
      log "  ${pkg}:${tag} version ${id} → delete"
      run_or_dry "gh api -X DELETE /orgs/${ORG}/packages/container/${pkg}/versions/${id}"
    done < <(gh api "/orgs/${ORG}/packages/container/${pkg}/versions" \
      --jq ".[] | select(.metadata.container.tags[]? == \"${tag}\") | .id" 2>/dev/null)
  done
done

# -------- Step 4: prune github-pages deployments to the latest N --------

log "pruning github-pages deployments to the latest ${KEEP_DEPLOYMENTS}"
mapfile -t DEPLOY_IDS < <(gh api --paginate \
  "/repos/${REPO}/deployments?environment=github-pages" --jq '.[].id' 2>/dev/null || true)
if (( ${#DEPLOY_IDS[@]} > KEEP_DEPLOYMENTS )); then
  for id in "${DEPLOY_IDS[@]:${KEEP_DEPLOYMENTS}}"; do
    [[ -z "${id}" ]] && continue
    log "  deployment ${id} → inactive + delete"
    # The API rejects deleting an active deployment, so mark inactive first.
    run_or_dry "gh api -X POST /repos/${REPO}/deployments/${id}/statuses -f state=inactive >/dev/null"
    run_or_dry "gh api -X DELETE /repos/${REPO}/deployments/${id}"
  done
else
  log "  ${#DEPLOY_IDS[@]} deployments ≤ ${KEEP_DEPLOYMENTS}; nothing to prune"
fi

# -------- Step 5: delete dependabot branches with no open PR --------

log "deleting dependabot branches whose PR is closed/merged"
while IFS= read -r br; do
  [[ -z "${br}" ]] && continue
  open=$(gh pr list --repo "${REPO}" --head "${br}" --state open --json number --jq 'length' 2>/dev/null || echo 0)
  if [[ "${open}" == "0" ]]; then
    log "  branch ${br} → delete (no open PR)"
    run_or_dry "gh api -X DELETE /repos/${REPO}/git/refs/heads/${br}"
  fi
done < <(gh api --paginate "/repos/${REPO}/branches" --jq '.[].name' 2>/dev/null \
  | grep '^dependabot/' || true)

# -------- Step 6: trim charts/index.yaml on gh-pages --------

log "trimming Helm chart index to latest ${KEEP_COUNT}"
if $DRY_RUN; then
  printf '\033[1;35m[dry-run]\033[0m would: fetch gh-pages, yq-trim charts/index.yaml to %s, commit\n' "${KEEP_COUNT}"
else
  WORKTREE_DIR=$(mktemp -d)
  trap 'git worktree remove --force "${WORKTREE_DIR}" >/dev/null 2>&1 || true' EXIT
  git fetch origin gh-pages --depth=1
  git worktree add "${WORKTREE_DIR}" origin/gh-pages
  if [[ -f "${WORKTREE_DIR}/charts/index.yaml" ]]; then
    pushd "${WORKTREE_DIR}" >/dev/null
    yq -i ".entries.agenttier |= (sort_by(.version) | reverse | .[:${KEEP_COUNT}])" charts/index.yaml
    if git diff --quiet charts/index.yaml; then
      log "  charts/index.yaml already trimmed; nothing to commit"
    else
      git add charts/index.yaml
      git -c user.email='actions@github.com' -c user.name='github-actions[bot]' \
        commit -m "Trim Helm chart index to latest ${KEEP_COUNT} (retention for ${CURRENT_TAG})"
      git push origin HEAD:gh-pages
      log "  charts/index.yaml trimmed and pushed"
    fi
    popd >/dev/null
  else
    warn "  gh-pages charts/index.yaml not found; skipping trim"
  fi
fi

# -------- Step 7: summary --------

log ""
log "post-state summary:"
gh release list --repo "${REPO}" --limit 10 || true
log "retention pass complete"
