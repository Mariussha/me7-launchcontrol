#!/usr/bin/env bash
# Downloads the third-party tools that get embedded into the exe, from pinned
# upstream sources, and verifies their SHA-256. Run once before building.
set -euo pipefail

ME7L_COMMIT=89d0ff9a3908226efa1995da26e9148f766302f7
ME7SUM_VERSION=v1.1.7

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Fetching ME7Info + defs (nyetwurk/ME7L @ ${ME7L_COMMIT:0:7})..."
curl -fsSL "https://github.com/nyetwurk/ME7L/archive/${ME7L_COMMIT}.zip" -o "$tmp/me7l.zip"
unzip -q "$tmp/me7l.zip" -d "$tmp"
src="$tmp/ME7L-${ME7L_COMMIT}"
mkdir -p assets/defs
cp "$src/bin/ME7Info.exe" assets/ME7Info.exe
cp "$src/defs/dist/me7_std.map" assets/defs/me7_std.map
cp "$src/defs/dist/me7_alias.map" assets/defs/me7_alias.map

echo "Fetching ME7Sum ${ME7SUM_VERSION} (nyetwurk/ME7Sum)..."
curl -fsSL "https://github.com/nyetwurk/ME7Sum/releases/download/${ME7SUM_VERSION}/me7sum-${ME7SUM_VERSION}-win.zip" -o "$tmp/me7sum.zip"
unzip -q "$tmp/me7sum.zip" -d "$tmp/me7sum"
cp "$tmp/me7sum/me7sum.exe" assets/me7sum.exe

echo "Verifying checksums..."
sha256sum -c scripts/tools.sha256
echo "OK - tools are in assets/"
