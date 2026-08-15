#!/usr/bin/env bash
set -euo pipefail

app_name="$1"
build_dir="$2"
output_dir="$3"
payload_dir="$4"

case "$(uname -m)" in
  x86_64) appimage_arch="x86_64" ;;
  aarch64|arm64) appimage_arch="aarch64" ;;
  *) echo "unsupported AppImage architecture: $(uname -m)" >&2; exit 1 ;;
esac

app_dir="${build_dir}/${app_name}-${appimage_arch}.AppDir"
linuxdeploy="${build_dir}/linuxdeploy-${appimage_arch}.AppImage"
generated="${build_dir}/${app_name}-${appimage_arch}.AppImage"
output="${output_dir}/${app_name}-${appimage_arch}.AppImage"

test -d "$app_dir"
test -x "$linuxdeploy"
test -f "${payload_dir}/payload.json"

rm -rf "${app_dir}/usr/bin/payload"
mkdir -p "${app_dir}/usr/bin/payload"
cp -R "${payload_dir}/." "${app_dir}/usr/bin/payload/"

rm -f "$generated"
(
  cd "$build_dir"
  NO_STRIP=1 OUTPUT="$(basename "$generated")" \
    "$linuxdeploy" --appimage-extract-and-run --appdir "$app_dir" --output appimage
)
mv -f "$generated" "$output"
