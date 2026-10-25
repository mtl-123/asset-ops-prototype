#!/bin/sh
set -eu
if [ ! -f /usr/lib/crti.o ]; then
  /sbin/apk add --no-cache --repository https://mirrors.ustc.edu.cn/alpine/v3.22/main musl-dev
fi
cd /src
export CARGO_TARGET_DIR=/tmp/qiyun-test
export CARGO_HTTP_TIMEOUT=600
cargo test --release