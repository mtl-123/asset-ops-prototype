#!/bin/bash
set -euo pipefail
cd /mnt/g/asset-ops-prototype/asset-ops-prototype/agent
chmod +x qiyun-agent build/qiyun-linux
rm -rf /tmp/qiyun-e2e /tmp/qiyun-e2e.log /tmp/qiyun-hosts.json /tmp/qiyun-detail.json /tmp/qiyun-host-id
QIYUN_AGENT_TOKEN=test-agent-token build/qiyun-linux \
  -addr 127.0.0.1:8799 -data /tmp/qiyun-e2e \
  -web /mnt/g/asset-ops-prototype/asset-ops-prototype -metrics-interval 0 \
  >/tmp/qiyun-e2e.log 2>&1 &
server=$!
cleanup() { kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true; }
trap cleanup EXIT
for _ in $(seq 1 40); do
  if curl -fsS http://127.0.0.1:8799/healthz >/dev/null 2>&1; then break; fi
  sleep 0.5
done
curl -fsS http://127.0.0.1:8799/healthz >/dev/null
./qiyun-agent --once --server http://127.0.0.1:8799 --token test-agent-token
curl -fsS http://127.0.0.1:8799/api/v1/agent/hosts > /tmp/qiyun-hosts.json
id=$(python3 -c 'import json;print(json.load(open("/tmp/qiyun-hosts.json"))["items"][0]["host_id"])')
curl -fsS "http://127.0.0.1:8799/api/v1/agent/hosts/$id" > /tmp/qiyun-detail.json
python3 build/check-e2e.py
echo '--- server log ---'
tail -8 /tmp/qiyun-e2e.log