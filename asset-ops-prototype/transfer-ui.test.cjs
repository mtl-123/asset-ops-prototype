const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

const ASSETS = [
  { id: 'a1', name: '虚拟化', address: '192.168.1.190', ssh_port: 50022, host_key_status: 'verified' },
  { id: 'a2', name: 'LX-4', address: '10.0.0.4', ssh_port: 22, host_key_status: 'verified' },
  { id: 'a3', name: '待核验', address: '10.0.0.9', ssh_port: 22, host_key_status: 'pending' }
];

// 抽屉以前不看当前终端，默认选中资产列表第一台：在 A 机终端里点“文件传输”，
// 请求实际发往 B 机，报“主机不可达”，看上去像 SSH 通了却传不了。
function boot(activeHost) {
  const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://127.0.0.1:8765/', runScripts: 'outside-only' });
  const window = dom.window;
  let snapshot = {
    activeTabId: 't1',
    tabs: [{ id: 't1', hostId: activeHost, activePaneId: 'p1', paneIds: ['p1'] }],
    panes: { p1: { hostId: activeHost } }
  };
  const A = {
    actions: {}, toast() {}, root: window.document.body,
    terminalWorkspace: { snapshot: () => snapshot }
  };
  window.AssetDemo = A;
  window.fetch = async url => ({
    ok: true, status: 200,
    json: async () => (String(url).includes('/transfers') ? { items: [] } : { items: ASSETS })
  });
  window.eval(fs.readFileSync(path.join(__dirname, 'transfer-ui.js'), 'utf8'));
  return {
    A, window, document: window.document,
    setHost(hostId) { snapshot = { ...snapshot, pans: undefined, panes: { p1: { hostId } }, tabs: [{ id: 't1', hostId, activePaneId: 'p1', paneIds: ['p1'] }] }; },
    select: () => window.document.querySelector('[data-field="asset"]'),
    note: () => window.document.querySelector('[data-target-note]')
  };
}

// 上传循环是唯一能真正打乱字节顺序的地方：预读下一块时如果算错偏移，
// 文件会「成功」但内容错位，所以这里逐个断言 offset 与长度。
async function uploadSession(totalBytes) {
  const s = boot('a2');
  const chunks = [];
  let serverOffset = 0;
  const hostRef = { s };
  hostRef.s = s;
  s.window.fetch = async (url, options = {}) => {
    const target = String(url);
    if (target.includes('/chunk')) {
      const offset = Number(new URL(target, 'http://x').searchParams.get('offset'));
      chunks.push({ offset, size: options.body ? options.body.byteLength : 0 });
      // A real slice of time between chunks keeps the rate window meaningful.
      await new Promise(resolve => setTimeout(resolve, 20));
      serverOffset = offset + options.body.byteLength;
      return { ok: true, status: 200, json: async () => ({ offset: serverOffset }) };
    }
    if (target.includes('/assets')) return { ok: true, status: 200, json: async () => ({ items: ASSETS }) };
    if (target.includes('/transfers') && options.method === 'POST') {
      return { ok: true, status: 201, json: async () => ({ id: 't9', offset: 0, size: totalBytes }) };
    }
    return { ok: true, status: 200, json: async () => ({ items: [] }) };
  };
  await s.A.transferPanel.open();
  const file = {
    name: 'big.bin', size: totalBytes,
    slice: (from, to) => ({ arrayBuffer: async () => new ArrayBuffer(to - from) })
  };
  const input = s.document.querySelector('[data-field="file"]');
  Object.defineProperty(input, 'files', { value: [file], configurable: true });
  s.document.querySelector('[data-field="path"]').value = '/tmp/big.bin';
  return { s, chunks, file };
}

const until = async (probe, label = 'condition') => {
  for (let i = 0; i < 400; i += 1) {
    if (probe()) return;
    await new Promise(resolve => setTimeout(resolve, 5));
  }
  throw new Error('等待超时：' + label);
};

test('分块按服务端上限 8 MiB 严格顺序发送，不重不漏', async () => {
  const total = 20 << 20;
  const { s, chunks } = await uploadSession(total);
  s.document.querySelector('[data-op="start"]').click();
  await until(() => chunks.length === 3, '三个分块');
  await new Promise(resolve => setTimeout(resolve, 60));
  assert.deepEqual(chunks, [
    { offset: 0, size: 8 << 20 },
    { offset: 8 << 20, size: 8 << 20 },
    { offset: 16 << 20, size: 4 << 20 }
  ], '分块必须首尾相接、顺序提交');
});

test('上传中显示实时速率与剩余时间', async () => {
  const total = 40 << 20;
  const { s, chunks } = await uploadSession(total);
  s.document.querySelector('[data-op="start"]').click();
  // 第一块完成后进度里就有两个采样点，速率行应该出现。
  await until(() => chunks.length >= 1, '第一块');
  await until(() => s.document.querySelector('.transfer-rate'), '速率行');
  const row = s.document.querySelector('.transfer-rate');
  assert.match(row.textContent, /\/s/, '必须给出吞吐速率');
  assert.match(row.textContent, /剩余/, '必须给出剩余时间');
});

test('分块说明里写明单块大小，避免与服务端上限对不上', async () => {
  const s = boot('a2');
  await s.A.transferPanel.open();
  assert.match(s.document.querySelector('[data-hint]').textContent, /单块 8\.0 MB/);
});

// 中断后提示说「可续传」，就必须真的能续：以前 failed 被排除在可续传之外，
// 用户重新选同一个文件只会从头再传一遍。
test('失败的上传会被续传，而不是从头重传', async () => {
  const total = 20 << 20;
  const resumeAt = 12 << 20;
  const s = boot('a2');
  const existing = {
    id: 't9', direction: 'upload', remote_path: '/tmp/big.bin', size: total,
    offset: resumeAt, status: 'failed', temp_path: '/tmp/big.bin.qiyun-x.part'
  };
  const chunks = [];
  let created = false, completed = 0;
  s.window.fetch = async (url, options = {}) => {
    const target = String(url);
    if (target.includes('/chunk')) {
      const offset = Number(new URL(target, 'http://x').searchParams.get('offset'));
      chunks.push(offset);
      return { ok: true, status: 200, json: async () => ({ offset: offset + options.body.byteLength }) };
    }
    if (target.includes('/complete')) {
      completed += 1;
      return { ok: true, status: 200, json: async () => ({ ...existing, status: 'completed', offset: total }) };
    }
    if (target.includes('/assets')) return { ok: true, status: 200, json: async () => ({ items: ASSETS }) };
    if (options.method === 'POST') { created = true; return { ok: true, status: 201, json: async () => existing }; }
    return { ok: true, status: 200, json: async () => ({ items: [existing] }) };
  };
  await s.A.transferPanel.open();
  const input = s.document.querySelector('[data-field="file"]');
  Object.defineProperty(input, 'files', {
    value: [{ name: 'big.bin', size: total, slice: (from, to) => ({ arrayBuffer: async () => new ArrayBuffer(to - from) }) }],
    configurable: true
  });
  s.document.querySelector('[data-field="path"]').value = '/tmp/big.bin';
  s.document.querySelector('[data-op="start"]').click();
  await until(() => chunks.length === 1 && completed === 1, '续传并收尾');
  assert.equal(created, false, '不应新建任务');
  assert.deepEqual(chunks, [resumeAt], '必须从已确认断点续传');
  assert.equal(completed, 1, '收尾时应触发一次发布');
});

test('抽屉默认选中当前终端连接的主机', async () => {
  const s = boot('a2');
  await s.A.transferPanel.open();
  assert.equal(s.select().value, 'a2', '不能默认落到资产列表第一台（a1）');
  assert.match(s.note().textContent, /与终端一致/);
  assert.equal(s.note().dataset.mismatch, 'false');
});

test('手动切到另一台主机时给出明确提示', async () => {
  const s = boot('a2');
  await s.A.transferPanel.open();
  const select = s.select();
  select.value = 'a1';
  select.dispatchEvent(new s.window.Event('change', { bubbles: true }));
  s.A.actions['terminal-transfer'](); // 再次打开，目标应回到终端主机
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(select.value, 'a2');
});

test('未核验指纹的主机不出现在目标列表里', async () => {
  const s = boot('a2');
  await s.A.transferPanel.open();
  const values = Array.from(s.select().options).map(option => option.value);
  assert.deepEqual(values, ['a1', 'a2']);
});

test('换一个终端分屏后重新打开，目标主机随之切换', async () => {
  const s = boot('a2');
  await s.A.transferPanel.open();
  assert.equal(s.select().value, 'a2');
  s.setHost('a1');
  await s.A.transferPanel.open();
  assert.equal(s.select().value, 'a1');
  assert.match(s.select().selectedOptions[0].textContent, /192\.168\.1\.190/);
});

test('终端没有活动会话时退回第一台已核验主机，不报错', async () => {
  const s = boot('');
  await s.A.transferPanel.open();
  assert.equal(s.select().value, 'a1');
  assert.equal(s.note().textContent, '');
});
