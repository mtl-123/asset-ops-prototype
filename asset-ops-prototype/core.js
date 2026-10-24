var AssetDemo = window.AssetDemo = (() => {
  const root = document.getElementById('asset-app');
  const state = { page: 'overview', filter: 'all', query: '', detailTab: 'basic', detailId: '', trend: 'half', activeTheme: 'system' };
  const pages = {}, actions = {};
  const nav = [
    ['工作空间','overview','首页','layout-dashboard',''],
    ['', 'assets','资产台账','boxes','248'],
    ['业务协同','procurement','采购与入库','shopping-bag',''],
    ['', 'circulation','领用与流转','arrow-left-right','6'],
    ['', 'inventory','盘点管理','clipboard-check',''],
    ['', 'finance','财务对账','receipt',''],
    ['应用与服务','applications','应用与网站','app-window',''],
    ['', 'monitoring','监控中心','activity',''],
    ['', 'alerts','告警与事件','bell-ring','3'],
    ['基础设施','hosts','主机与远程','monitor',''],
    ['', 'pve','PVE 虚拟化','layers',''],
    ['', 'ipam','IP 地址管理','network',''],
    ['', 'network','网络与流量','activity',''],
    ['平台接入','integrations','接入中心','plug',''],
    ['', 'automation','自动化任务','workflow',''],
    ['治理与安全','governance','权限与审计','shield-check','']
  ];
  const meta = {
    overview:['首页','OVERVIEW','让每一件资产，都有清晰的去向。'],
    assets:['资产台账','ASSET REGISTER','从采购到退役，一张卡片串起资产的完整生命周期。'],
    procurement:['采购与入库','PROCUREMENT','把预算、采购渠道与实际到货，关联到每一件资产。'],
    circulation:['领用与流转','ASSET LIFECYCLE','每一次交接都有据可查，每一件资产都有责任人。'],
    inventory:['盘点管理','STOCKTAKE','季度检查，年度核实，让账面与实物保持一致。'],
    finance:['财务对账','RECONCILIATION','从汇总金额穿透到原始单据、明细行与资产流水。'],
    hosts:['主机与远程','HOST MANAGEMENT','Windows 与 Linux 统一可见，运维访问全程可追溯。'],
    pve:['PVE 虚拟化','VIRTUALIZATION','从模板到交付，统一管理计算资源与 Cloud-Init 配置。'],
    ipam:['IP 地址管理','IP ADDRESS MANAGEMENT','看清地址占用、MAC 绑定与分配依据。'],
    network:['网络与流量','NETWORK OBSERVABILITY','交换机、路由器、接口与流量，一处掌握。'],
    applications:['应用与网站','APPLICATIONS & SERVICES','从网页体验到应用依赖，把每一个业务服务纳入管理。'],
    monitoring:['监控中心','OBSERVABILITY','服务是否可用，资源是否健康，从这里找到答案。'],
    alerts:['告警与事件','INCIDENT OPERATIONS','关联服务、定位影响、分派负责人，保留处理与恢复证据。'],
    integrations:['接入中心','INTEGRATION CONTROL','按站点连接设备、应用与已有监控，明确权限与数据新鲜度。'],
    automation:['自动化任务','CONTROLLED AUTOMATION','先预检，再授权执行；每次任务都跟踪外部结果。'],
    governance:['权限与审计','GOVERNANCE','职责分离、最小授权，让每一次操作留下证据。']
  };
  // The ledger is the single source of truth for asset rows. Real assets come
  // from the Go API; rows created by flows that are not persisted yet are kept
  // separately and labelled, so a deleted asset can never linger here.
  let assets = [];
  const money = cents => (cents && cents > 0 ? '¥ ' + (cents / 100).toLocaleString() : '待补充');
  const shortDate = value => (value ? String(value).slice(0, 10) : '—');
  // ---- asset editor: one place for create-read-update-delete on real assets ----
  function assetEditor(apiId) {
    const api = window.QiyunAPI;
    if (!api?.getAsset) return toast('资产服务未就绪。');
    api.getAsset(apiId).then(raw => {
      const a = api.mapAsset(raw);
      const field = (id, label, value, extra = '') => `<div class="field"><label for="${id}">${label}</label><input id="${id}" value="${escape(value ?? '')}" ${extra}></div>`;
      modal(`编辑资产 · ${escape(a.name)}`, `<div class="notice amber">修改地址或 SSH 端口会让已批准的主机指纹失效，需要重新扫描并核验；只改名称、部门等信息不影响现有连接。</div>
        <div class="form-grid" style="margin-top:16px">
          ${field('ae-code', '资产编码（不可修改）', a.assetCode, 'disabled')}
          ${field('ae-name', '资产名称 *', a.name, 'maxlength="80"')}
          ${field('ae-address', 'IP 或主机名 *', a.ip, 'maxlength="255"')}
          ${field('ae-port', 'SSH 端口 *', a.sshPort, 'type="number" min="1" max="65535"')}
          ${field('ae-os', '操作系统', a.os, 'maxlength="80"')}
          ${field('ae-owner', '负责人', a.owner === '未指定' ? '' : a.owner, 'maxlength="60"')}
          ${field('ae-dept', '部门', a.department === '未分部门' ? '' : a.department, 'maxlength="60"')}
          <div class="field"><label for="ae-env">环境</label><select id="ae-env">${[['production', '生产环境'], ['staging', '预发布环境'], ['testing', '测试环境'], ['development', '开发环境']].map(([v, l]) => `<option value="${v}" ${(a.environment || 'production') === v ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
          ${field('ae-serial', 'SN 序列号', a.serial_number, 'maxlength="80"')}
          ${field('ae-mac', 'MAC 地址', a.mac_address, 'maxlength="32"')}
          ${field('ae-location', '存放位置', a.location, 'maxlength="120"')}
          ${field('ae-supplier', '供应商', a.supplier, 'maxlength="80"')}
          ${field('ae-po', '采购单号', a.purchase_order, 'maxlength="60"')}
          ${field('ae-price', '采购价（元）', a.price_cents ? (a.price_cents / 100).toFixed(2) : '', 'type="number" min="0" step="0.01"')}
          ${field('ae-purchased', '购买日期', a.purchased_at ? String(a.purchased_at).slice(0, 10) : '', 'type="date"')}
          ${field('ae-warranty', '保修到期', a.warranty_until ? String(a.warranty_until).slice(0, 10) : '', 'type="date"')}
        </div><div id="asset-editor-error" class="text-red small" role="alert" style="min-height:18px"></div>`,
        button('取消', 'close-modal') + button('删除资产', 'asset-delete', false, `data-id="${escape(apiId)}"`) + button('保存修改', 'asset-save', true, `data-id="${escape(apiId)}"`));
    }).catch(error => toast('读取资产失败：' + error.message));
  }
  async function saveAssetEdit(button) {
    const value = id => root.querySelector('#' + id)?.value?.trim() ?? '';
    const out = root.querySelector('#asset-editor-error');
    const name = value('ae-name'), address = value('ae-address'), port = Number(value('ae-port'));
    if (!name || !address) { if (out) out.textContent = '资产名称和地址不能为空'; return; }
    if (!port || port < 1 || port > 65535) { if (out) out.textContent = 'SSH 端口必须在 1-65535 之间'; return; }
    if (out) out.textContent = '正在保存...';
    const price = value('ae-price');
    try {
      await window.QiyunAPI.updateAsset(button.dataset.id, {
        name, address, ssh_port: port, os_name: value('ae-os'), owner: value('ae-owner'),
        department: value('ae-dept'), environment: value('ae-env'),
        serial_number: value('ae-serial'), mac_address: value('ae-mac'), location: value('ae-location'),
        supplier: value('ae-supplier'), purchase_order: value('ae-po'),
        price_cents: price ? Math.round(Number(price) * 100) : 0,
        purchased_at: value('ae-purchased'), warranty_until: value('ae-warranty')
      });
      await window.QiyunAPI.refreshAssets();
      closeModal(); toast('资产已更新。');
    } catch (error) { if (out) out.textContent = '保存失败：' + error.message; }
  }
  async function deleteAssetConfirm(apiId, name) {
    if (!window.confirm(`确认删除资产${name ? `“${name}”` : ''}？关联终端会话将关闭，历史审计记录保留。`)) return;
    try { await window.QiyunAPI.deleteAsset(apiId); await window.QiyunAPI.refreshAssets(); closeModal(); toast('资产已删除。'); }
    catch (error) { toast('删除失败：' + error.message); }
  }
  function ledgerRow(a) {
    const verified = a.hostKeyStatus === 'verified';
    const typeLabel = a.assetType === 'linux_host' || !a.assetType ? 'Linux 主机' : a.assetType;
    return {
      source: 'api', id: a.assetCode, apiId: a.id, name: a.name, serial: a.serial_number || '',
      icon: 'server', type: typeLabel, os: a.os_name || a.os || 'Linux',
      status: verified ? (a.status === 'offline' ? '离线' : '在用') : '待核验',
      online: verified ? (a.status === 'offline' ? '离线' : '在线') : '未纳管',
      owner: a.owner || '未指定', dept: a.department || '未分类', ip: a.ip || a.address || '—',
      mac: a.mac_address || '待采集', priceCents: a.price_cents || 0, price: null,
      purchased: shortDate(a.purchased_at), channel: a.supplier || '待补充',
      supplier: a.supplier || '待补充', warranty: shortDate(a.warranty_until), location: a.location || '待补充',
      environment: a.environment || '', port: a.sshPort, fingerprint: a.hostKeyFingerprint || '',
      serialNumber: a.serial_number || '', macAddress: a.mac_address || '',
      lastSeen: a.updated_at ? String(a.updated_at).replace('T', ' ').slice(0, 16) : '—',
      lastUsed: '—', idle: '—', offline: '—', age: '—',
      purchase: a.purchase_order || '待补充', used: '—', cost: '待补充'
    };
  }
  function syncAssets() {
    const api = window.QiyunAPI;
    const remote = (api?.getAssets?.() || []).map(ledgerRow);
    const local = assets.filter(a => a.source === 'local');
    // Mutate in place: other modules hold a reference to this array.
    assets.length = 0;
    assets.push(...local, ...remote);
    if (state.page === 'assets' || state.page === 'overview') { try { render(); } catch { /* before first paint */ } }
  }
  const iconSymbols = {'layout-dashboard':'▦',boxes:'▧','shopping-bag':'♧','arrow-left-right':'⇄','clipboard-check':'☑',receipt:'▤',monitor:'▱',layers:'▱',network:'⌘',activity:'⌁','shield-check':'◇',laptop:'▱',server:'▥',router:'▤',search:'⌕',plus:'＋',download:'↓',bell:'♧','arrow-up-right':'↗'};
  const iconMap={'layout-dashboard':'home',boxes:'box-open','shopping-bag':'shopping-cart','arrow-left-right':'exchange-alt','clipboard-check':'clipboard-check',receipt:'receipt',monitor:'laptop',layers:'layer-group',network:'network-wired',activity:'chart-line','shield-check':'shield-alt',laptop:'laptop',server:'server',router:'network-wired',search:'search',plus:'plus',download:'download',bell:'bell','arrow-up-right':'arrow-up','app-window':'laptop-code','bell-ring':'bell',plug:'plug',workflow:'tasks'};
  function icon(name){return `<span class="op-icon vab-icon vab-icon-${iconMap[name]||'cog'}" aria-hidden="true"></span>`;}
  function escape(value){return String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
  function safeAsset(a){return Object.fromEntries(Object.entries(a).map(([k,v])=>[k,typeof v==='string'?escape(v):v]));}
  function badge(text,tone='green'){return `<span class="badge badge-${tone}">${text}</span>`;}
  function button(label,action,primary=false,attrs=''){const marks={'＋':'plus','+':'plus','↓':'download'};const mark=marks[label[0]];if(mark)label=`<span class="vab-icon vab-icon-${mark}" aria-hidden="true"></span>${label.slice(1).trim()}`;return `<button type="button" class="op-btn${primary?' op-btn-primary':''}" data-action="${action}" ${attrs}>${label}</button>`;}
  function panel(title,body,extra=''){return `<section class="panel"><div class="panel-head"><h2>${title}</h2>${extra}</div><div class="panel-body">${body}</div></section>`;}
  function metric(label,value,foot,tone='green'){return `<div class="metric"><div class="metric-label">${label}<span class="metric-icon text-${tone}">↗</span></div><div class="metric-value">${value}</div><div class="metric-foot">${foot}</div></div>`;}
  function table(headers,rows){return `<div class="table-wrap"><table class="data-table"><thead><tr>${headers.map(c=>`<th scope="col">${c}</th>`).join('')}</tr></thead><tbody>${rows.map(row=>`<tr>${row.map(c=>`<td>${c}</td>`).join('')}</tr>`).join('')||`<tr><td colspan="${headers.length}" class="empty-state">暂无匹配记录</td></tr>`}</tbody></table></div>`;}
  function refreshIcons(){root.querySelectorAll('.field').forEach((field,i)=>{const label=field.querySelector('label'),control=field.querySelector('input,select,textarea');if(label&&control){if(!control.id)control.id='asset-field-'+i;label.htmlFor=control.id;}});if(globalThis.lucide)globalThis.lucide.createIcons({attrs:{width:16,height:16}});root.dispatchEvent(new CustomEvent("asset-rendered"));}
  let lastFocus = null;
  function modal(title,html,footer=''){
    const layer=root.querySelector('#modal-layer');
    if(layer.hidden)lastFocus=document.activeElement;
    layer.innerHTML=`<section class="op-modal" role="dialog" aria-modal="true" aria-labelledby="modal-title"><header class="modal-header"><h2 id="modal-title">${title}</h2><button class="modal-close" data-action="close-modal" aria-label="关闭">×</button></header><div class="modal-body">${html}</div>${footer?`<footer class="modal-footer">${footer}</footer>`:''}</section>`;
    layer.hidden=false; root.querySelector('.app-main').inert=true;root.querySelector('.sidebar').inert=true;
    refreshIcons();layer.querySelector('.modal-close').focus({preventScroll:true});
  }
  function closeModal(){root.querySelector('#modal-layer').hidden=true;root.querySelector('#modal-layer').innerHTML='';root.querySelector('.app-main').inert=false;root.querySelector('.sidebar').inert=false;if(lastFocus?.isConnected)lastFocus.focus({preventScroll:true});window.AdminShell?.apply();}
  let toastTimer;
  function toast(msg){const el=root.querySelector('#app-toast');el.textContent=msg;el.hidden=false;clearTimeout(toastTimer);toastTimer=setTimeout(()=>el.hidden=true,4500);}
  function download(filename,content,mime='text/plain;charset=utf-8'){
    const url=URL.createObjectURL(new Blob([content],{type:mime}));const a=document.createElement('a');a.href=url;a.download=filename;a.click();setTimeout(()=>URL.revokeObjectURL(url),1500);toast('已生成文件：'+filename);
  }
  function saveState(){if(window.openai?.setWidgetState)window.openai.setWidgetState({modelContent:{page:state.page,selectedAsset:state.detailId||null,prototype:'栖云资产与应用运维 v0.4'},privateContent:{filter:state.filter}}).catch(()=>{});}
  function render(){const m=meta[state.page]||meta.overview;root.querySelector('#page-title').textContent=m[0];root.querySelector('#breadcrumb-current').textContent=m[0];root.querySelector('#page-eyebrow').textContent=m[1];root.querySelector('#page-description').textContent=m[2];root.querySelector('.heading-date strong').textContent=state.page==='terminal'?'GO · SQLITE':['applications','alerts','integrations','automation'].includes(state.page)?'2026.10.15':state.page==='monitoring'?'2026.10.23':'2026.10.08';root.querySelectorAll('[data-nav]').forEach(b=>{b.classList.toggle('active',b.dataset.nav===state.page);b.setAttribute('aria-current',b.dataset.nav===state.page?'page':'false');});root.querySelector('#page-content').innerHTML=pages[state.page]?pages[state.page]():'';window.AdminShell?.sync();refreshIcons();}
  function navigate(page){if(!pages[page])return;closeModal();window.AdminShell?.beforeNavigate(page);state.page=page;render();saveState();}
  function assetRows(list,compact=false){return list.map(raw=>{const a=safeAsset(raw);const price=a.source==='local'?(a.priceCents?money(a.priceCents):'待补充'):money(a.priceCents);return [
    `<div class="asset-name"><span class="asset-symbol">${icon(a.icon)}</span><div><button class="op-btn op-btn-link" data-action="asset-detail" data-id="${a.id}">${a.name}</button><small class="mono">${a.id}${a.source==='local'?' · 未持久化':''}</small></div></div>`,
    badge(a.status,a.status==='在用'?'green':a.status==='在库'?'blue':a.status==='待核验'?'blue':'amber'),
    `<strong>${a.owner}</strong><small>${a.dept}</small>`,
    `<span class="mono">${a.ip}</span><small>${a.online==='在线'?'<span class="status-dot"></span>在线':a.online==='离线'?`<span class="text-amber">离线 ${a.offline}</span>`:'尚未纳管'}</small>`,
    compact?`<span class="mono">${price}</span><small>${a.age}</small>`:`${a.channel}<small class="mono">${price} · ${a.age}</small>`,
    a.source === 'api'
      ? `<div class="row" style="gap:6px;flex-wrap:wrap"><button class="op-btn op-btn-link" data-action="asset-detail" data-id="${a.id}">查看</button><button class="op-btn op-btn-link" data-action="asset-edit" data-id="${a.apiId}">编辑</button><button class="op-btn op-btn-link" data-action="asset-delete" data-id="${a.apiId}" data-name="${a.name}">删除</button></div>`
      : `<div class="row" style="gap:6px"><button class="op-btn op-btn-link" data-action="asset-detail" data-id="${a.id}">查看</button><button class="op-btn op-btn-link" data-action="asset-delete-local" data-id="${a.id}">移除</button></div>`];});}
  pages.overview=()=>{
    const isHalf=state.trend==='half';const incoming=isHalf?[18,27,22,38,26,42]:[9,8,13,12];const outgoing=isHalf?[14,21,17,29,22,31]:[8,5,10,8];const labels=isHalf?['5 月','6 月','7 月','8 月','9 月','10 月']:['第 1 周','第 2 周','第 3 周','第 4 周'];
    const chart=`<div class="between"><div class="chart-summary"><strong>${incoming.reduce((a,b)=>a+b,0)} <span>件入库</span></strong><span>领用出库 ${outgoing.reduce((a,b)=>a+b,0)} 件</span></div><div class="chart-legend"><span><b></b>入库</span><span><b class="outgoing"></b>出库</span></div></div><div class="bar-chart" role="img" aria-label="${labels.map((l,i)=>`${l}入库${incoming[i]}件，出库${outgoing[i]}件`).join('；')}">${labels.map((l,i)=>`<div class="bar-group"><div class="bar" style="height:${incoming[i]/42*100}%"><span>${incoming[i]}</span></div><div class="bar outgoing" style="height:${outgoing[i]/42*100}%"><span>${outgoing[i]}</span></div><small>${l}</small></div>`).join('')}</div><div class="between small muted"><span>单位：件 · ${isHalf?'10 月截至 08 日，其余为完整月':'9 月按周统计'}</span><span>已确认业务单据</span></div>`;
    const tasks=[['↓','4 台笔记本待验收入库','采购单 PO-202610-001 · 行政部','procurement','amber'],['⇄','李然的设备领用待审批','产品研发部 · 今天 09:12','circulation','blue'],['!','1 个 IP 地址存在冲突','10.20.30.45 · 两个 MAC 观测','ipam','red']].map(t=>`<div class="task-row"><span class="task-icon text-${t[4]}">${t[0]}</span><div class="task-copy"><strong>${t[1]}</strong><p>${t[2]}</p></div><button class="task-arrow" data-action="go" data-page="${t[3]}" aria-label="处理${t[1]}">↗</button></div>`).join('');
    const verified=assets.filter(a=>a.online==='在线').length,pending=assets.filter(a=>a.status==='待核验').length,offline=assets.filter(a=>a.online==='离线').length;
    return `<div class="grid-4">${metric('资产总量',`${assets.length} <small>件</small>`,'来源：Go 服务资产库 · 删除后即时同步')}${metric('已核验主机',`${verified} <small>/ ${assets.length}</small>`,'<span class="status-dot"></span>已批准 SSH 主机指纹')}${metric('待核验',`${pending} <small>项</small>`,`<span class="text-amber">${offline} 台离线</span>`,pending?'amber':'green')}${metric('终端会话','已启用','SSH WebSocket · trzsz 文件传输')}</div>
    ${panel('资产全生命周期',`<div class="flow-strip">${[['01','采购申请','企微审批','procurement'],['02','验收入库','建立资产卡片','procurement'],['03','领用交付','责任到人','circulation'],['04','运营维护','主机 / 网络 / PVE','hosts'],['05','盘点对账','实物与账务核实','inventory'],['06','退役处置','安全擦除 / 归档','governance']].map((s,i)=>`${i?'<span class="flow-arrow">›</span>':''}<div class="flow-step"><span class="flow-number">${s[0]}</span><div><button class="op-btn op-btn-link" data-action="go" data-page="${s[3]}">${s[1]}</button><small style="display:block">${s[2]}</small></div></div>`).join('')}</div>`,badge('全程留痕','green'))}
    <div class="dashboard-middle">${panel('资产出入库趋势',chart,`<button class="op-btn op-btn-small" data-action="toggle-trend">${isHalf?'近 6 个月':'9 月按周'} ⌄</button>`)}${panel('待办与关注',tasks,`<button class="op-btn op-btn-link" data-action="notifications">查看全部 →</button>`)}</div>
    ${panel('近期资产',`${table(['资产名称 / 编码','资产状态','保管人 / 部门','IP 地址 / 在线状态','采购信息',''],assetRows(assets.slice(0,5),true))}<div class="table-tail"><span>${assets.length?'最近登记 '+Math.min(5,assets.length)+' 条 · 共 '+assets.length+' 条':'尚未登记资产'}</span><span>数据来源 Go 服务资产库</span></div>`,`<div class="row">${button('＋ 登记资产','new-asset',true)}<button class="op-btn op-btn-link" data-action="go" data-page="assets">全部资产 →</button></div>`)}
    <div class="notice green between"><span>季度与年终盘点 · 核实数量、保管人和实际位置</span><button class="op-btn op-btn-link" data-action="go" data-page="inventory">继续盘点 →</button></div>`;
  };
  pages.assets=()=>{
    const filtered=assets.filter(a=>(state.filter==='all'||a.status===state.filter||a.online===state.filter)&&Object.values(a).join(' ').toLowerCase().includes(state.query.toLowerCase()));
    return `<div class="toolbar"><div class="row"><label><input id="asset-search" aria-label="筛选资产" placeholder="资产名称、编码、IP、领用人" value="${escape(state.query)}"></label><button class="op-btn" data-action="search-assets">搜索</button><select id="asset-filter" aria-label="资产状态筛选">${[['all','全部状态'],['在用','在用'],['待核验','待核验'],['离线','离线']].map(s=>`<option value="${s[0]}" ${state.filter===s[0]?'selected':''}>${s[1]}</option>`).join('')}</select></div><div class="row">${button('↓ 导出台账','export-assets')}${button('＋ 登记资产','new-asset',true)}</div></div>
    ${panel('全部资产',`${assets.length?table(['资产名称 / 编码','资产状态','保管人 / 部门','IP 地址 / 在线状态','采购信息',''],assetRows(filtered)):'<div class="empty-state" style="padding:44px 0;text-align:center"><p>还没有登记任何资产</p><p class="small muted" style="margin-top:8px">添加 Linux 主机后，凭据加密保存在服务端，可核验指纹并打开真实终端。</p><div class="row" style="justify-content:center;margin-top:16px">'+button('＋ 添加真实主机','new-ssh-asset',true)+'</div></div>'}<div class="table-tail"><span>显示 ${filtered.length} 条 · 全部 ${assets.length} 条（来自 Go 服务资产库）</span><span>点击资产名称查看完整档案</span></div>`,badge('单一数据源','neutral'))}
    <div class="grid-3">${panel('数据来源','<div class="small muted">本页资产全部来自 Go 服务 SQLite 资产库，与远程终端共用同一份数据；在终端删除资产后此处立即消失。<br>带“未持久化”标记的行由尚未接入服务端的采购/入库流程临时创建。</div>')}${panel('责任与归属','<div class="small muted">保管人、使用人、部门、位置与费用归属分别保存，调拨后保留历史快照。</div>')}${panel('观测可信度','<div class="small muted">在线状态来自主机指纹核验与最近一次连接；未核验资产显示“待核验”，不推断其在线与否。</div>')}</div>`;
  };
  function kv(pairs){return `<div class="details-grid">${pairs.map(p=>`<div><div class="detail-label">${p[0]}</div><div class="detail-value">${p[1]}</div></div>`).join('')}</div>`;}
  function showAsset(id,tab='basic'){
    const raw=assets.find(a=>a.id===id);if(!raw)return toast('该资产不存在或已被删除。');const a=safeAsset(raw);state.detailId=id;state.detailTab=tab;
    const detailTabs=[['basic','资产档案'],['telemetry','系统与链路'],['history','变更履历'],['cost','采购与费用']];
    const pending='<span class="muted">待补充</span>';
    let content='';
    if(tab==='basic')content=`${kv([['资产编码',a.id],['资产状态',badge(a.status,a.status==='在用'?'green':'blue')],['资产类型',a.type],['SN 序列号',a.serial?a.serial:pending],['保管人',a.owner==='未指定'?pending:a.owner],['所属部门',a.dept],['实际位置',a.location==='待补充'?pending:a.location],['IP 地址',a.ip],['SSH 端口',String(a.port||'—')],['MAC 地址',a.mac==='待采集'?pending:a.mac],['在线状态',badge(a.online,a.online==='在线'?'green':'amber')],['主机指纹',a.fingerprint?badge('已核验','green'):badge('待核验','amber')],['最近更新',a.lastSeen]])}<div class="notice">资产记录来自 Go 服务资产库。未填写的采购类字段显示为待补充，系统不会生成占位数值。</div>`;
    if(tab==='telemetry'){
      content=`${kv([['操作系统',a.os],['连接方式','SSH · WebSocket 网关'],['目标地址',`${a.ip}:${a.port||22}`],['主机指纹',a.fingerprint||pending],['采集方式','尚未接入主机指标采集'],['采集时间','—']])}<div class="notice amber">主机指标、已安装软件与端口采集尚未接入，本页不展示推断数据。可在远程终端内执行命令查看实时状态。</div>${table(['可执行操作','入口','说明'],[['打开交互式终端','远程终端','真实 SSH PTY，支持 trzsz 文件传输'],['查看连接诊断','远程终端资产行 ◎','逐项显示认证失败原因']])}`;
    }
    if(tab==='history')content=`<div class="timeline">${[
      [a.lastSeen,'最近一次更新',`资产状态 ${a.status} · 在线 ${a.online} · 版本记录随服务端变更递增`],
      [a.purchased&&a.purchased!=='—'?a.purchased:'—','采购与入库',`${a.purchase} · ${a.channel} · ${a.purchased!=='—'?a.purchased:'采购信息待补充'}`],
      ['—','资产建档','由 Go 服务资产库创建，凭据以 AES-GCM 加密保存']
    ].map(x=>`<div class="timeline-item"><small>${x[0]}</small><strong>${x[1]}</strong><p>${x[2]}</p></div>`).join('')}</div><div class="notice">变更记录由服务端审计表保存；采购、领用与盘点单据尚未与资产库打通，完成后会合并到此处。</div>`;
    if(tab==='cost')content=`${kv([['采购单 / 明细行',a.purchase],['供应商',a.supplier],['购买渠道',a.channel],['采购含税单价',money(a.priceCents)],['购买数量','1 件（当前资产）'],['币种','CNY'],['费用归属',a.cost],['取得日期',a.purchased],['保修到期',a.warranty]])}<div class="notice">采购价、资产原值与折旧后净值单独核算。折旧年限、残值率、税额和费用分摊按企业会计政策配置；服务端尚未计算会计折旧。</div><div class="row">${button('查看财务对账','go',false,'data-page="finance"')}${button('查看采购记录','go',false,'data-page="procurement"')}</div>`;
    const remote=raw.source==='api'?button('编辑资产','asset-edit',false,`data-id="${raw.apiId}"`)+button('连接终端','asset-remote',true,`data-id="${a.id}" ${a.online!=='在线'?'disabled':''}`):button('移除本地记录','asset-delete-local',false,`data-id="${a.id}"`);
    modal(`${a.name} ${badge(a.status,'green')}`,`<div class="row small muted" style="margin-bottom:20px"><span class="mono">${a.id}</span><span>·</span><span>${a.dept} / ${a.owner}</span></div><div class="tabs">${detailTabs.map(t=>`<button class="${tab===t[0]?'active':''}" data-action="asset-tab" data-tab="${t[0]}">${t[1]}</button>`).join('')}</div>${content}`,button('关闭','close-modal')+remote);saveState();
  }
  pages.governance=()=>`${panel('角色与职责边界',table(['角色','业务范围','可执行动作','限制'],[['员工','本人资产与申请','领用申请、交接确认','不能批准自己的申请'],['采购负责人','采购与入库','登记采购、验收实物','无财务更正权限'],['行政管理员','库存与资产流转','审批、出库、调拨、盘点','差异调整需独立复核'],['财务人员','全部已确认业务明细','查询、核对、导出','不直接修改业务原单'],['IT 运维人员','授权主机与网络设备','会话、配置、PVE 任务','按资源授权；敏感操作复核']]),badge('建议权限模型','neutral'))}
    <div class="grid-2">${panel('安全访问',`<div class="stack small"><div class="between"><span>统一身份 / SSO + MFA</span>${badge('接入规划','blue')}</div><div class="between"><span>凭据托管与短期授权</span>${badge('接入规划','blue')}</div><div class="between"><span>SSH / RDP 会话审计</span>${badge('模拟预览','amber')}</div><div class="between"><span>文件传输记录与审批</span>${badge('模拟预览','amber')}</div></div>`) }${panel('还应补齐的环节',`<div class="stack small"><span>保修续保、维修工单、借用归还与离职回收</span><span>软件授权、补丁基线、凭据轮换与到期提醒</span><span>备份恢复演练、退役擦除与处置凭证</span><span>IPv6 / DNS / DHCP、地址冲突与采集失联告警</span></div>`)}</div>
    ${panel('操作审计样例',table(['时间','操作人','对象','动作','结果'],[['2026-10-08 09:42','林远','10.20.20.6','SSH 会话申请','待授权'],['2026-10-08 09:35','赵会计','FIN-202609','导出对账快照','成功 · 已记录筛选条件'],['2026-10-08 09:18','周启明','10.20.30.45','发现重复 MAC / IP 关系','告警'],['2026-10-08 09:02','陈行政','ST-2026-Q3','提交盘点差异复核','待复核']]),badge('留痕预览','neutral'))}<div class="notice">实际权限由服务端校验；审计日志需独立存储、设置保留策略与防篡改措施。此页面展示拟定的职责边界。</div>`;
  Object.assign(actions,{
    'go':b=>navigate(b.dataset.page),'close-modal':closeModal,
    'toggle-trend':()=>{state.trend=state.trend==='half'?'month':'half';render();},
    'asset-detail':b=>showAsset(b.dataset.id),'asset-tab':b=>showAsset(state.detailId,b.dataset.tab),
    'search-assets':()=>{state.query=root.querySelector('#asset-search').value;render();},
    'export-assets':()=>{const list=assets.filter(a=>(state.filter==='all'||a.status===state.filter||a.online===state.filter)&&Object.values(a).join(' ').toLowerCase().includes(state.query.toLowerCase()));const cell=v=>{let s=String(v);if(/^[\s]*[=+@-]|^[\t\r\n]/.test(s))s="'"+s;return '"'+s.replaceAll('"','""')+'"';};download('资产台账-'+new Date().toISOString().slice(0,10)+'.csv','\ufeff'+[['资产编码','名称','状态','保管人','部门','IP','MAC','在线状态','最近更新','供应商','采购价（元）','购买日期'],...list.map(a=>[a.id,a.name,a.status,a.owner,a.dept,a.ip,a.mac,a.online,a.lastSeen,a.supplier,a.priceCents?(a.priceCents/100).toFixed(2):'',a.purchased==='—'?'':a.purchased])].map(r=>r.map(cell).join(',')).join('\r\n'),'text/csv;charset=utf-8');},
    'new-asset':()=>modal('登记资产',`<div class="notice">两种方式，请按是否需要远程登录选择：</div><div class="stack" style="margin-top:16px">
      <div class="between"><div><strong class="small">添加 Linux 主机（真实 SSH）</strong><p class="small muted">写入 Go 服务资产库 · 凭据 AES-GCM 加密 · 需核验主机指纹后才能连接</p></div>${button('去添加','new-ssh-asset',true)}</div>
      <div class="between"><div><strong class="small">登记无远程访问的 IT 设备</strong><p class="small muted">笔记本、打印机等 · 仅保存在当前浏览器，刷新后不保留</p></div>${button('本地登记','new-local-asset')}</div></div>`,button('关闭','close-modal')),
    'new-ssh-asset':()=>{closeModal();navigate('terminal');setTimeout(()=>actions['terminal-add-asset']?.(),120);},
    'new-local-asset':()=>modal('登记本地 IT 设备',`<div class="notice">此记录只写入当前浏览器，不会出现在服务端资产库，也无法用于远程连接。需要远程登录的服务器请用“添加 Linux 主机”。</div><form id="new-asset-form"><div class="form-grid"><div class="field"><label for="new-name">资产名称 *</label><input id="new-name" name="name" required placeholder="例如 ThinkPad T14"></div><div class="field"><label for="new-serial">SN 序列号 *</label><input id="new-serial" name="serial" required placeholder="唯一硬件序列号"></div><div class="field"><label for="new-owner">保管人 / 库房 *</label><input id="new-owner" name="owner" required value="行政资产库"></div><div class="field"><label for="new-price">采购价格（元）</label><input id="new-price" name="price" type="number" min="0" step="0.01" placeholder="可留空"></div><div class="field"><label for="new-date">购买日期</label><input id="new-date" name="date" type="date"></div><div class="field"><label for="new-channel">购买渠道</label><select id="new-channel" name="channel"><option>京东企业购</option><option>厂商直采</option><option>授权代理商</option><option>框架协议采购</option><option>待补充</option></select></div></div></form>`,button('取消','close-modal')+button('保存到当前浏览器','save-asset',true)),
    'save-asset':()=>{const f=root.querySelector('#new-asset-form');if(!f.reportValidity())return;const v=Object.fromEntries(new FormData(f));if(!v.name.trim()||!v.serial.trim()||!v.owner.trim()){toast('资产名称、序列号和保管人不能为空。');return;}if(assets.some(a=>(a.serial||'').toUpperCase()===v.serial.trim().toUpperCase())){toast('此 SN 已存在，请检查重复资产。');return;}assets.unshift({source:'local',id:'LOCAL-'+String(assets.filter(a=>a.source==='local').length+1).padStart(4,'0'),name:v.name.trim(),serial:v.serial.trim(),owner:v.owner.trim(),dept:'未分类',type:'IT 设备',icon:'laptop',os:'待采集',status:'在库',online:'未纳管',ip:'—',mac:'待采集',priceCents:v.price?Math.round(Number(v.price)*100):0,purchased:v.date||'—',channel:v.channel,supplier:'待补充',location:'待补充',warranty:'—',lastSeen:'—',lastUsed:'—',idle:'—',offline:'—',purchase:'待补充',used:'—',cost:'待补充',age:'—',history:[['—','存量资产登记',`序列号 ${v.serial.trim()} · 仅保存在当前浏览器`]]});closeModal();state.query='';state.filter='all';navigate('assets');toast('已保存到当前浏览器；该记录未写入服务端，刷新后不保留。');},
    'asset-remote':b=>{const a=assets.find(x=>x.id===b.dataset.id);if(!a)return;if(a.source!=='api')return toast('该记录未写入服务端，无法建立真实会话。');if(a.online!=='在线')return toast('资产尚未通过 SSH 指纹核验，或当前不可连接。');navigate('terminal');setTimeout(()=>window.AssetDemo?.terminalWorkspace?.openHost?.(a.apiId),60);},
    'asset-edit':b=>assetEditor(b.dataset.id),
    'asset-save':b=>saveAssetEdit(b),
    'asset-delete':b=>deleteAssetConfirm(b.dataset.id,b.dataset.name),
    'asset-delete-local':b=>{const index=assets.findIndex(a=>a.id===b.dataset.id&&a.source==='local');if(index<0)return;assets.splice(index,1);closeModal();render();toast('本地记录已移除。');},
    'notifications':()=>modal('待办与关注',`<div class="stack">${[['4 台笔记本待验收入库','采购与入库','procurement'],['领用申请待主管审批','领用与流转','circulation'],['10.20.30.45 地址冲突','IP 地址管理','ipam'],['季度盘点差异待复核','盘点管理','inventory'],['采购金额与凭证存在差异','财务对账','finance']].map(t=>`<div class="between"><div><strong class="small">${t[0]}</strong><p class="small muted">${t[1]}</p></div>${button('查看 →','go',false,`data-page="${t[2]}"`)}</div>`).join('')}</div>`,button('关闭','close-modal'))
  });
  function init(){
    root.addEventListener('submit',e=>{e.preventDefault();const primary=root.querySelector('#modal-layer .modal-footer .op-btn-primary');if(primary&&!primary.disabled)primary.click();});
    root.querySelector('#app-nav').innerHTML=nav.map(n=>`${n[0]?`<div class="nav-section">${n[0]}</div>`:''}<button type="button" class="nav-link" data-nav="${n[1]}" aria-label="${n[2]}" data-tooltip="${n[2]}">${icon(n[3])}<span class="nav-label">${n[2]}</span>${n[4]?`<span class="nav-count">${n[4]}</span>`:''}</button>`).join('');
    const saved=window.openai?.widgetState?.modelContent;if(saved?.prototype==='栖云资产与应用运维 v0.4'&&pages[saved.page])state.page=saved.page;
    root.addEventListener('click',e=>{const navButton=e.target.closest('[data-nav]');if(navButton){navigate(navButton.dataset.nav);return;}const b=e.target.closest('[data-action]');if(b&&!b.disabled){const fn=actions[b.dataset.action];if(fn)fn(b);}if(e.target===root.querySelector('#modal-layer'))closeModal();});
    root.addEventListener('change',e=>{if(e.target.id==='asset-filter'){state.filter=e.target.value;render();}});
    root.addEventListener('keydown',e=>{if(e.key==='Enter'&&e.target.closest('form')&&e.target.tagName!=='TEXTAREA'){e.preventDefault();const primary=root.querySelector('#modal-layer .modal-footer .op-btn-primary');if(primary&&!primary.disabled)primary.click();return;}if(e.key==='Escape'){closeModal();return;}if(e.key==='Enter'&&e.target.id==='global-search'){state.query=e.target.value.trim();state.filter='all';navigate('assets');}if(e.key==='Enter'&&e.target.id==='asset-search')actions['search-assets']();if(e.key==='Tab'&&!root.querySelector('#modal-layer').hidden){const focusables=[...root.querySelector('#modal-layer').querySelectorAll('button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href]')];const first=focusables[0],last=focusables[focusables.length-1];if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus();}}});
    window.addEventListener('openai:set_globals',e=>{const saved=e.detail?.globals?.widgetState?.modelContent;if(saved?.prototype==='栖云资产与应用运维 v0.4'&&pages[saved.page]&&state.page!==saved.page){state.page=saved.page;render();}});
    syncAssets();
    window.QiyunAPI?.onAssets?.(syncAssets);
    window.AdminShell?.mount();
    render();
    if(globalThis.Tweak && !window.AdminShell){const design={density:'舒展',accent:'#07836c'};const tweak=new Tweak({container:root,onChange:()=>{root.style.setProperty('--accent-color',design.accent);root.classList.toggle('compact-design',design.density==='紧凑');}});tweak.addSelect(design,'density',{label:'信息密度',options:['舒展','紧凑']});tweak.addColorPicker(design,'accent',{label:'品牌强调色'});}
  }
  return {root,state,pages,actions,assets,nav,meta,icon,badge,button,panel,metric,table,modal,closeModal,toast,render,navigate,escape,download,init,syncAssets,ledgerRow,assetEditor,deleteAssetConfirm};
})();


