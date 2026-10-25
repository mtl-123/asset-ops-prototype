const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

function bootAgentUI() {
  let pageHtml = '';
  let modal = null;
  const toasts = [];
  const A = {
    state: { page: 'agents' },
    pages: {},
    actions: {},
    root: { addEventListener() { } },
    escape: value => String(value == null ? '' : value).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
    })[c]),
    badge: (text, tone) => `<span class="badge badge-${tone}">${text}</span>`,
    button: (label, action, primary, attrs) => `<button data-action="${action}" ${attrs || ''}>${label}</button>`,
    panel: (title, body, extra) => `<section><h2>${title}</h2>${body}${extra || ''}</section>`,
    table: (headers, rows) => `<table data-headers="${headers.join('|')}">${rows.map(row => `<tr>${row.map(cell => `<td>${cell}</td>`).join('')}</tr>`).join('')}</table>`,
    metric: (label, value, foot) => `<div class="metric">${label}=${value} ${foot}</div>`,
    modal: (title, body, footer) => { modal = { title, body, footer }; },
    toast: message => { toasts.push(message); },
    render: () => {
      if (A.state.page === 'agents') pageHtml = A.pages.agents();
    }
  };
  const hosts = [{
    host_id: 'host-1', hostname: 'pve-1', agent_version: '0.1.0', os_name: 'Debian',
    collected_at: '2026-10-25T10:00:00Z', cpu_percent: 12.5, memory_percent: 42,
    disk_percent: 55, uptime_seconds: 90000, users_online: 2, users_stale: 1,
    pve_available: true, pve_vms: 1, pve_containers: 1,
    docker_available: true, docker_running: 1, docker_total: 2,
    k8s_available: true, k8s_nodes: 1, k8s_pods: 3,
    virtualization: 'physical', hypervisor_detail: 'none'
  }];
  const detail = {
    summary: hosts[0],
    payload: {
      schema_version: 1, agent_version: '0.1.0', host_id: 'host-1',
      collected_at: '2026-10-25T10:00:00Z',
      host: {
        hostname: 'pve-1', os_name: 'Debian', os_version: '12', kernel_version: '6.8',
        arch: 'x86_64', uptime_seconds: 90000,
        cpu: { usage_percent: 12.5, logical_cores: 8, physical_cores: 4 },
        memory: { total_bytes: 1024, used_bytes: 430, usage_percent: 42 },
        disks: [{ mount: '/', filesystem: 'ext4', total_bytes: 100, used_bytes: 55, usage_percent: 55 }],
        network: { interfaces: [{ name: 'vmbr0', received_bytes: 100, transmitted_bytes: 200, packets_received: 1, packets_transmitted: 2 }] }
      },
      users: {
        logged_in_count: 2, session_count: 2, stale_count: 1, stale_days: 90,
        lastlog_available: true,
        logged_in: [{ user: 'ops', tty: 'pts/0', host: '10.0.0.5', login_time: '2026-10-25 09:00' }],
        stale: [{ user: 'old-user', last_login: '2024-01-01T00:00:00Z', days_inactive: 600, never: false }],
        never_logged_in: ['never-user']
      },
      platforms: {
        pve: {
          available: true, node: 'pve-1', version: 'pve-manager/8.2', error: null,
          vms: [{ vmid: 101, name: 'win-1', status: 'running', os_family: 'windows', ostype: 'win11', ip_addresses: ['10.0.0.4'], cpu_cores: 4, memory_bytes: 1024, disk_bytes: 2048, connection: { protocol: 'rdp', host: '10.0.0.4', port: 3389 } }],
          containers: [{ vmid: 200, name: 'ct-1', status: 'running', os_family: 'linux', ostype: 'ubuntu', ip_addresses: ['10.0.0.5'], connection: { protocol: 'ssh', host: '10.0.0.5', port: 22 } }]
        },
        docker: { available: true, version: '27', containers_total: 2, containers_running: 1, containers: [{ id: 'abcdef012345', name: 'nginx', image: 'nginx:latest', state: 'running', status: 'Up', ports: '80/tcp', cpu_percent: 1.2, memory_usage: '10MiB / 1GiB' }] },
        kubernetes: { available: true, context: 'test', node_count: 1, pods_running: 2, pods_pending: 1, pods_failed: 0, nodes: [{ name: 'node-1', ready: true, roles: ['control-plane'], kubelet_version: 'v1.30', cpu_cores: 8, memory_bytes: 1024 }], unhealthy_pods: [{ namespace: 'prod', name: 'api-1', phase: 'Pending', restarts: 2, reason: 'ImagePullBackOff' }] }
      }
    }
  };
  const QiyunAPI = {
    agentHosts: async () => ({ items: hosts }),
    agentHost: async () => detail,
    agentEnrollment: async () => ({
      server_url: 'http://127.0.0.1:8765',
      token: 'agent-token-example',
      token_file: 'data/agent.token',
      download_url: '/downloads/qiyun-agent-linux-amd64.zip',
      platform: 'linux-amd64',
      local_only: true
    })
  };
  const window = { AssetDemo: A, QiyunAPI, location: { origin: 'http://127.0.0.1:8765' } };
  const document = { hidden: false, activeElement: null, addEventListener() { }, getElementById() { return null; } };
  const localStorage = { getItem() { return null; }, setItem() { } };
  const context = vm.createContext({
    window, document, localStorage, console, Promise, Number, String, Array, Date, Math, JSON,
    setTimeout, clearTimeout, setInterval: () => 0, clearInterval() { }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'agent-ui.js'), 'utf8'), context);
  return {
    A,
    QiyunAPI,
    get pageHtml() { return pageHtml; },
    get modal() { return modal; },
    toasts,
    hosts,
    reset() { pageHtml = ''; modal = null; }
  };
}

const tick = () => new Promise(resolve => setTimeout(resolve, 10));

test('Agent 页面加载主机列表并渲染平台摘要', async () => {
  const ui = bootAgentUI();
  const initial = ui.A.pages.agents();
  assert.match(initial, /正在读取|还没有 Agent/);
  await tick();
  assert.match(ui.pageHtml, /pve-1/);
  assert.match(ui.pageHtml, /PVE/);
  assert.match(ui.pageHtml, /Docker/);
  assert.match(ui.pageHtml, /Kubernetes|K8s/);
  assert.match(ui.pageHtml, /全局资源水位/);
  assert.match(ui.pageHtml, /数据新鲜度/);
  assert.match(ui.pageHtml, /节点清单/);
  assert.match(ui.pageHtml, /实时刷新中/);
  assert.match(ui.pageHtml, /平台覆盖/);
});

test('Agent 页面无数据时提示安装命令', async () => {
  const ui = bootAgentUI();
  ui.QiyunAPI.agentHosts = async () => ({ items: [] });
  ui.A.agentState.loaded = false;
  ui.A.agentState.error = '';
  ui.A.pages.agents();
  await tick();
  assert.match(ui.pageHtml, /还没有 Agent 上报/);
  assert.match(ui.pageHtml, /agent\.token/);
  assert.match(ui.pageHtml, /qiyun-agent --server/);
});

test('接入弹窗给出下载、令牌、命令和回环警告', async () => {
  const ui = bootAgentUI();
  await ui.A.actions['agent-onboard']();
  assert.ok(ui.modal);
  assert.match(ui.modal.title, /接入/);
  assert.match(ui.modal.body, /qiyun-agent-linux-amd64\.zip/);
  assert.match(ui.modal.body, /agent-token-example/);
  assert.match(ui.modal.body, /--install/);
  assert.match(ui.modal.body, /systemctl is-enabled/);
  assert.match(ui.modal.body, /\/etc\/qiyun-agent\.toml/);
  assert.match(ui.modal.body, /无法通过 127\.0\.0\.1 访问/);
});

test('Agent 详情展示 PVE 虚拟机、用户与容器', async () => {
  const ui = bootAgentUI();
  await ui.A.actions['agent-detail']({ dataset: { hostId: 'host-1' } });
  await tick();
  assert.ok(ui.modal);
  assert.match(ui.modal.title, /pve-1/);
  assert.match(ui.modal.body, /win-1/);
  assert.match(ui.modal.body, /rdp:\/\/10\.0\.0\.4:3389/);
  assert.match(ui.modal.body, /nginx/);
  assert.match(ui.modal.body, /old-user/);
  assert.match(ui.modal.body, /ImagePullBackOff/);
  assert.match(ui.modal.body, /系统与采集/);
  assert.match(ui.modal.body, /CPU \/ 内存 \/ 负载/);
  assert.match(ui.modal.body, /网络/);
  assert.match(ui.modal.body, /原始 JSON/);
});
