(function () {
  'use strict';
  const A = window.AssetDemo;
  const E = A.escape;
  const money = n => '¥' + Number(n).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  const b = (label, action, id, primary = false, extra = '') => A.button(label, action, primary, (id ? 'data-id="' + E(id) + '"' : '')+' '+extra);
  const notice = text => '<div class="notice">' + text + '</div>';
  const cell = (title, sub) => '<strong>' + E(title) + '</strong><div class="small muted">' + E(sub || '') + '</div>';
  const toolbar = (left, right) => '<div class="row between" style="margin-bottom:20px;gap:12px;flex-wrap:wrap"><div class="small muted">' + left + '</div><div class="row">' + right + '</div></div>';
  const kv = (label, value) => '<div><div class="small muted">' + E(label) + '</div><div style="margin-top:5px;font-weight:600">' + E(value) + '</div></div>';
  const field = (name, label, type = 'text', value = '', extra = '') => '<div class="field"><label for="biz-' + name + '">' + E(label) + '</label><input id="biz-' + name + '" name="' + name + '" type="' + type + '" value="' + E(value) + '" ' + extra + '></div>';
  const select = (name, label, options) => '<div class="field"><label for="biz-' + name + '">' + E(label) + '</label><select id="biz-' + name + '" name="' + name + '">' + options.map(o => '<option value="' + E(o[0]) + '">' + E(o[1]) + '</option>').join('') + '</select></div>';
  const read = name => { const el = A.root.querySelector('[name="' + name + '"]'); return el ? el.value.trim() : ''; };
  const now = '2026-10-08';
  const state = {
    purchases: [
      { id: 'PO-202610-001', title: 'ThinkPad T14 Gen 5', supplier: '联想企业购', channel: '品牌直营', kind: 'unique', qty: 4, price: 6800, status: 'approved', date: '2026-10-02', buyer: '陈慧', warehouse: '总部 IT 仓', serials: [], trace: ['10-02 09:10 · 陈慧提交采购申请', '10-02 14:32 · 企业微信审批通过（样例）', '10-03 10:05 · 采购登记完成，等待到货'] },
      { id: 'PO-202609-028', title: 'H3C S5130S 交换机', supplier: '华三授权服务商', channel: '授权经销商', kind: 'unique', qty: 2, price: 3500, status: 'received', date: '2026-09-25', buyer: '陈慧', warehouse: '总部 IT 仓', serials: ['H3C26A001', 'H3C26A002'], trace: ['09-25 10:00 · 采购审批通过', '09-26 16:20 · 实物验收 2 台，序列号登记完成', '09-26 16:23 · IN-202609-028 确认，台账增加 2 台'] },
      { id: 'PO-202610-003', title: 'Samsung 1TB 固态硬盘', supplier: '京东企业业务', channel: '电商采购', kind: 'batch', qty: 10, price: 620, status: 'pending', date: '2026-10-07', buyer: '陈慧', warehouse: '总部 IT 仓', serials: [], trace: ['10-07 11:08 · 提交企业微信采购审批（样例）'] }
    ],
    stock: { laptop: 2, monitor: 1, ssd: 10 },
    batches: [],
    requests: [
      { id: 'REQ-202610-018', person: '李然', department: '产品研发部', item: 'laptop', title: 'ThinkPad T14', qty: 1, purpose: '新员工入职设备', stage: 0, trace: ['10-08 09:12 · 李然提交领用申请'] },
      { id: 'REQ-202610-017', person: '周杰', department: '平台工程部', item: 'ssd', title: '1TB 固态硬盘', qty: 2, purpose: '测试服务器扩容', stage: 1, trace: ['10-07 14:03 · 周杰提交领用申请', '10-08 08:40 · 部门主管审批通过'] },
      { id: 'REQ-202610-016', person: '王璐', department: '财务部', item: 'monitor', title: 'Dell 27 英寸显示器', qty: 1, purpose: '工作设备更换', stage: 2, trace: ['10-07 10:26 · 王璐提交领用申请', '10-07 13:05 · 部门主管审批通过', '10-07 15:20 · 行政审批通过，等待实物交接'] }
    ],
    inventoryTab: 'quarter',
    plans: [
      { id: 'CHK-2026-Q4', title: '2026 年第四季度盘点', kind: 'quarter', date: '2026-10-15', owner: '赵敏', rows: [
        { id: 'IT-0001', title: 'ThinkPad T14', location: '研发区 A-12', person: '张明', qty: 1, actual: 1, actualLocation: '研发区 A-12', actualPerson: '张明', status: 'matched' },
        { id: 'IT-0002', title: 'Dell U2723QE', location: '财务区 B-03', person: '李娜', qty: 1, actual: 1, actualLocation: '财务区 B-03', actualPerson: '李娜', status: 'matched' },
        { id: 'NET-0001', title: 'H3C S5130S', location: '机房 A / U22', person: '陈涛', qty: 1, actual: 1, actualLocation: '机房 A / U22', actualPerson: '陈涛', status: 'matched' },
        { id: 'IT-0004', title: 'HP EliteBook 840', location: '行政区 C-05', person: '王磊', qty: 1, actual: null, status: 'pending' },
        { id: 'BAT-001', title: '1TB 固态硬盘', location: '总部 IT 仓', person: '赵敏', qty: 10, actual: 9, actualLocation: '总部 IT 仓', actualPerson: '赵敏', status: 'difference' },
        { id: 'BAT-002', title: 'USB 有线鼠标', location: '总部 IT 仓', person: '赵敏', qty: 20, actual: null, status: 'pending' }
      ] },
      { id: 'CHK-2026-Y', title: '2026 年度资产盘点', kind: 'year', date: '2026-12-20', owner: '赵敏', planned: true, rows: [] }
    ],
    financeChecked: false,
    finance: [
      { id: 'FIN-001', item: 'H3C S5130S × 2', type: '验收入库', source: 'PO-202609-028 / L01', document: 'IN-202609-028 / L01', flow: 'FL-202609-028-01', asset: 'NET-0021 / NET-0022', ledger: 7000, book: 7000, voucher: '记-202609-112', reason: '采购成本、数量与凭证一致。' },
      { id: 'FIN-002', item: '1TB 固态硬盘 × 2', type: '耗材领用', source: 'REQ-202609-022 / L01', document: 'OUT-202609-022 / L01', flow: 'FL-202609-022-01', asset: 'BAT-202609-007', ledger: 1240, book: 1240, voucher: '记-202609-126', reason: '领用批次成本与财务凭证一致。' },
      { id: 'FIN-003', item: 'ThinkPad T14 × 4', type: '验收入库', source: 'PO-202609-020 / L01', document: 'IN-202609-020 / L01', flow: 'FL-202609-020-01', asset: 'IT-0110 — IT-0113', ledger: 27200, book: 26800, voucher: '记-202609-105', reason: '采购登记含 400 元运费，财务凭证未计入。需采购核对发票与原单费用分摊；本演示不自动选择会计处理。' }
    ]
  };
  const inventoryBaseline = state.plans[0].rows.map(r => ({ id: r.id, title: r.title, location: r.location, person: r.person, qty: r.qty }));
  const statusPurchase = p => p.status === 'received' ? A.badge('已入库', 'green') : p.status === 'approved' ? A.badge('待验收入库', 'blue') : p.status === 'registration' ? A.badge('待采购登记', 'blue') : A.badge('审批中', 'amber');
  const stageLabel = ['待上级审批', '待行政审批', '待确认出库', '已领用', '已归还'];
  const requestStatus = r => r.stage === -1 ? A.badge('已驳回', 'red') : A.badge(stageLabel[r.stage], r.stage < 2 ? 'amber' : r.stage === 2 ? 'blue' : 'green');
  const timeline = entries => '<div class="stack">' + entries.map((t, i) => '<div class="row" style="align-items:flex-start;gap:12px"><span style="display:grid;place-items:center;background:var(--soft);color:var(--accent-color);width:24px;height:24px;border-radius:50%;flex-shrink:0;font-size:11px">' + (i + 1) + '</span><div class="small" style="padding-top:3px">' + E(t) + '</div></div>').join('') + '</div>';
  function purchaseModal(p) {
    A.modal('采购单 · ' + p.id,
      '<div class="row between" style="margin-bottom:20px"><h3 style="margin:0">' + E(p.title) + '</h3>' + statusPurchase(p) + '</div>' +
      '<div class="grid-3" style="margin-bottom:24px">' + kv('供应商 / 购买渠道', p.supplier + ' / ' + p.channel) + kv('登记数量 / 单价', p.qty + ' / ' + money(p.price)) + kv('采购总额', money(p.qty * p.price)) + kv('资产计量方式', p.kind === 'unique' ? '单件资产 · 序列号追踪' : '批次物资 · 数量追踪') + kv('采购人 / 日期', p.buyer + ' / ' + p.date) + kv('接收仓库', p.warehouse) + '</div>' +
      (p.batchId ? notice('入库批次：'+E(p.batchId)+' · '+p.qty+' 件 · '+E(p.warehouse)) : '') + (p.serials.length ? notice('序列号：' + p.serials.map(E).join('、')) : '') +
      (p.assetIds?.length ? '<div style="margin-top:16px">' + notice('已建立 ' + p.assetIds.length + ' 张资产卡片：' + p.assetIds.map(E).join('、') + '。入库单：' + E(p.receipt) + ' / L01。') + '</div>' : '') +
      '<h4>原始单据与业务流水</h4>' + timeline(p.trace) + '<div style="margin-top:20px">' + notice('演示业务记录。审批只改变单据状态；验收确认后才增加库存。原始单据与流水保留，不覆盖历史。') + '</div>',
      p.status === 'approved' ? b('办理验收入库', 'biz-receive', p.id, true) : p.status === 'registration' ? b('办理采购登记', 'biz-register-po', p.id, true) : p.status === 'pending' ? b('模拟企业微信审批通过', 'biz-po-approve', p.id, true) : (p.assetIds?.length ? b('查看资产台账', 'biz-open-ledger', p.id, true) : b('关闭', 'biz-close')));
  }
  function currentPlan() { return state.plans.filter(p => p.kind === state.inventoryTab).slice(-1)[0]; }
  function inventoryBadge(row) {
    return row.status === 'matched' ? A.badge('账实一致', 'green') : row.status === 'pending' ? A.badge('待核验', 'neutral') : row.status === 'review' ? A.badge('差异处理中', 'blue') : A.badge('盘点差异', 'red');
  }
  Object.assign(A.pages, {
    procurement: () => {
      const sum = state.purchases.reduce((s, p) => s + p.qty * p.price, 0);
      const received = state.purchases.filter(p => p.status === 'received').reduce((s, p) => s + p.qty * p.price, 0);
      return toolbar('企微采购申请审批 → 采购登记 → 验收入库 → 台账更新', b('新增采购申请', 'biz-new-po', '', true)) +
        '<div class="grid-4">' + A.metric('采购总额', money(sum), state.purchases.length + ' 笔模块样例采购单') + A.metric('已验收入库', money(received), '按已确认采购单统计') + A.metric('待入库金额', money(sum - received), '包括审批中与待到货', 'amber') + A.metric('待审批', String(state.purchases.filter(p => p.status === 'pending').length), '企业微信审批 · 本地模拟', 'blue') + '</div>' +
        A.panel('采购与入库', A.table(['采购单 / 物品', '渠道 / 供应商', '数量 × 单价', '金额', '采购日期', '状态', '操作'], state.purchases.map(p => [cell(p.id, p.title), cell(p.channel, p.supplier), p.qty + ' × ' + money(p.price), '<strong>' + money(p.qty * p.price) + '</strong>', p.date, statusPurchase(p), b('查看', 'biz-po-detail', p.id) + (p.status === 'approved' ? ' ' + b('验收入库', 'biz-receive', p.id, true) : p.status === 'registration' ? ' ' + b('采购登记', 'biz-register-po', p.id, true) : '')])), A.badge('全部采购单 · ' + state.purchases.length, 'neutral')) +
        '<div class="grid-2">' + A.panel('单件资产', '<p class="muted small">电脑、服务器、交换机逐台登记序列号、资产编码、保修和存放位置。后续交接、维修、调拨都关联同一个资产身份。</p>' + '<div class="row">' + A.badge('序列号唯一', 'blue') + A.badge('可绑定 MAC / IP', 'neutral') + '</div>') + A.panel('批次物资', '<p class="muted small">硬盘、线缆和配件按批次管理数量与单位成本。支持部分领用和批次追溯；耗材领用成本与单件资产折旧分别核算。</p>' + '<div class="row">' + A.badge('批次数量台账', 'blue') + A.badge('领用成本可追溯', 'neutral') + '</div>') + '</div>';
    },
    circulation: () => {
      const waiting = state.requests.filter(r => r.stage >= 0 && r.stage < 3).length;
      return toolbar('上级审批 → 行政批复 → 实物交接 → 确认出库；当前为独立领用库存样例', b('发起领用申请', 'biz-new-request', '', true)) +
        '<div class="grid-4">' + A.metric('待处理申请', String(waiting), '本模块样例申请', 'amber') + A.metric('可领用笔记本', String(state.stock.laptop), '总部 IT 仓 · 台') + A.metric('可领用显示器', String(state.stock.monitor), '总部 IT 仓 · 台') + A.metric('可领用固态硬盘', String(state.stock.ssd), '批次 BAT-202610 · 块') + '</div>' +
        A.panel('领用与交接工作台', A.table(['申请单 / 用途', '申请人 / 部门', '领用物品', '状态', '操作'], state.requests.map(r => [cell(r.id, r.purpose), cell(r.person, r.department), E(r.title) + ' × ' + r.qty, requestStatus(r), b('记录', 'biz-request-detail', r.id) + (r.stage >= 0 && r.stage < 3 ? ' ' + b(r.stage === 0 ? '上级审批' : r.stage === 1 ? '行政审批' : '确认出库', 'biz-request-action', r.id, true) : r.stage === 3 ? ' ' + b('归还登记', 'biz-return', r.id) : '')])), A.badge('角色切换 · 演示', 'blue')) +
        A.panel('每一次交接，都有责任归属', '<div class="grid-3">' + kv('交接前', '申请人与用途、成本中心、审批链') + kv('交接时', '实领人、资产身份、数量与交接确认') + kv('交接后', '保管人、位置、业务流水与归还记录') + '</div><div style="margin-top:20px">' + notice('操作为本地业务模拟。正式系统可按角色授予上级、行政和出库权限；领用、归还、调拨均需单独记录，禁止直接覆盖保管人。') + '</div>');
    },
    inventory: () => {
      const p = currentPlan();
      const done = p.rows.filter(r => r.status !== 'pending').length;
      const diff = p.rows.filter(r => r.status === 'difference' || r.status === 'review').length;
      const tabs = '<div class="tabs"><button data-action="biz-inventory-tab" data-id="quarter" class="' + (state.inventoryTab === 'quarter' ? 'active' : '') + '">季度盘点</button><button data-action="biz-inventory-tab" data-id="year" class="' + (state.inventoryTab === 'year' ? 'active' : '') + '">年度盘点</button></div>';
      return toolbar(tabs, b('新建盘点计划', 'biz-new-inventory', '', true)) +
        '<div class="grid-4">' + A.metric('盘点范围', String(p.rows.length), '本计划样例条目 · 含批次') + A.metric('已核验', String(done), p.rows.length ? Math.round(done / p.rows.length * 100) + '% 完成' : '计划未启动') + A.metric('待核验', String(p.rows.length - done), '逐项核实数量、保管人与位置', 'amber') + A.metric('待关闭差异', String(diff), '原单更正后重新核对', diff ? 'amber' : 'green') + '</div>' +
        A.panel(E(p.title), '<div class="row between" style="margin-bottom:20px"><div class="small muted">' + E(p.id) + ' · 负责人 ' + E(p.owner) + ' · 截止 ' + E(p.date) + '</div>' + A.badge(p.planned ? '待启动' : done === p.rows.length && !diff ? '核验完成' : '进行中', p.planned ? 'neutral' : 'blue') + '</div>' +
          (p.planned ? '<div style="padding:45px 20px;text-align:center"><h3>年末关账前，完成一次账实核对</h3><p class="muted small">冻结盘点基准、逐项核验实物，再将差异交回责任部门处理。</p>' + b('启动样例盘点', 'biz-start-inventory', p.id, true) + '</div>' : A.table(['资产 / 批次', '账面保管人 / 位置', '账面数量', '实盘数量', '结果', '操作'], p.rows.map(r => [cell(r.id, r.title), cell(r.person, r.location), String(r.qty), r.actual === null ? '<span class="muted">待核验</span>' : String(r.actual), inventoryBadge(r), r.status === 'pending' ? b('实物核验', 'biz-check-item', r.id, true) : b(r.status === 'matched' ? '查看记录' : '查看差异', 'biz-check-detail', r.id)]))) +
          '<div style="margin-top:20px">' + notice('盘点采集只记录实盘结果，不自动修改台账。网络在线不等于实物在位；离线也不等于资产丢失。') + '</div>') +
        (state.plans.filter(x => x.kind === state.inventoryTab).length > 1 ? A.panel('同类盘点计划', A.table(['计划', '编号', '截止日期', '操作'], state.plans.filter(x => x.kind === state.inventoryTab).map(x => [E(x.title), E(x.id), E(x.date), b('切换到此计划', 'biz-select-plan', x.id)]))) : '');
    },
    finance: () => {
      const total = state.finance.reduce((s, r) => s + r.ledger, 0);
      const book = state.finance.reduce((s, r) => s + r.book, 0);
      return toolbar('财务期间 2026-09 · ' + (state.financeChecked ? '刚刚完成本地校验' : '样例校验时间 2026-10-08 09:30'), b('重新校验', 'biz-reconcile') + b('导出对账明细', 'biz-export-finance', '', true)) +
        '<div class="grid-4">' + A.metric('业务台账发生额', money(total), '3 笔业务样例 · 非期间费用') + A.metric('财务凭证发生额', money(book), '同范围、同期间、同币种 CNY') + A.metric('金额差异', money(total - book), '台账金额 − 财务金额', 'amber') + A.metric('待核对单据', String(state.finance.filter(r => r.ledger !== r.book).length), '财务只读 · 采购 / 行政更正', 'amber') + '</div>' +
        A.panel('台账与财务凭证核对', A.table(['业务单据 / 资产', '业务类型', '台账金额', '财务金额', '差额', '结果', '操作'], state.finance.map(r => [cell(r.document, r.item), E(r.type), money(r.ledger), money(r.book), '<span class="' + (r.ledger !== r.book ? 'text-amber' : '') + '">' + money(r.ledger - r.book) + '</span>', A.badge(r.ledger === r.book ? '一致' : '有差异', r.ledger === r.book ? 'green' : 'amber'), b('追溯明细', 'biz-finance-detail', r.id)])), A.badge('财务人员 · 只读', 'neutral')) +
        '<div class="grid-2">' + A.panel('一条完整的核对链', '<p class="small muted">每个金额都可以定位到实际业务与原始凭证。</p><div class="stack"><div>物品编码 / 批次 / 资产编码</div><div class="muted">↓</div><div>采购或领用原单 + 明细行</div><div class="muted">↓</div><div>入库 / 出库确认 + 流水号</div><div class="muted">↓</div><div>财务期间 + 会计凭证</div></div>') + A.panel('差异处理规则', '<div class="stack"><div>' + A.badge('01', 'blue') + ' 财务定位差异，查看原单和明细</div><div>' + A.badge('02', 'blue') + ' 采购 / 行政按权限提交关联更正</div><div>' + A.badge('03', 'blue') + ' 审批确认后形成新的调整流水</div><div>' + A.badge('04', 'green') + ' 重新校验，保留完整历史记录</div></div><p class="small muted" style="margin-top:24px">单件资产领用不直接计为费用。折旧、摊销和费用归属需要单独配置财务政策。</p>') + '</div>';
    }
  });
  Object.assign(A.actions, {
    'biz-close': () => A.closeModal(),
    'biz-po-detail': el => purchaseModal(state.purchases.find(p => p.id === el.dataset.id)),
    'biz-new-po': () => A.modal('新增采购申请', notice('填写申请草稿，提交后进入本地模拟的企业微信审批队列。审批通过后由采购负责人登记成交信息；不会发送消息或调用企业微信。') + '<div class="form-grid" style="margin-top:20px">' + field('title', '物品名称', 'text', '', 'placeholder="例如 ThinkPad T14" required maxlength="80"') + field('supplier', '拟采购供应商', 'text', '', 'placeholder="供应商名称" required maxlength="80"') + select('channel', '购买渠道', [['品牌直营', '品牌直营'], ['授权经销商', '授权经销商'], ['电商采购', '电商采购'], ['线下采购', '线下采购']]) + select('kind', '资产管理方式', [['unique', '单件资产 · 逐件追踪'], ['batch', '批次物资 · 数量追踪']]) + field('qty', '申请数量', 'number', '1', 'min="1" max="1000" step="1"') + field('price', '申请含税单价（元）', 'number', '', 'min="0.01" max="10000000" step="0.01"') + '</div>', b('提交模拟审批', 'biz-save-po', '', true)),
    'biz-save-po': () => {
      const title = read('title'), supplier = read('supplier'), qty = Number(read('qty')), price = Number(read('price'));
      if (!title || !supplier || !Number.isInteger(qty) || qty < 1 || qty > 1000 || !Number.isFinite(price) || price < 0.01 || price > 10000000 || Math.abs(price*100-Math.round(price*100))>0.000001) return A.toast('请填写物品、供应商、1–1000 的整数数量和有效单价');
      const id = 'PO-202610-' + String(state.purchases.length + 10).padStart(3, '0');
      state.purchases.unshift({ id, title, supplier, channel: read('channel'), kind: read('kind'), qty, price: Math.round(price * 100) / 100, status: 'pending', date: now, buyer: '陈慧', warehouse: '总部 IT 仓', serials: [], trace: ['10-08 · 陈慧提交采购申请（本地模拟）'] });
      A.closeModal(); A.render(); A.toast('采购申请已创建，等待模拟审批');
    },
    'biz-po-approve': el => {
      const p = state.purchases.find(p => p.id === el.dataset.id);
      if (!p || p.status !== 'pending') return A.toast('该采购单已处理');
      p.status = 'registration'; p.trace.push('10-08 · 企业微信审批通过（本地模拟），等待采购负责人登记，库存尚未增加');
      A.closeModal(); A.render(); A.toast('模拟审批已通过，进入采购登记环节');
    },
    'biz-register-po': el => {
      const p = state.purchases.find(x => x.id === el.dataset.id);
      if (!p || p.status !== 'registration') return A.toast('只有已审批的申请可以办理采购登记');
      A.modal('采购登记 · ' + p.id, '<div class="grid-3">' + kv('审批物品', p.title) + kv('供应商 / 渠道', p.supplier + ' / ' + p.channel) + kv('数量 / 成交单价', p.qty + ' / ' + money(p.price)) + '</div><div class="form-grid" style="margin-top:20px">' + field('buyer', '采购负责人', 'text', p.buyer, 'required maxlength="30"') + field('warehouse', '到货仓库', 'text', p.warehouse, 'required maxlength="60"') + '</div><div style="margin-top:20px">' + notice('演示按已批准的数量和价格登记。成交条件发生变化时，正式流程应重新审批；登记完成仍不增加库存。') + '</div>', b('确认采购登记', 'biz-save-registration', p.id, true));
    },
    'biz-save-registration': el => {
      const p = state.purchases.find(x => x.id === el.dataset.id);
      if (!p || p.status !== 'registration') return A.toast('该采购单已完成登记或尚未审批');
      const buyer = read('buyer'), warehouse = read('warehouse');
      if (!buyer || !warehouse) return A.toast('请填写采购负责人和到货仓库');
      p.buyer = buyer; p.warehouse = warehouse; p.status = 'approved';
      p.trace.push('10-08 · ' + buyer + ' 完成采购登记：' + p.qty + ' 件 × ' + money(p.price) + '，等待到货验收');
      A.closeModal(); A.render(); A.toast('采购信息已登记，等待实物验收入库');
    },
    'biz-open-ledger': el => {
      if (A.state) { A.state.query = el.dataset.id; A.state.filter = 'all'; }
      A.navigate('assets');
    },
    'biz-receive': el => {
      const p = state.purchases.find(p => p.id === el.dataset.id);
      if (!p || p.status !== 'approved') return A.toast('只有审批通过的采购单可以入库');
      A.modal('验收入库 · ' + p.id, '<div class="grid-3">' + kv('物品', p.title) + kv('本次验收数量', String(p.qty)) + kv('入库总成本', money(p.qty * p.price)) + '</div><div class="form-grid" style="margin-top:24px">' + field('warehouse', '接收仓库', 'text', p.warehouse, 'required maxlength="60"') + field('receiver', '验收人', 'text', '赵敏', 'required maxlength="30"') + '</div>' + (p.kind === 'unique' ? '<div class="field" style="margin-top:16px"><label for="biz-serials">设备序列号 · 每台一行（共 ' + p.qty + ' 台）</label><textarea id="biz-serials" name="serials" rows="4" style="width:100%;padding:12px;border:1px solid var(--line);border-radius:8px;background:var(--surface);color:var(--ink)">' + Array.from({ length: p.qty }, (_, i) => p.id + '-SN' + String(i + 1).padStart(3, '0')).join('\n') + '</textarea></div>' : notice('批次物资将在确认后生成唯一入库批次，并增加可用数量。')) + '<div style="margin-top:18px">' + notice('当前雏形演示整单验收。确认后生成入库流水；重复确认不会重复增加库存。') + '</div>', b('确认实物入库', 'biz-confirm-receive', p.id, true));
    },
    'biz-confirm-receive': el => {
      const p = state.purchases.find(p => p.id === el.dataset.id);
      if (!p || p.status !== 'approved') return A.toast('该采购单已处理，请勿重复入库');
      const warehouse = read('warehouse'), receiver = read('receiver');
      if (!warehouse || !receiver) return A.toast('请填写接收仓库和验收人');
      const serials = p.kind === 'unique' ? read('serials').split(/[\n,，]+/).map(s => s.trim()).filter(Boolean) : [];
      const existing = state.purchases.filter(x => x.id !== p.id).flatMap(x => x.serials).concat(A.assets.map(a => a.serial));
      if (p.kind === 'unique' && (serials.length !== p.qty || new Set(serials.map(x => x.toLowerCase())).size !== p.qty || serials.some(s => existing.some(x => x.toLowerCase() === s.toLowerCase() || x.toLowerCase() === E(s).toLowerCase())))) return A.toast('序列号数量需与设备数量一致，且不得重复或已入库');
      p.status = 'received'; p.warehouse = warehouse; p.serials = serials;
      p.receipt = p.id.replace('PO-', 'IN-'); p.assetIds = [];
      if (p.kind === 'unique') {
        const type = /交换机|路由器|防火墙/.test(p.title) ? '网络设备' : /服务器|PowerEdge/i.test(p.title) ? '物理服务器' : /ThinkPad|笔记本|电脑|EliteBook|MacBook/i.test(p.title) ? '办公电脑' : '通用设备';
        for (let i = 0; i < serials.length; i++) {
          const serial = serials[i];
          if (A.assets.some(a => a.serial.toLowerCase() === E(serial).toLowerCase())) continue;
          let number = A.assets.length + 1;
          while (A.assets.some(a => a.id === 'IT-2026-IN-' + String(number).padStart(4, '0'))) number += 1;
          const id = 'IT-2026-IN-' + String(number).padStart(4, '0');
          const flow = p.id.replace('PO-', 'FL-') + '-' + String(i + 1).padStart(3, '0');
          p.assetIds.push(id);
          A.assets.unshift({
            source: 'local', id, name: p.title, type, icon: type === '网络设备' ? 'router' : type === '物理服务器' ? 'server' : 'laptop',
            os: '待采集', status: '在库', online: '未纳管', owner: '行政资产库', dept: '行政部',
            ip: '—', mac: '待采集', priceCents: p.price ? Math.round(Number(p.price) * 100) : 0, purchased: p.date, channel: p.channel, supplier: p.supplier, serial,
            location: warehouse, warranty: '待登记保修凭证', lastSeen: '尚未接入', lastUsed: '尚未领用', idle: '—', offline: '—',
            origin: p.id, purchase: p.id, receipt: p.receipt, received: now, flow, used: '—', cost: '公共资产池',
            age: Math.floor((Date.parse(now) - Date.parse(p.date)) / 86400000) + ' 天',
            history: [
              [p.date, '采购审批与采购登记', p.id + ' / L01 · ' + p.channel + ' · 单价 ' + money(p.price)],
              [now, '验收入库，建立唯一资产卡片', p.receipt + ' / L01 → 流水 ' + flow + ' · 序列号 ' + serial + ' · 验收人 ' + receiver]
            ]
          });
        }
      }
      if(p.kind==='batch'){p.batchId=p.id.replace('PO-','BAT-');state.batches.push({id:p.batchId,title:p.title,qty:p.qty,warehouse,price:p.price,purchase:p.id,receipt:p.receipt});}
      p.trace.push('10-08 · ' + receiver + ' 验收 ' + p.qty + ' 件，存放于 ' + warehouse, '10-08 · ' + p.receipt + ' / L01 已确认，生成入库流水' + (p.kind === 'unique' ? '，资产台账新增 ' + p.assetIds.length + ' 张卡片' : '，批次 '+p.batchId+' 增加 '+p.qty+' 件（独立于领用样例库存）'));
      A.closeModal(); A.render(); A.toast(p.kind === 'unique' ? '入库确认成功，资产台账已新增 ' + p.assetIds.length + ' 台设备' : '入库确认成功，批次数量已更新');
    },
    'biz-request-detail': el => {
      const r = state.requests.find(x => x.id === el.dataset.id);
      A.modal('领用记录 · ' + r.id, '<div class="row between" style="margin-bottom:20px"><strong>' + E(r.title) + ' × ' + r.qty + '</strong>' + requestStatus(r) + '</div><div class="grid-3" style="margin-bottom:24px">' + kv('申请人', r.person) + kv('部门', r.department) + kv('用途', r.purpose) + '</div>' + timeline(r.trace), b('关闭', 'biz-close'));
    },
    'biz-new-request': () => A.modal('发起领用申请', '<div class="form-grid">' + field('person', '申请人', 'text', '', 'required maxlength="30"') + field('department', '部门', 'text', '', 'required maxlength="60"') + select('item', '领用物品', [['laptop', 'ThinkPad T14 · 可用 ' + state.stock.laptop], ['monitor', 'Dell 27 英寸显示器 · 可用 ' + state.stock.monitor], ['ssd', '1TB 固态硬盘 · 可用 ' + state.stock.ssd]]) + field('qty', '领用数量', 'number', '1', 'min="1" max="100" step="1"') + field('purpose', '领用用途', 'text', '', 'required maxlength="100"') + '</div><div style="margin-top:20px">' + notice('领用申请依次经过上级审批、行政批复和实物交接。库存会在最终确认出库时再次校验。') + '</div>', b('提交领用申请', 'biz-save-request', '', true)),
    'biz-save-request': () => {
      const person = read('person'), department = read('department'), purpose = read('purpose'), qty = Number(read('qty')), item = read('item');
      if (!person || !department || !purpose || !Number.isInteger(qty) || qty < 1 || qty > 100) return A.toast('请填写完整信息与有效的整数领用数量');
      const titles = { laptop: 'ThinkPad T14', monitor: 'Dell 27 英寸显示器', ssd: '1TB 固态硬盘' };
      state.requests.unshift({ id: 'REQ-202610-' + (state.requests.length + 20), person, department, purpose, qty, item, title: titles[item], stage: 0, trace: ['10-08 · ' + person + ' 提交领用申请（本地模拟）'] });
      A.closeModal(); A.render(); A.toast('领用申请已进入上级审批环节');
    },
    'biz-request-action': el => {
      const r = state.requests.find(x => x.id === el.dataset.id);
      if (!r || r.stage < 0 || r.stage >= 3) return A.toast('该申请已处理');
      A.modal((r.stage === 0 ? '上级审批' : r.stage === 1 ? '行政审批' : '确认实物交接') + ' · ' + r.id, '<div class="grid-3">' + kv('实领人', r.person) + kv('物品 / 数量', r.title + ' × ' + r.qty) + kv('当前可用库存', String(state.stock[r.item])) + '</div><p class="small muted">用途：' + E(r.purpose) + '</p>' + notice(r.stage === 2 ? '确认表示已完成实物交接；系统扣减本模块样例库存，并记录实领责任与流水；全局台账逐件分配尚未接入。' : '当前模拟角色：' + (r.stage === 0 ? '部门上级审批人' : '行政管理员') + '。审批通过只推进流程，不扣减库存。'), (r.stage < 2 ? b('驳回申请', 'biz-reject-request', r.id) : '') + b(r.stage === 2 ? '确认交接并出库' : '审批通过', 'biz-advance-request', r.id, true, 'data-stage="'+r.stage+'"'));
    },
    'biz-advance-request': el => {
      const r = state.requests.find(x => x.id === el.dataset.id);
      if (!r || r.stage < 0 || r.stage >= 3) return A.toast('该申请已处理');
      if (Number(el.dataset.stage)!==r.stage) return A.toast('审批步骤已变化，请重新打开当前环节');
      if (r.stage === 2 && state.stock[r.item] < r.qty) return A.toast('可用库存不足，不能确认出库');
      if (r.stage === 0) r.trace.push('10-08 · 部门上级审批通过，库存未变动');
      if (r.stage === 1) r.trace.push('10-08 · 行政管理员批复通过，等待实物交接');
      if (r.stage === 2) { state.stock[r.item] -= r.qty; r.trace.push('10-08 · 赵敏确认实物交接给 ' + r.person, '10-08 · ' + r.id.replace('REQ-', 'OUT-') + ' / L01 已确认，可用库存减少 ' + r.qty + '，本申请实领责任记录为 ' + r.person); }
      r.stage += 1; A.closeModal(); A.render(); A.toast(r.stage === 3 ? '已确认出库，样例库存和本申请交接记录已更新' : '审批已通过，流程进入' + stageLabel[r.stage]);
    },
    'biz-reject-request': el => {
      const r = state.requests.find(x => x.id === el.dataset.id);
      if (!r || r.stage < 0 || r.stage > 1) return A.toast('当前环节不可驳回');
      A.modal('驳回领用申请', field('reason', '驳回原因', 'text', '', 'required maxlength="200" placeholder="请说明原因"'), b('确认驳回', 'biz-confirm-reject', r.id, true));
    },
    'biz-confirm-reject': el => {
      const r = state.requests.find(x => x.id === el.dataset.id), reason = read('reason');
      if (!reason) return A.toast('请填写驳回原因');
      if (!r || r.stage < 0 || r.stage > 1) return A.toast('该申请已处理');
      r.trace.push('10-08 · ' + stageLabel[r.stage].replace('待', '') + '驳回：' + reason + '；未出库'); r.stage = -1;
      A.closeModal(); A.render(); A.toast('申请已驳回，库存未发生变化');
    },
    'biz-return': el => {
      const r = state.requests.find(x => x.id === el.dataset.id);
      A.modal('归还验收 · ' + r.id, '<p>' + E(r.person) + ' 归还 ' + E(r.title) + ' × ' + r.qty + '</p>' + select('condition', '验收结果', [['usable', '设备完好 / 可重新领用'], ['repair', '需要维修 / 暂不可领用']]) + notice('归还生成独立流水并保留原领用记录。完好的设备才会重新进入可用库存。'), b('确认归还验收', 'biz-confirm-return', r.id, true));
    },
    'biz-confirm-return': el => {
      const r = state.requests.find(x => x.id === el.dataset.id);
      if (!r || r.stage !== 3) return A.toast('只有已领用的物品可以归还');
      if(!['usable','repair'].includes(read('condition')))return A.toast('请选择有效验收结果'); const usable = read('condition') === 'usable'; if (usable) state.stock[r.item] += r.qty;
      r.stage = 4; r.trace.push('10-08 · RETURN-' + r.id.slice(4) + ' 归还验收 ' + r.qty + ' 件，' + (usable ? '完好入库，可重新领用' : '转维修区，未计入可用库存'));
      A.closeModal(); A.render(); A.toast(usable ? '归还已确认，可用库存已增加' : '归还已确认，设备进入待维修状态');
    },
    'biz-inventory-tab': el => { state.inventoryTab = el.dataset.id; A.render(); },
    'biz-select-plan': el => { const i = state.plans.findIndex(p => p.id === el.dataset.id); if (i >= 0) state.plans.push(state.plans.splice(i, 1)[0]); A.render(); },
    'biz-new-inventory': () => A.modal('新建盘点计划', '<div class="form-grid">' + field('planTitle', '计划名称', 'text', '总部 IT 资产专项盘点', 'required maxlength="80"') + select('planKind', '盘点类别', [['quarter', '季度盘点'], ['year', '年度盘点']]) + field('planOwner', '盘点负责人', 'text', '赵敏', 'required maxlength="30"') + field('planDate', '完成日期', 'date', '2026-10-31', 'min="2026-10-08"') + '</div><div style="margin-top:20px">' + notice('演示范围：总部 6 条样例台账。新计划启动时生成独立的盘点基准，历史盘点保留。') + '</div>', b('创建计划', 'biz-save-inventory', '', true)),
    'biz-save-inventory': () => {
      const title = read('planTitle'), owner = read('planOwner'), date = read('planDate'), kind = read('planKind');
      if (!title || !owner || !date || date < now) return A.toast('请填写计划名称、负责人和不早于今天的日期');
      state.plans.push({ id: 'CHK-202610-' + String(state.plans.length + 1).padStart(3, '0'), title, owner, date, kind, planned: true, rows: [] });
      state.inventoryTab = kind; A.closeModal(); A.render(); A.toast('新盘点计划已创建，可启动核验');
    },
    'biz-start-inventory': el => {
      const p = state.plans.find(x => x.id === el.dataset.id); if (!p || !p.planned) return;
      p.rows = inventoryBaseline.map(r => ({ ...r, actual: null, status: 'pending' }));
      p.planned = false; A.render(); A.toast('已冻结 6 条样例基准，可以开始实物核验');
    },
    'biz-check-item': el => {
      const r = currentPlan().rows.find(x => x.id === el.dataset.id);
      A.modal('实物核验 · ' + r.id, '<p><strong>' + E(r.title) + '</strong> · 账面数量 ' + r.qty + '</p><div class="form-grid">' + field('actual', '实盘数量', 'number', String(r.qty), 'min="0" max="10000" step="1"') + field('actualPerson', '实际保管人', 'text', r.person, 'required maxlength="30"') + field('actualLocation', '实际位置', 'text', r.location, 'required maxlength="80"') + '</div>', b('保存核验结果', 'biz-save-check', r.id, true));
    },
    'biz-save-check': el => {
      const r = currentPlan().rows.find(x => x.id === el.dataset.id), raw = read('actual'), actual = Number(raw), person = read('actualPerson'), location = read('actualLocation');
      if (raw === '' || !Number.isInteger(actual) || actual < 0 || actual > 10000 || !person || !location) return A.toast('请填写有效实盘数量、保管人和位置');
      if (!r || r.status !== 'pending') return A.toast('该条目已核验');
      r.actual = actual; r.actualPerson = person; r.actualLocation = location; r.status = actual === r.qty && person === r.person && location === r.location ? 'matched' : 'difference';
      A.closeModal(); A.render(); A.toast(r.status === 'matched' ? '核验完成，账实一致' : '已记录盘点差异，账面台账未修改');
    },
    'biz-check-detail': el => {
      const r = currentPlan().rows.find(x => x.id === el.dataset.id);
      A.modal('盘点记录 · ' + r.id, '<div class="row between" style="margin-bottom:20px"><strong>' + E(r.title) + '</strong>' + inventoryBadge(r) + '</div>' + A.table(['核对项', '账面记录', '实盘记录'], [['数量', String(r.qty), String(r.actual)], ['保管人', E(r.person), E(r.actualPerson || r.person)], ['位置', E(r.location), E(r.actualLocation || r.location)]]) + '<div style="margin-top:20px">' + notice(r.status === 'matched' ? '已核验账实一致。盘点记录独立保存，关联当前资产或批次。' : r.status === 'review' ? '差异工单已创建：DIFF-' + r.id + '。等待行政核查原单并审批更正，台账尚未改变。' : '核验存在差异。可创建本地差异工单，交由行政核查领用、归还与入库原始单据。') + '</div>', r.status === 'difference' ? b('创建差异工单', 'biz-create-difference', r.id, true) : b('关闭', 'biz-close'));
    },
    'biz-create-difference': el => {
      const r = currentPlan().rows.find(x => x.id === el.dataset.id); if (!r || r.status !== 'difference') return;
      r.status = 'review'; A.closeModal(); A.render(); A.toast('本地差异工单已创建，等待行政核查；台账未修改');
    },
    'biz-reconcile': () => { state.financeChecked = true; A.render(); A.toast('校验完成：2 笔一致，1 笔存在 400 元差异'); },
    'biz-finance-detail': el => {
      const r = state.finance.find(x => x.id === el.dataset.id);
      A.modal('财务追溯 · ' + r.id, '<div class="row between" style="margin-bottom:20px"><strong>' + E(r.item) + '</strong>' + A.badge(r.ledger === r.book ? '一致' : '差异 ' + money(r.ledger - r.book), r.ledger === r.book ? 'green' : 'amber') + '</div><div class="grid-2">' + kv('资产编码 / 批次', r.asset) + kv('原始单据 + 明细行', r.source) + kv('确认单据 + 明细行', r.document) + kv('业务流水号', r.flow) + kv('财务期间 / 币种', '2026-09 / CNY') + kv('财务凭证', r.voucher) + kv('台账金额', money(r.ledger)) + kv('财务金额', money(r.book)) + '</div><h4>核对说明</h4><p class="small" style="line-height:1.8">' + E(r.reason) + '</p>' + notice('当前财务角色只读。差异需由采购 / 行政关联原单更正，并经审批后生成调整流水；不会直接修改历史记录。'), b('导出此笔追溯记录', 'biz-export-line', r.id, true));
    },
    'biz-export-line': el => {
      const r = state.finance.find(x => x.id === el.dataset.id);
      exportFinance([r], '财务追溯-' + r.id + '.csv'); A.toast('追溯明细已导出');
    },
    'biz-export-finance': () => { exportFinance(state.finance, '2026-09-财务对账样例.csv'); A.toast('对账明细已导出 CSV'); }
  });
  function exportFinance(rows, filename) {
    const data = [['财务期间', '币种', '资产或批次', '物品', '业务类型', '原始单据和明细行', '确认单据和明细行', '业务流水', '凭证', '台账金额', '财务金额', '差额', '核对说明']].concat(rows.map(r => ['2026-09', 'CNY', r.asset, r.item, r.type, r.source, r.document, r.flow, r.voucher, r.ledger.toFixed(2), r.book.toFixed(2), (r.ledger - r.book).toFixed(2), r.reason]));
    const csv = '\ufeff' + data.map(row => row.map(v => '"' + String(v).replace(/"/g, '""') + '"').join(',')).join('\r\n');
    A.download(filename, csv, 'text/csv;charset=utf-8');
  }
})();
