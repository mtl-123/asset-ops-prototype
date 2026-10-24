(function () {
  'use strict';
  const A = window.AssetDemo;
  const { escape: E, badge, button: B, panel: P, table: T, metric: M } = A;
  const snapshot = '2026-10-15 10:30';
  const state = {
    filter: 'all', app: 'APP-001', tab: 'overview', eventSequence: 0,
    apps: [
      { id: 'APP-001', name: '研发工单平台', category: '内部 Web', env: '生产', team: '平台工程组', owner: '陈宇', tier: '核心', url: 'https://tickets.example.invalid', health: '性能下降', method: 'HTTP + OTel', site: '总部管理区', p95: '5,800 ms', errors: '3.2%', volume: '42 req/s', uptime: '99.82%', cert: '82 天', host: 'srv-prod-01', dependencies: ['Nginx 入口', '工单 API · 2 个实例', 'PostgreSQL · 连接池告警', 'Redis · 正常'], last: snapshot, connected: true },
      { id: 'APP-002', name: '企业官网', category: '公开网站', env: '生产', team: '数字运营组', owner: '王铭', tier: '重要', url: 'https://www.example.invalid', health: '正常', method: '多点 HTTP / TLS 拨测', site: '外部探测点', p95: '320 ms', errors: '0.05%', volume: '18 req/s', uptime: '99.98%', cert: '14 天', host: 'web-prod-01', dependencies: ['DNS / TLS', 'CDN / WAF', 'Nginx · 官网', '静态内容存储'], last: snapshot, connected: true },
      { id: 'APP-003', name: '库存查询 API', category: 'API 服务', env: '生产', team: '业务研发组', owner: '许嘉宁', tier: '核心', url: 'https://inventory.example.invalid/health', health: '正常', method: 'HTTP + OTel', site: '总部管理区', p95: '185 ms', errors: '0.1%', volume: '64 req/s', uptime: '99.95%', cert: '61 天', host: 'srv-test-02', dependencies: ['内部负载均衡', '库存 API', 'MySQL · 主从', '消息队列'], last: snapshot, connected: true },
      { id: 'APP-004', name: '分支文件服务', category: '内部服务', env: '生产', team: '分支运维组', owner: '周启明', tier: '一般', url: 'tcp://files.example.invalid:445', health: '观测中断', method: 'TCP + 主机 Agent', site: '分支办公区', p95: '未知', errors: '未知', volume: '未采集', uptime: '覆盖不足', cert: '不适用', host: 'branch-file-01', dependencies: ['分支防火墙', 'Windows 文件服务', 'NAS 存储'], last: '2026-10-15 10:12', connected: true }
    ],
    alerts: [
      { id: 'ALT-101', app: 'APP-001', title: '工单 API 延迟超过阈值', level: '严重', start: '10:23', status: '待确认', rule: 'p95 > 2,000 ms 持续 5 分钟', evidence: '演示窗口 10:25–10:30，p95 为 5,800 ms；部分 trace 出现数据库等待。相关性需要进一步核实。', caseId: '' },
      { id: 'ALT-102', app: 'APP-002', title: '官网 TLS 证书即将到期', level: '提醒', start: '09:00', status: '待确认', rule: '证书剩余时间 < 15 天', evidence: '外部拨测观察到证书剩余 14 天。到期时间、证书链与自动续期结果需从真实目标回读。', caseId: '' },
      { id: 'ALT-103', app: 'APP-004', title: '分支采集网关失联', level: '重要', start: '10:15', status: '待确认', rule: '超过 3 个心跳周期未上报', evidence: '最后网关心跳 10:12。分支服务当前健康未知；相关下游指标转为过期，避免将其全部报为故障。', caseId: '' }
    ],
    connectors: [
      { id: 'WIN', name: 'Windows / Linux 主机', protocol: 'Agent · HTTPS / mTLS', scope: '总部办公与机房', authority: '资产清单、心跳、服务、软件', mode: '只读采集', status: '演示就绪', seen: '10:30:00' },
      { id: 'PVE', name: 'PVE 集群', protocol: 'PVE API · HTTPS', scope: '生产集群 / 资源池', authority: '节点、VM、存储、任务', mode: '只读 + 授权任务', status: '演示就绪', seen: '10:29:45' },
      { id: 'NET', name: '交换机与路由器', protocol: 'SNMPv3 / IPFIX', scope: '总部网络管理区', authority: '接口指标、邻居、采样流量', mode: '只读采集', status: '演示就绪', seen: '10:29:30' },
      { id: 'WEB', name: '应用与 Web 拨测', protocol: 'HTTP / TLS / OTel', scope: '内部与外部探测点', authority: '可用性、性能、日志与追踪', mode: '只读探测', status: '演示就绪', seen: '10:30:00' },
      { id: 'BRANCH', name: '分支站点网关', protocol: '出站 mTLS · 443', scope: '分支办公区', authority: '本地采集、缓冲与任务回执', mode: '只读采集', status: '采集失联', seen: '10:12:00' }
    ],
    jobs: [{ id: 'JOB-001', title: '重启工单 API 服务', target: 'APP-001 / 工单 API 实例 A', stage: 0, trace: ['10:30 · 运维负责人提交演示任务，尚未执行'], request: 'REQ-20261015-001', external: '尚未提交', credential: '受管凭据引用 · 不向前端返回' }]
  };
  A.services = { getApplications: () => state.apps.map(a => ({...a, dependencies:[...a.dependencies]})) };
  const findApp = id => state.apps.find(a => a.id === id);
  const attrs = id => `data-id="${E(id)}"`;
  const act = (text, action, id, primary = false) => B(text, action, primary, attrs(id));
  const tone = text => /严重|失败/.test(text) ? 'red' : /中断|失联|下降|待|提醒|重要/.test(text) ? 'amber' : /正常|完成|就绪|已确认/.test(text) ? 'green' : 'neutral';
  const details = rows => `<div class="details-grid">${rows.map(([k,v]) => `<div><div class="detail-label">${E(k)}</div><div class="detail-value">${E(v)}</div></div>`).join('')}</div>`;
  const notice = text => `<div class="notice">${text}</div>`;
  const tiles = items => `<div class="platform-map">${items.map(i => `<div class="platform-node"><small>${E(i[0])}</small><strong>${E(i[1])}</strong><span>${E(i[2])}</span></div>`).join('')}</div>`;
  const field = (name, label, control) => `<div class="field"><label for="pf-${name}">${label}</label>${control}</div>`;
  const read = name => A.root.querySelector('#pf-' + name)?.value.trim() || '';
  function error(message) { const el = A.root.querySelector('#platform-error'); if (el) el.textContent = message; }
  function history(items) { return `<div class="timeline">${items.map(t => `<div class="timeline-item"><strong>${E(t)}</strong></div>`).join('')}</div>`; }
  function persist() {
    if (window.openai?.setWidgetState) window.openai.setWidgetState({ modelContent: { page: A.state.page, selectedApplication: state.app, prototype: '栖云资产与应用运维 v0.4' }, privateContent: { appTab: state.tab } }).catch(() => {});
  }
  A.pages.applications = () => {
    const selected = state.apps.filter(a => state.filter === 'all' || (state.filter === 'issue' && a.health !== '正常') || a.env === state.filter);
    return `<div class="platform-hero"><div><span class="eyebrow">SERVICE CATALOG</span><h2>从一台设备，走到一个业务服务</h2><p>应用、入口、负责人和运行依赖，在同一份服务档案里关联。</p></div>${B('＋ 登记应用 / 网站', 'pf-register-app', true)}</div>
      <div class="grid-4">${M('已登记服务',String(state.apps.length),'应用、网站与内部服务')}${M('性能异常',String(state.apps.filter(a=>a.health==='性能下降').length),'外部探测 + 内部追踪关联','amber')}${M('观测未知',String(state.apps.filter(a=>/中断|待接入/.test(a.health)).length),'采集失联与未接入分别标记','amber')}${M('证书到期提醒',String(state.apps.filter(a=>a.cert==='14 天').length),'剩余 < 15 天 · 演示策略','amber')}</div>
      <div class="toolbar"><div class="tabs platform-tabs">${[['all','全部服务'],['issue','需关注'],['生产','生产环境']].map(t=>`<button data-action="pf-app-filter" data-id="${t[0]}" class="${state.filter===t[0]?'active':''}">${t[1]}</button>`).join('')}</div><span class="small muted">演示观测 ${snapshot} · 指标窗口近 5 分钟</span></div>
      ${P('我的应用与网站',T(['应用 / 类型','运行状态','负责人 / 环境','p95 响应 / 错误率','采集来源 / 最后观测',''],selected.map(a=>[
        `<button class="op-btn op-btn-link" data-action="pf-app-detail" ${attrs(a.id)}>${E(a.name)}</button><small>${E(a.id)} · ${E(a.category)}</small>`,
        badge(E(a.health),tone(a.health)),`${E(a.owner)}<small>${E(a.team)} · ${E(a.env)}</small>`,`${E(a.p95)}<small>${E(a.errors)} 错误率</small>`,`${E(a.method)}<small>${E(a.last)}</small>`,act('服务详情 →','pf-app-detail',a.id)
      ])),badge('演示服务清单','neutral'))}
      <div class="grid-3">${P('网页可用性','<div class="small muted">DNS、TLS、HTTP 状态码、内容断言、多点拨测与关键业务流程。网页能打开，还要确认用户操作能完成。</div>')}${P('应用内部观测','<div class="small muted">请求量、错误率、延迟、日志和 trace；关联进程、数据库、缓存与发布版本。</div>')}${P('完整服务责任','<div class="small muted">业务负责人、技术负责人、值班组、环境、等级、成本与应急手册统一登记。</div>')}</div>`;
  };
  function appModal(id, tab = 'overview') {
    const a = findApp(id); if (!a) return;
    state.app = id; state.tab = tab;
    const tabs = [['overview','服务档案'],['dependencies','依赖与影响'],['probes','Web 拨测'],['telemetry','指标日志追踪']];
    let body = '';
    if (tab === 'overview') body = `${details([['运行状态',a.health],['技术负责人',a.owner+' / '+a.team],['环境 / 等级',a.env+' / '+a.tier],['登记入口',a.url],['承载资源',a.host],['探测位置',a.site],['采集方式',a.method],['最后成功观测',a.last],['近 30 天可用率',a.uptime]])}${notice('可用率按有效观测窗口计算，并显示覆盖率。探针失联进入未知，不当作服务故障；应用安装清单与业务服务清单分别管理。')}<div class="row">${act('关联告警','pf-app-alerts',id)}${B('查看主机资源','go',false,'data-page="hosts"')}</div>`;
    if (tab === 'dependencies') body = `${tiles(a.dependencies.map((d,i)=>[i===0?'入口':i===a.dependencies.length-1?'依赖':'运行组件',d,'演示关系 · 待真实发现核实']))}${notice('关联链：业务服务 → 应用版本 / 实例 → VM / 容器 → 宿主机 → IP / 交换机端口。关系保存来源和有效期；自动发现先待认领，不能覆盖已确认责任归属。')}<div class="row">${B('主机与远程','go',false,'data-page="hosts"')}${B('PVE 资源','go',false,'data-page="pve"')}${B('网络与 IP','go',false,'data-page="ipam"')}</div>`;
    if (tab === 'probes') {const isHTTP=/^https?:/.test(a.url);body = `${details([['目标入口',a.url],['探测网区',a.site],['采样周期','建议 60 秒 · 超时 10 秒'],['断言',isHTTP?'TLS 有效 + HTTP 2xx + 内容检查':'TCP 端口可达；服务协议需额外验证'],['证书剩余',a.cert],['最近观测',a.last]])}${notice(a.connected ? '演示结果不执行网络请求。HTTP 探测不能证明登录、查询或提交成功，关键流程需单独配置浏览器合成测试。' : '此服务尚未接入探测点，登记入口不代表可以访问或已经纳管。')}${T(['探测项目','能力范围','当前示例'],[['DNS / TLS','解析、证书链、有效期',!isHTTP?'TLS 不适用':a.connected?'演示观测':'未接入'],['HTTP / 内容断言','状态码、重定向、关键内容',!isHTTP?'TCP 服务不适用':a.connected?'演示观测':'待配置'],['关键业务流程','登录 → 查询 → 提交','待配置合成账号与流程'],['真实用户体验','JS 错误、页面性能、网络地区','待接入 RUM，先明确隐私范围']])}<div style="margin-top:16px">${act('演示一次探测结果','pf-probe',id,true)}</div>`;}
    if (tab === 'telemetry') body = `${details([['观测窗口','10:25–10:30 · 演示快照'],['请求量',a.volume],['p95 延迟',a.p95],['错误比例',a.errors],['资源关联',a.host],['数据来源',a.method]])}${notice(a.connected&&a.health!=='观测中断' ? '指标、日志、trace 以 service_id、环境、版本、实例和 trace_id 关联。敏感请求参数与口令不进入默认日志。' : '没有有效观测时保持未知，下方没有生成新的请求或追踪数据。')}${a.health==='性能下降'?T(['时间','关联证据','解释'],[['10:29:42','trace-demo-018 · 请求耗时 6.1 秒','数据库等待 span 较长，属于排查线索'],['10:29:42','日志包含同一 trace_id','可关联错误上下文与实例'],['10:27:00','release-demo-42','近期发布与故障时间相近，需验证因果']]):'<div class="small muted">详细日志、调用追踪和发布记录在真实接入后查询。</div>'}`;
    A.modal(E(a.name)+' · '+badge(E(a.health),tone(a.health)),`<div class="tabs">${tabs.map(t=>`<button class="${tab===t[0]?'active':''}" data-action="pf-app-tab" data-id="${t[0]}">${t[1]}</button>`).join('')}</div>${body}`,B('关闭','close-modal'));
    persist();
  }
  A.pages.alerts = () => `${P('告警处理链',tiles([['01','发现与聚合','同源去重 · 维护窗口'],['02','确认与分派','业务影响 · 值班负责人'],['03','处理与验证','工单 · 变更 · 恢复证据']]),badge('未连通知渠道','neutral'))}
    ${P('服务与采集告警',T(['告警 / 规则','关联服务','等级','状态','处理'],state.alerts.map(e=>{const a=findApp(e.app);return [`<strong>${E(e.title)}</strong><small>${E(e.id)} · ${E(e.rule)}</small>`,E(a.name),badge(e.level,tone(e.level)),badge(e.status,e.status==='已确认'?'blue':'amber'),act('查看与处理','pf-alert-detail',e.id)];})),badge(`${state.alerts.filter(e=>e.status==='待确认').length} 项待确认`,'amber'))}
    ${P('事件工单',T(['工单','来源告警','负责组','状态'],state.alerts.filter(e=>e.caseId).map(e=>[E(e.caseId),E(e.title),E(findApp(e.app).team),'待诊断 · 演示'])))}
    ${notice('确认告警表示有人接手，不表示故障恢复。网关失联时，下游状态过期并标记未知；告警分组仍保留每项原始证据。通知到企微、邮件等渠道需另外接入。任务演练独立于本快照，不改变观测或关闭告警。')}`;
  function alertModal(id) {
    const e=state.alerts.find(x=>x.id===id);if(!e)return;const a=findApp(e.app);
    A.modal(E(e.title),`${details([['告警编号',e.id],['关联服务',a.name],['状态',e.status],['技术负责人',a.owner],['开始时间','2026-10-15 '+e.start],['触发规则',e.rule]])}<div class="notice amber">${E(e.evidence)}</div>${e.caseId?`<p class="small">关联工单 ${E(e.caseId)} · 待诊断</p>`:''}<div class="row">${act('服务档案','pf-app-detail',a.id)}${act('自动化预案','pf-open-automation',a.id)}</div>`,(e.status==='待确认'?act('确认接手','pf-alert-ack',id):'')+(e.caseId?'':act('创建演示工单','pf-alert-case',id,true)));
  }
  A.pages.integrations = () => `${P('真实环境接入路径',tiles([['管理平台','权限与业务账本','资产 / 应用 / 工单 / 审计'],['异步执行','任务与凭据引用','队列 / 幂等 / 回读 / 补偿'],['站点网关','各网区就近部署','出站 mTLS / 本地缓冲'],['被管理资源','设备与业务服务','Agent / API / SNMP / OTel']]),badge('目标架构 · 尚未部署','neutral'))}
    <div class="toolbar"><span class="small muted">连接状态为演示样例，不代表已与实际环境建立连接。</span>${B('＋ 接入向导','pf-onboard',true)}</div>
    ${P('连接器与采集来源',T(['连接器 / 协议','范围 / 数据','权限模式','采集状态','最后成功',''],state.connectors.map(c=>[`${E(c.name)}<small>${E(c.protocol)}</small>`,`${E(c.scope)}<small>${E(c.authority)}</small>`,E(c.mode),badge(c.status,tone(c.status)),E(c.seen),act('诊断预览','pf-connector',c.id)])))}
    <div class="grid-2">${P('发现后的认领',`<div class="stack small"><span>发现候选 → 稳定身份匹配 → 差异复核 → 认领归属</span><span>不以 IP 作为永久主键；设备变更保留来源与历史。</span><span>API 采集不能改写采购价格、保管人等业务权威字段。</span></div>`)}${P('采集与控制分离',`<div class="stack small"><span>先开放只读能力，按资源单独启用远程和变更权限。</span><span>凭据只存在服务端密钥库，前端只引用凭据 ID。</span><span>网关失联显示数据过期，缓存回传带原始采集时间。</span></div>`)}</div>`;
  A.pages.automation = () => `${P('授权执行工作流',tiles([['预检','验证目标与依赖','参数 / 作用范围 / 备份'],['审批','绑定参数版本','审批不触发执行'],['执行','跟踪外部任务','幂等请求 / 持久化回执'],['验证','读回真实结果','失败补偿 / 人工恢复']]),badge('本地模拟 · 无远程执行','amber'))}
    ${P('任务中心',T(['任务 / 目标','请求标识','当前阶段','外部任务',''],state.jobs.map(j=>[`${E(j.title)}<small>${E(j.target)}</small>`,E(j.request),badge(stageName(j.stage),j.stage===6?'red':j.stage===5?'green':'amber'),E(j.external),act('查看与推进','pf-task',j.id)])))}
    <div class="grid-2">${P('模板化操作范围','<div class="stack small"><span>主机：服务重启、补丁、配置备份、受控文件分发。</span><span>PVE：Cloud-Init 创建、快照、备份、回收申请。</span><span>应用：健康核验、发布回滚申请、证书更新与回读。</span></div>')}${P('失败时怎么处理','<div class="stack small"><span>超时先查询外部结果，禁止盲目重放非幂等命令。</span><span>跨系统任务记录每个成功步骤，再决定补偿。</span><span>没有可靠回滚的操作，提前准备恢复与人工接管路径。</span></div>')}</div>`;
  const stageName = stage => ['待预检','待审批','待执行','待结果回读','待健康验证','演示验证完成','演示执行失败'][stage];
  function taskModal(id) {
    const j=state.jobs.find(x=>x.id===id);if(!j)return;
    const next=[['运行演示预检','pf-task-precheck'],['模拟独立审批通过','pf-task-approve'],['模拟提交执行','pf-task-execute'],['演示回读结果','pf-task-readback'],['模拟健康验证通过','pf-task-verify']][j.stage];
    A.modal(E(j.title),`${badge(stageName(j.stage),tone(stageName(j.stage)))}<div style="margin:20px 0">${details([['目标范围',j.target],['请求 ID',j.request],['外部任务 ID',j.external],['授权方式','演示分角色审批 · 限单实例'],['参数版本','v1 · 服务名与目标锁定'],['凭据策略',j.credential]])}</div>${history(j.trace)}${notice('演示步骤只改变本页状态。生产环境由服务端校验独立审批人、任务版本、授权时效和执行器身份；审批与回读无法由前端按钮代替。')}`,B('关闭','close-modal')+(next?act(next[0],next[1],j.id,true):'')+(j.stage===3?act('模拟执行失败','pf-task-fail',j.id):''));
  }
  Object.assign(A.actions,{
    'pf-app-filter':el=>{state.filter=el.dataset.id;A.render();persist();},
    'pf-app-detail':el=>appModal(el.dataset.id),
    'pf-app-tab':el=>appModal(state.app,el.dataset.id),
    'pf-app-alerts':el=>{const alerts=state.alerts.filter(e=>e.app===el.dataset.id);if(alerts.length)alertModal(alerts[0].id);else A.modal('关联告警','<div class="small muted">当前样例没有关联告警。未接入不代表没有故障。</div>');},
    'pf-probe':el=>{const a=findApp(el.dataset.id);if(!a)return;A.modal('探测结果预览 · '+E(a.name),`${details([['执行方式','演示结果 · 未发网络请求'],['探测位置',a.site],['目标',a.url],['状态',a.connected&&a.health!=='观测中断'?'引用既有演示快照':'未知 / 无有效探测结果'],['证据时间',a.last],['p95 / 证书',a.p95+' / '+a.cert]])}${notice('点击检查不产生新的真实观测。真实系统应保存每次探测的 DNS、TLS、连接、响应、断言与错误阶段。')}`,act('返回服务档案','pf-app-detail',a.id));},
    'pf-register-app':()=>A.modal('登记应用与网站',`<form id="platform-app-form"><div class="form-grid">${field('name','服务名称 *','<input id="pf-name" name="name" required maxlength="80" placeholder="例如 企业门户">')}${field('url','HTTP / HTTPS 入口 *','<input id="pf-url" name="url" type="url" required placeholder="https://portal.example.invalid">')}${field('owner','技术负责人 *','<input id="pf-owner" name="owner" required maxlength="40">')}${field('team','负责组 *','<input id="pf-team" name="team" required maxlength="60">')}${field('env','环境','<select id="pf-env"><option>生产</option><option>测试</option><option>开发</option></select>')}${field('site','探测网区','<select id="pf-site"><option>总部管理区</option><option>外部探测点</option><option>分支办公区</option></select>')}</div><div id="platform-error" role="alert" class="text-red small" style="margin-top:12px"></div></form>${notice('此处只登记服务目录；真实接入需选择授权探测范围和凭据引用，URL 中不要携带密码或令牌。')}`,B('取消','close-modal')+B('保存服务目录','pf-save-app',true)),
    'pf-save-app':()=>{const f=A.root.querySelector('#platform-app-form');if(!f.reportValidity())return;const name=read('name'),owner=read('owner'),team=read('team');if(!name||!owner||!team)return error('请完整填写服务名称、负责人和负责组。');let url;try{url=new URL(read('url'));}catch{return error('请填写有效的 URL。');}if(!['https:','http:'].includes(url.protocol)||url.username||url.password)return error('仅支持 HTTP / HTTPS，且入口不能包含账号或密码。');if(state.apps.some(a=>a.url===url.href||a.url===url.href.replace(/\/$/,'')))return error('此入口已登记，请查看已有服务。');const id='APP-'+String(state.apps.length+1).padStart(3,'0');state.apps.push({id,name,owner,team,url:url.href,category:'Web 应用',env:read('env'),site:read('site'),tier:'待评定',health:'待接入',method:'待配置',p95:'未知',errors:'未知',volume:'未采集',uptime:'未采集',cert:'未采集',host:'待关联',last:'尚未观测',connected:false,dependencies:['入口已登记','实例待关联','后端依赖待认领']});state.filter='all';A.closeModal();A.render();A.toast('服务已登记；监控状态保持待接入。');appModal(id);},
    'pf-alert-detail':el=>alertModal(el.dataset.id),
    'pf-alert-ack':el=>{const e=state.alerts.find(e=>e.id===el.dataset.id);if(!e)return;e.status='已确认';A.render();alertModal(e.id);},
    'pf-alert-case':el=>{const e=state.alerts.find(e=>e.id===el.dataset.id);if(!e||e.caseId)return;e.caseId='INC-20261015-'+String(++state.eventSequence).padStart(3,'0');A.render();alertModal(e.id);A.toast('已创建演示事件工单，等待负责人诊断。');},
    'pf-open-automation':()=>A.navigate('automation'),
    'pf-connector':el=>{const c=state.connectors.find(c=>c.id===el.dataset.id);if(!c)return;const fail=c.status==='采集失联';A.modal('连接器诊断预览 · '+E(c.name),`${details([['范围',c.scope],['协议',c.protocol],['权限模式',c.mode],['最近成功上报','2026-10-15 '+c.seen],['状态',c.status],['凭据','仅服务端引用，不返回浏览器']])}<div style="margin-top:20px">${T(['诊断阶段','演示结果'],[['DNS / 路由 / TLS',fail?'网关通道超时 · 后续阶段未执行':'样例通过'],['认证与作用域',fail?'未知':'样例通过 · 最小权限'],['读取与字段映射',fail?'保留旧快照，标记过期':'样例通过 · 身份字段匹配'],['写操作权限','此诊断不执行写操作']])}</div>${notice('实际连通性测试在所选站点网关执行，校验目标证书。浏览器不直接访问管理端口。此诊断为离线样例。')}`,B('关闭','close-modal'));},
    'pf-onboard':()=>A.modal('真实环境接入向导',`${history(['登记站点和授权网络范围，部署网关并校验证书','选择协议与只读凭据引用，测试路由和服务端身份','发现候选资源，匹配已有资产、应用和责任人','配置采集周期、过期阈值、告警与保留期限','试运行验证后，分别授权远程会话与变更任务'])}${notice('原型不接收真实口令、不安装 Agent、不扫描网络。正式接入先以少量授权设备进行只读验证。')}`,B('关闭','close-modal')),
    'pf-task':el=>taskModal(el.dataset.id),
    'pf-task-precheck':el=>advance(el,0,1,'演示预检通过：限定实例 A，服务名、依赖与恢复预案已核对'),
    'pf-task-approve':el=>advance(el,1,2,'演示独立审批通过：参数 v1，有效期 15 分钟'),
    'pf-task-execute':el=>advance(el,2,3,'演示执行器接受任务；等待外部结果',true),
    'pf-task-readback':el=>advance(el,3,4,'演示回读：服务进程重新运行；业务健康尚未验证'),
    'pf-task-verify':el=>advance(el,4,5,'演示健康验证通过：入口断言与服务指标恢复，保留前后观测'),
    'pf-task-fail':el=>advance(el,3,6,'演示执行失败：保留目标现状与回执，转人工恢复；不盲目重试')
  });
  function advance(el,from,to,message,submitted=false) {const j=state.jobs.find(j=>j.id===el.dataset.id);if(!j||j.stage!==from)return;j.stage=to;if(submitted)j.external='DEMO-EXT-'+j.request;j.trace.push(message);A.render();taskModal(j.id);}
})();

