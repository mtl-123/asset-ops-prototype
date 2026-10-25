#!/bin/sh
set -eu
if [ ! -f /usr/lib/crti.o ]; then
  /sbin/apk add --no-cache --repository https://mirrors.ustc.edu.cn/alpine/v3.22/main musl-dev
fi
cd /src
export CARGO_TARGET_DIR=/tmp/qiyun-target
export CARGO_HTTP_TIMEOUT=600
export CARGO_NET_RETRY=5
cargo test --release
cargo build --release
cargo rustc --release -- -C target-feature=+crt-static
cp /tmp/qiyun-target/release/qiyun-agent /src/qiyun-agent
ls -lh /src/qiyun-agent