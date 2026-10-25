#!/bin/sh
set -eu
mkdir -p /tmp/fakebin
for command in curl; do
  printf '#!/bin/sh\nexit 0\n' > "/tmp/fakebin/$command"
  chmod +x "/tmp/fakebin/$command"
done
cat > /tmp/fakebin/systemctl <<'EOF'
#!/bin/sh
case "$1" in
  is-enabled) echo enabled ;;
  is-active) exit 0 ;;
  --version) echo "systemd 255" ;;
esac
exit 0
EOF
chmod +x /tmp/fakebin/systemctl
export PATH="/tmp/fakebin:$PATH"
/src/qiyun-agent --install \
  --server https://ops.example.com \
  --token test-agent-token \
  --interval 30 \
  --stale-days 120 \
  --network-interfaces eth0
echo '--- config ---'
cat /etc/qiyun-agent.toml
echo '--- unit ---'
cat /etc/systemd/system/qiyun-agent.service
test -x /usr/local/bin/qiyun-agent
grep -q 'server = "https://ops.example.com"' /etc/qiyun-agent.toml
grep -q 'network_interfaces = \["eth0"\]' /etc/qiyun-agent.toml
grep -q 'ExecStart=/usr/local/bin/qiyun-agent --config "/etc/qiyun-agent.toml"' /etc/systemd/system/qiyun-agent.service
echo '--- second install (idempotent) ---'
/src/qiyun-agent --install \
  --server https://ops.example.com \
  --token test-agent-token \
  --interval 30 \
  --stale-days 120 \
  --network-interfaces eth0
echo INSTALL_TEST_OK