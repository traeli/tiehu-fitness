#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: build-macos.sh <whisper.xcframework>" >&2
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd)
framework="$1/macos-arm64_x86_64/whisper.framework"

if [ ! -f "$framework/Headers/whisper.h" ]; then
  echo "macOS whisper.framework is invalid: $framework" >&2
  exit 3
fi

build_dir="$project_dir/.native-build/local-transcription"
mkdir -p "$build_dir"
binary="$build_dir/tiehu-local-whisper"

clang++ \
  -std=c++17 \
  -O2 \
  -arch arm64 \
  -arch x86_64 \
  -mmacosx-version-min=13.0 \
  -I "$framework/Headers" \
  "$script_dir/main.cpp" \
  -F "$(dirname "$framework")" \
  -framework whisper \
  -Wl,-rpath,@executable_path \
  -o "$binary"

for arch in arm64 x64; do
  output_dir="$project_dir/plugin/native/darwin-$arch"
  mkdir -p "$output_dir"
  cp "$binary" "$output_dir/tiehu-local-whisper"
  rm -rf "$output_dir/whisper.framework"
  cp -R "$framework" "$output_dir/whisper.framework"
  chmod 755 "$output_dir/tiehu-local-whisper"
  codesign --force --sign - "$output_dir/whisper.framework/Versions/A/whisper"
  codesign --force --sign - "$output_dir/tiehu-local-whisper"
done
