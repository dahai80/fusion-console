#!/usr/bin/env bash
# release.sh — end-to-end release automation for fusion-console.
#
# One command does the whole release; every step fails loudly:
#   1. Preflight: clean tree, webdist fresh, gh authed
#   2. Package:   scripts/package.sh (console+gateway, startup, sidecar, DMG)
#   3. Verify:    every archive must contain the console, the gateway binary,
#                 a startup launcher, the sidecar and config.yaml — refuse to
#                 publish anything else
#   4. Publish:   git tag + gh release create, upload every dist artifact,
#                 then verify uploaded sizes byte-match local files
#
# Usage:
#   scripts/release.sh vX.Y.Z            # full release
#   scripts/release.sh vX.Y.Z --draft    # everything but publish (draft release)
#   scripts/release.sh vX.Y.Z --verify-only   # re-check existing dist/ + assets
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?usage: release.sh vX.Y.Z [--draft|--verify-only]}"
MODE="${2:-}"
case "$MODE" in
    --draft | --verify-only | "") ;;
    *) echo "error: unknown flag $MODE (use --draft or --verify-only)" >&2; exit 2 ;;
esac
REPO="dahai80/fusion-console"
DIST="dist"
log() { echo "[release] $*"; }
die() { echo "[release] ERROR: $*" >&2; exit 1; }

# --- 1. Preflight -----------------------------------------------------------
command -v gh >/dev/null || die "gh CLI not found"
command -v shasum >/dev/null || die "shasum not found"
gh auth status >/dev/null 2>&1 || die "gh not authenticated (run: gh auth login)"

if [ "$MODE" != "--verify-only" ]; then
    # Uncommitted changes would silently miss the release — refuse.
    if ! git diff-index --quiet HEAD -- 2>/dev/null; then
        die "working tree has uncommitted changes — commit first (git status)"
    fi
    [ -f internal/server/webdist/index.html ] || die "webdist missing — run (cd web && bun run build)"
    # webdist must not be older than web sources (stale embed = old UI shipped).
    if [ -n "$(find web/src -newer internal/server/webdist/index.html -name '*.tsx' -print -quit 2>/dev/null)" ] ||
       [ -n "$(find web/src -newer internal/server/webdist/index.html -name '*.ts' -print -quit 2>/dev/null)" ]; then
        die "web/src is newer than webdist — rebuild the frontend first (cd web && bun run build)"
    fi
    if git tag --list "$VERSION" | grep -q .; then
        die "tag $VERSION already exists — pick a new version or delete the tag"
    fi
    log "preflight ok (tree clean, webdist fresh, tag $VERSION free)"
fi

# --- 2. Package -------------------------------------------------------------
if [ "$MODE" != "--verify-only" ]; then
    log "packaging $VERSION via scripts/package.sh"
    ./scripts/package.sh "$VERSION"
else
    [ -d "$DIST" ] || die "no dist/ directory to verify"
fi
[ -f "$DIST/sha256sums.txt" ] || shasum -a 256 "$DIST"/fusion-console-$VERSION-* > "$DIST/sha256sums.txt"

# --- 3. Verify archives -----------------------------------------------------
# One missing launcher or gateway binary is exactly how v0.1.17 first shipped
# broken — every archive is inspected before anything is uploaded.
verify_archive() {
    local archive="$1" kind="$2" listing
    case "$kind" in
        tar) listing=$(tar tzf "$archive") || die "cannot read archive $archive" ;;
        zip) listing=$(unzip -l "$archive" | awk '{print $4}') || die "cannot read archive $archive" ;;
        dmg) hdiutil imageinfo "$archive" >/dev/null 2>&1 || die "$archive: not a readable DMG" ;;
    esac
    if [ "$kind" != "dmg" ]; then
        echo "$listing" | grep -qE "/(fusion-console|fusion-console\.exe)$" || die "$archive: console binary missing"
        echo "$listing" | grep -qE "/(fusion-gateway|fusion-gateway\.exe)$" || die "$archive: gateway binary missing"
        echo "$listing" | grep -qE "/(startup\.sh|startup\.cmd)$" || die "$archive: startup launcher missing"
        echo "$listing" | grep -q "harnessd.py" || die "$archive: sidecar/harnessd.py missing"
        echo "$listing" | grep -q "config.yaml" || die "$archive: gateway config.yaml missing"
    fi
    log "verified $(basename "$archive")"
}

ARCHIVES=()
for f in "$DIST"/fusion-console-$VERSION-*; do
    [ -f "$f" ] || continue
    case "$f" in
        *.tar.gz) verify_archive "$f" tar ;;
        *.zip) verify_archive "$f" zip ;;
        *.dmg) verify_archive "$f" dmg ;;
        *) die "unexpected artifact in dist/: $f" ;;
    esac
    ARCHIVES+=("$f")
done
[ ${#ARCHIVES[@]} -ge 5 ] || die "expected at least 5 archives in dist/, found ${#ARCHIVES[@]}"
log "all ${#ARCHIVES[@]} archives verified"

# --- 4. Publish -------------------------------------------------------------
if [ "$MODE" = "--verify-only" ]; then
    log "verify-only mode: nothing published"
    exit 0
fi

TITLE="$VERSION — fusion-console release"
if gh release view "$VERSION" -R "$REPO" >/dev/null 2>&1; then
    die "release $VERSION already exists on GitHub — delete it first if republishing"
fi

git tag "$VERSION"
git push origin "$VERSION" || die "tag push failed"
log "tag $VERSION pushed"

if [ "$MODE" = "--draft" ]; then
    gh release create "$VERSION" -R "$REPO" --draft --title "$TITLE" \
        --notes "Draft release — see scripts/release.sh notes template." \
        || die "gh release create failed"
    log "draft release $VERSION created"
else
    gh release create "$VERSION" -R "$REPO" --title "$TITLE" \
        --generate-notes --latest \
        || die "gh release create failed"
    log "release $VERSION created"
fi

log "uploading ${#ARCHIVES[@]} archives + sha256sums.txt"
gh release upload "$VERSION" -R "$REPO" --clobber "${ARCHIVES[@]}" "$DIST/sha256sums.txt" \
    || die "asset upload failed"

# --- 5. Post-publish verification -------------------------------------------
# Compare every uploaded asset size with the local file; a truncated upload
# (the v0.1.16 download once died mid-stream) is caught here, not by users.
local_size() { stat -f%z "$1" 2>/dev/null || stat -c%s "$1"; }
fail=0
while read -r name size; do
    [ -n "$name" ] || continue
    local_file="$DIST/$name"
    if [ -f "$local_file" ]; then
        want=$(local_size "$local_file")
        if [ "$size" != "$want" ]; then
            echo "[release] ERROR: asset $name size $size != local $want" >&2
            fail=1
        fi
    fi
done < <(gh release view "$VERSION" -R "$REPO" --json assets --jq '.assets[] | "\(.name) \(.size)"')
[ "$fail" -eq 0 ] || die "asset size mismatch after upload — delete the release and re-run"
[ "$(gh release view "$VERSION" -R "$REPO" --json isDraft --jq .isDraft)" = "false" ] || die "release is still a draft"

URL=$(gh release view "$VERSION" -R "$REPO" --json url --jq .url)
log "RELEASE OK: $URL"
