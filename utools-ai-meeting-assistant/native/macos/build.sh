#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd)
module_cache_dir="$project_dir/.native-build/module-cache"
mkdir -p "$module_cache_dir"
export CLANG_MODULE_CACHE_PATH="$module_cache_dir"

build_target() {
  target_arch=$1
  output_arch=$2
  output_dir="$project_dir/plugin/native/darwin-$output_arch"
  mkdir -p "$output_dir"
  swiftc \
    -O \
    -parse-as-library \
    -target "$target_arch-apple-macosx13.0" \
    -framework AVFAudio \
    -framework CoreMedia \
    -framework ScreenCaptureKit \
    -Xlinker -sectcreate \
    -Xlinker __TEXT \
    -Xlinker __info_plist \
    -Xlinker "$script_dir/Info.plist" \
    "$script_dir/main.swift" \
    -o "$output_dir/tiehu-system-audio"
  chmod 755 "$output_dir/tiehu-system-audio"
  codesign --force --sign - "$output_dir/tiehu-system-audio"
}

build_target arm64 arm64
build_target x86_64 x64
