#!/usr/bin/env bash
# Build PyPI platform wheels for fusion-console (ruff/uv distribution model:
# one wheel per platform, each carrying the prebuilt Go binary + a thin
# Python entry-point shim).
#
# Usage: scripts/pypi-build.sh <version> [bindir]
#   <version>  package version, e.g. 0.2.1 (must be PyPI-normalized, no "v")
#   [bindir]   directory with cross binaries (default: bin/); binaries are
#              produced by "make cross" as fusion-console-<os>-<arch>[.exe]
#
# Output: dist-pypi/*.whl — upload with twine.
set -euo pipefail

VERSION="${1:?usage: pypi-build.sh <version> [bindir]}"
BINDIR="${2:-$(cd "$(dirname "$0")/.." && pwd)/bin}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist-pypi"
PKG_SRC="$ROOT/pypi"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "FAIL: version must be X.Y.Z"; exit 1; }
mkdir -p "$OUT"

# tag -> (binary suffix, wheel platform tag, optional extension)
MAPPING=(
    "macosx_11_0_arm64|darwin-arm64|"
    "macosx_10_9_x86_64|darwin-amd64|"
    "manylinux2014_x86_64|linux-amd64|"
    "manylinux2014_aarch64|linux-arm64|"
    "win_amd64|windows-amd64|.exe"
)

for entry in "${MAPPING[@]}"; do
    IFS='|' read -r PLAT OSARC EXT <<< "$entry"
    BIN="$BINDIR/fusion-console-${OSARC}${EXT}"
    [[ -f "$BIN" ]] || { echo "FAIL: missing $BIN (run make cross)"; exit 1; }

    WORK="$(mktemp -d)"
    trap 'rm -rf "$WORK"' EXIT
    PKG="$WORK/fusion_console"
    mkdir -p "$PKG/bin"
    cp "$PKG_SRC/fusion_console/__init__.py" "$PKG/__init__.py"
    cp "$BIN" "$PKG/bin/fusion-console${EXT}"
    chmod +x "$PKG/bin/fusion-console${EXT}"

    WHEEL="$OUT/fusion_console-${VERSION}-py3-none-${PLAT}.whl"
    python3 - "$WORK" "$WHEEL" "$VERSION" "$PLAT" <<'EOF'
import base64, csv, hashlib, io, json, os, sys, time, zipfile

work, out, version, plat = sys.argv[1:5]
now = time.time()
records = []

def add(zf, arcname, data, mode=None):
    if isinstance(data, str):
        data = data.encode()
    zi = zipfile.ZipInfo(arcname, (2026, 1, 1, 0, 0, 0))
    zi.compress_type = zipfile.ZIP_DEFLATED
    zi.external_attr = ((mode if mode is not None else 0o644) | 0o100000) << 16
    zf.writestr(zi, data)
    digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
    records.append((arcname, "sha256=" + digest, len(data)))

with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zf:
    for root, _, files in os.walk(work):
        for f in files:
            p = os.path.join(root, f)
            arc = os.path.relpath(p, work)
            mode = 0o755 if f.startswith("fusion-console") else None
            add(zf, arc.replace(os.sep, "/"), open(p, "rb").read(), mode)
    dist = "fusion_console-" + version
    add(zf, f"{dist}.dist-info/METADATA", f"""Metadata-Version: 2.1
Name: fusion-console
Version: {version}
Summary: Browser console for cloud/local LLM chat and headless coding agents (single Go binary)
Requires-Python: >=3.8
""".encode())
    add(zf, f"{dist}.dist-info/WHEEL", """Wheel-Version: 1.0
Generator: fusion-console pypi-build.sh
Root-Is-Purelib: false
Tag: py3-none-""" + plat + "\n")
    add(zf, f"{dist}.dist-info/entry_points.txt", b"""[console_scripts]
fusion-console = fusion_console:main
""")
    buf = io.StringIO()
    csv.writer(buf, lineterminator="\n").writerows(records + [(f"{dist}.dist-info/RECORD", "", "")])
    add(zf, f"{dist}.dist-info/RECORD", buf.getvalue().encode())
print("built", out)
EOF
done
echo "PYPI WHEELS OK -> $OUT"
ls -1 "$OUT"
