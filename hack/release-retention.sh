#!/usr/bin/env bash
#
# release-retention.sh
#
# After a successful release, prune everything older than the latest 3
# GA releases. Designed to run as the final job in the release workflow,
# AFTER github-release has succeeded — so the just-shipped release is
# verifiably reachable before we touch anything older.
#
# What gets pruned:
#  * GitHub Release entries older than the latest 3 GA → marked as
#    pre-release (NOT deleted; CLI binary deep links keep working).
#  * Container package versions on ghcr.io tagged for releases that
#    just fell out of the latest-3 window → deleted.
#  * Untagged container manifests older than 30 days → deleted.
#  * `gh-pages` charts/index.yaml entries older than the latest 3 →
#    trimmed (the .tgz blobs themselves are kept for deep-link safety).
#
# What is NEVER pruned:
#  * Git tags. Forever.
#  * PyPI versions of the agenttier wheel. PEP 449 antisocial-deletion.
#  * Cosign signatures + SBOM attestations whose target manifests are
#    still kept.
#
# Usage:
#   hack/release-retention.sh <current-tag> [--dry-run]
#
# Examples:
#   hack/release-retention.sh v0.5.5
#   hack/release-retention.sh v0.5.5 --dry-run
#
# Requirements:
#   * `gh` CLI authenticated (release workflow already has GITHUB_TOKEN).
#   * `jq` for JSON parsing.
#   * `yq` v4+ for YAML editing of charts/index.yaml.
#   * `git` configured with push access for gh-pages (release workflow
#     already runs in repo context with contents: write).
#
# Designed to be idempotent — running twice on the same release is a
# no-op (older releases stay pre-release; already-deleted package
# versions return 404 which we tolerate).

set -euo pipefail

CURRENT_TAG="${1:-}"
DRY_RUN=false

if [[ "${2:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

if [[ -z "${CURRENT_TAG}" ]]; then
  echo "usage: $0 <current-tag> [--dry-run]" >&2
  echo "       (current-tag must be the just-shipped release, e.g. v0.5.5)" >&2
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

KEEP_COUNT="${KEEP_COUNT:-3}"
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

log "starting retention pass; current=${CURRENT_TAG} keep=${KEEP_COUNT} dry-run=${DRY_RUN}"

# -------- Step 1: identify GA releases, oldest first --------

log "listing GA releases"
ALL_GA=$(gh release list --repo "${REPO}" --limit 100 \
  --json tagName,publishedAt,isDraft,isPrerelease \
  --jq 'map(select(.isDraft==false and .isPrerelease==false)) | sort_by(.publishedAt) | .[].tagName')

# Newest-first array so we can take the head.
mapfile -t GA_NEWEST_FIRST < <(echo "${ALL_GA}" | tac)

KEEP_TAGS=("${GA_NEWEST_FIRST[@]:0:${KEEP_COUNT}}")
PRUNE_TAGS=("${GA_NEWEST_FIRST[@]:${KEEP_COUNT}}")

if (( ${#GA_NEWEST_FIRST[@]} == 0 )); then
  warn "no GA releases found; nothing to prune"
  exit 0
fi

log "keeping: ${KEEP_TAGS[*]}"
if (( ${#PRUNE_TAGS[@]} == 0 )); then
  log "no releases to demote (only ${#GA_NEWEST_FIRST[@]} GA releases exist; need >${KEEP_COUNT} before any get pruned)"
else
  log "demoting to pre-release: ${PRUNE_TAGS[*]}"
fi

# -------- Step 2: mark older Releases as pre-release --------

for tag in "${PRUNE_TAGS[@]}"; do
  log "marking ${tag} as pre-release"
  run_or_dry "gh release edit ${tag} --repo ${REPO} --prerelease"
done

# -------- Step 3: prune untagged + out-of-window container versions --------

now=$(date -u +%s)
cutoff=$((now - UNTAGGED_PRUNE_AGE_SECONDS))

for pkg in "${PACKAGES[@]}"; do
  log "scanning ghcr.io/${ORG}/${pkg}"

  # Some packages may not exist (new ones in this release that aren't
  # yet in the historical retention scope, or older retired ones).
  # 404 the listing and continue.
  if ! gh api "/orgs/${ORG}/packages/container/${pkg}" >/dev/null 2>&1; then
    warn "package ${pkg} not found on ghcr.io; skipping"
    continue
  fi

  # 3a. Untagged versions older than 30 days.
  while IFS= read -r id; do
    [[ -z "${id}" ]] && continue
    log "  untagged ${pkg} version ${id} → delete"
    run_or_dry "gh api -X DELETE /orgs/${ORG}/packages/container/${pkg}/versions/${id}"
  done < <(gh api --paginate "/orgs/${ORG}/packages/container/${pkg}/versions" \
    --jq ".[] | select(.metadata.container.tags | length == 0)
              | select((.created_at | fromdateiso8601) < ${cutoff})
              | .id" 2>/dev/null)

  # 3b. Tagged versions for releases in PRUNE_TAGS.
  for tag in "${PRUNE_TAGS[@]}"; do
    while IFS= read -r id; do
      [[ -z "${id}" ]] && continue
      log "  ${pkg}:${tag} version ${id} → delete"
      run_or_dry "gh api -X DELETE /orgs/${ORG}/packages/container/${pkg}/versions/${id}"
    done < <(gh api "/orgs/${ORG}/packages/container/${pkg}/versions" \
      --jq ".[] | select(.metadata.container.tags[]? == \"${tag}\") | .id" 2>/dev/null)
  done
done

# -------- Step 4: trim charts/index.yaml on gh-pages --------

log "trimming Helm chart index to latest ${KEEP_COUNT}"

if $DRY_RUN; then
  printf '\033[1;35m[dry-run]\033[0m would: git fetch origin gh-pages && checkout && yq trim && commit\n'
else
  # Use a worktree so the workflow's main checkout stays untouched.
  WORKTREE_DIR=$(mktemp -d)
  trap 'git worktree remove --force "${WORKTREE_DIR}" >/dev/null 2>&1 || true' EXIT

  git fetch origin gh-pages --depth=1
  git worktree add "${WORKTREE_DIR}" origin/gh-pages

  if [[ -f "${WORKTREE_DIR}/charts/index.yaml" ]]; then
    pushd "${WORKTREE_DIR}" >/dev/null

    # Sort by version desc and keep only KEEP_COUNT entries.
    yq -i ".entries.agenttier |= (sort_by(.version) | reverse | .[:${KEEP_COUNT}])" charts/index.yaml

    if git diff --quiet charts/index.yaml; then
      log "  charts/index.yaml already at desired length; nothing to commit"
    else
      git add charts/index.yaml
      git -c user.email='actions@github.com' \
          -c user.name='github-actions[bot]' \
          commit -m "Trim Helm chart index to latest ${KEEP_COUNT} (post-release retention for ${CURRENT_TAG})"
      git push origin HEAD:gh-pages
      log "  charts/index.yaml trimmed and pushed"
    fi

    popd >/dev/null
  else
    warn "  gh-pages charts/index.yaml not found; skipping trim"
  fi
fi

# -------- Step 5: summary --------

log ""
log "post-state summary:"
gh release list --repo "${REPO}" --limit 10 || true
log ""
for pkg in "${PACKAGES[@]}"; do
  count=$(gh api "/orgs/${ORG}/packages/container/${pkg}/versions" --jq 'length' 2>/dev/null || echo "?")
  printf '  %-26s %s versions\n' "${pkg}" "${count}"
done

log "retention pass complete"
