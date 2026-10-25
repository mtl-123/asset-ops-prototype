#!/bin/bash
set -u
cd /mnt/g/asset-ops-prototype/asset-ops-prototype/agent
rm -f /tmp/qiyun-rate-*.json /tmp/rate-agent.log
python3 build/mock_server.py >/tmp/mock.log 2>&1 &
server=$!
./qiyun-agent --server http://127.0.0.1:8808 --token test-token --interval 5 --network-interfaces eth1 >/tmp/rate-agent.log 2>&1 &
agent=$!
sleep 22
kill "$agent" 2>/dev/null || true
kill "$server" 2>/dev/null || true
wait "$agent" 2>/dev/null || true
wait "$server" 2>/dev/null || true
latest=$(ls -1 /tmp/qiyun-rate-*.json 2>/dev/null | sort -V | tail -1)
echo "reports=$(ls -1 /tmp/qiyun-rate-*.json 2>/dev/null | wc -l) latest=$latest"
if [ -n "$latest" ]; then
  python3 build/inspect_network.py "$latest"
fi