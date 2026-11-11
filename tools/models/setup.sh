#!/bin/sh
set -eu

model_jar=$1
model_checksum=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
check_jar() {
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s  %s\n' "$model_checksum" "$1" | sha256sum -c - >/dev/null 2>&1
    else
        printf '%s  %s\n' "$model_checksum" "$1" | shasum -a 256 -c - >/dev/null 2>&1
    fi
}

mkdir -p "$(dirname "$model_jar")"
if [ -f "$model_jar" ] && check_jar "$model_jar"; then
    exit 0
fi
model_download=$(mktemp "${model_jar}.XXXXXX")
trap 'rm -f "$model_download"' EXIT HUP INT TERM
curl --fail --location --retry 2 --connect-timeout 15 --max-time 180 \
    --output "$model_download" \
    https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
if ! check_jar "$model_download"; then
    printf '%s\n' 'TLC jar checksum did not match.' >&2
    exit 1
fi
mv "$model_download" "$model_jar"
