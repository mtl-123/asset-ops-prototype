(function (root, factory) {
  const model = factory();
  if (typeof module === 'object' && module.exports) module.exports = model;
  else root.MonitorModel = model;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const NOW = Date.parse('2026-10-23T14:30:00+08:00');
  const PERIODS = { hour: 3600000, day: 86400000, week: 604800000 };
  const STATUSES = ['up', 'down', 'unknown', 'maintenance', 'paused'];
  function summarize(samples, from, to) {
    let up = 0, down = 0, unknown = 0, excluded = 0;
    const times = [];
    const sorted = samples.filter(s => Number.isFinite(s.t)).slice().sort((a,b)=>a.t-b.t);
    for (let i=0;i<sorted.length;i++) {
      const s=sorted[i], end=Math.min(to,s.t+(s.duration||60000),sorted[i+1]?.t??Infinity), duration=Math.max(0,end-Math.max(from,s.t));
      if(!duration)continue;
      if(s.status==='up')up+=duration;
      else if(s.status==='down')down+=duration;
      else if(['maintenance','paused'].includes(s.status))excluded+=duration;
      else unknown+=duration;
      if(s.status==='up'&&Number.isFinite(s.ms))times.push(s.ms);
    }
    const window=Math.max(0,to-from),known=up+down,eligible=Math.max(0,window-excluded);
    unknown=Math.max(unknown,eligible-known);
    times.sort((a,b)=>a-b);
    return {up,down,unknown,excluded,known,eligible,uptime:known?100*up/known:null,coverage:eligible?100*known/eligible:null,average:times.length?times.reduce((a,b)=>a+b,0)/times.length:null,p95:times.length?times[Math.max(0,Math.ceil(times.length*.95)-1)]:null};
  }
  function currentStatus(m,samples,now) {
    if(m.paused)return 'paused';
    if(m.maintenance&&m.maintenance.start<=now&&m.maintenance.end>now)return 'maintenance';
    if(m.awaitingSample)return 'pending';
    const last=samples.at(-1);
    if(!last)return 'pending';
    if(now-last.t>Math.max(180000,m.interval*3000))return 'unknown';
    if(last.status==='up'&&last.ms>m.slowMs)return 'degraded';
    return last.status;
  }
  function validateMonitor(input) {
    const errors=[];
    const val={id:String(input.id||''),name:String(input.name||'').trim().slice(0,80),type:String(input.type||''),target:String(input.target||'').trim(),owner:String(input.owner||'').trim().slice(0,60),group:String(input.group||'').trim().slice(0,40),serviceId:String(input.serviceId||''),hostId:String(input.hostId||''),interval:Number(input.interval),timeout:Number(input.timeout),retries:Number(input.retries),slowMs:Number(input.slowMs),keyword:String(input.keyword||'').trim().slice(0,120),port:input.port===''?0:Number(input.port||0),public:input.public===true,publicName:String(input.publicName||input.name||'').trim().slice(0,80)};
    if(val.type!=='tcp')val.port=0;
    if(val.type!=='keyword')val.keyword='';
    if(!val.name||!val.owner||!val.group)errors.push('名称、负责人和分组不能为空。');
    if(!['https','keyword','tcp','dns','push'].includes(val.type))errors.push('请选择有效的监控类型。');
    if(['https','keyword'].includes(val.type)){try{const u=new URL(val.target);if(!['http:','https:'].includes(u.protocol)||u.username||u.password)throw Error();if([...u.searchParams.keys()].some(k=>/token|password|secret|api.?key|authorization/i.test(k)))errors.push('目标地址不能携带密码、令牌或 API Key。');val.target=u.href;}catch{errors.push('请输入不含账号密码的 HTTP / HTTPS 地址。');}}
    else if(!/^[a-zA-Z0-9][a-zA-Z0-9.:-]{0,252}$/.test(val.target))errors.push(val.type==='push'?'请输入任务标识，例如 nightly-backup。':'请输入有效主机名或 IP，不能包含路径。');
    if(val.type==='tcp'&&(!Number.isInteger(val.port)||val.port<1||val.port>65535))errors.push('TCP 端口必须为 1–65535 的整数。');
    if(val.type==='keyword'&&!val.keyword)errors.push('关键词监控需要填写内容断言。');
    if(!Number.isInteger(val.interval)||val.interval<20||val.interval>3600)errors.push('检查间隔须为 20–3600 秒。');
    if(!Number.isInteger(val.timeout)||val.timeout<1||val.timeout>=val.interval)errors.push('超时须大于 0 且小于检查间隔。');
    if(!Number.isInteger(val.retries)||val.retries<0||val.retries>10)errors.push('重试次数须为 0–10。');
    if(!Number.isFinite(val.slowMs)||val.slowMs<50||val.slowMs>60000)errors.push('响应提醒阈值须在 50–60000 ms 之间。');
    return {value:val,errors};
  }
  function samplesFor(m) {
    if(!m.seed)return [];
    const seed={web:7,tickets:13,api:23,dns:31,push:47,maintenance:61,paused:71}[m.seed]||11;
    const values=[];
    for(let i=0;i<10080;i++){
      const t=NOW-604800000+i*60000,ago=10079-i;
      let status='up',ms=Math.round(32+seed*2+12*Math.sin(i*.17)+7*Math.sin(i*.053));
      if(m.seed==='web'&&i>8100&&i<8107)status='down';
      if(m.seed==='tickets'&&ago<25)ms=1800+Math.round(900*Math.abs(Math.sin(i*.13)));
      if(m.seed==='tickets'&&i>6500&&i<6516)status='down';
      if(m.seed==='api'&&ago<12)status='down';
      if(m.seed==='push'&&ago<24)status='unknown';
      if(m.seed==='push')ms=null;
      if(m.seed==='maintenance'&&ago<45)status='maintenance';
      if(m.seed==='paused'&&ago<120)status='paused';
      if(status!=='up')ms=null;
      values.push({t,duration:60000,status,ms});
    }
    return values;
  }
  function bucket(samples,from,to,count=50){
    const step=(to-from)/count;
    return Array.from({length:count},(_,i)=>{
      const start=from+i*step,end=start+step,group=samples.filter(s=>s.t<end&&s.t+(s.duration||60000)>start);
      const summary=summarize(group,start,end);
      const status=summary.down>0?'down':summary.unknown>0?'unknown':summary.known>0?'up':group.some(s=>s.status==='maintenance')?'maintenance':group.length?'paused':'unknown';
      return {start,end,status,ms:summary.average};
    });
  }
  function incidents(samples,from,to){
    const list=[];let event=null;
    for(const s of samples.filter(s=>s.t>=from&&s.t<to)){
      if(s.status==='down'){if(!event)event={start:s.t,end:null,duration:0};event.duration+=s.duration||60000;}
      else if(event&&s.status==='up'){event.end=s.t;list.push(event);event=null;}
    }
    if(event)list.push(event);
    return list.reverse();
  }
  function saveable(m){return {...validateMonitor(m).value,seed:m.seed||null,paused:!!m.paused,awaitingSample:!!m.awaitingSample,maintenance:m.maintenance?{start:m.maintenance.start,end:m.maintenance.end,reason:String(m.maintenance.reason||'').slice(0,150)}:null};}
  function restore(raw){
    if(!raw||raw.version!==1||!Array.isArray(raw.monitors)||raw.monitors.length>200)return null;
    const ids=new Set(),monitors=[];
    for(const m of raw.monitors){if(!m||typeof m!=='object'||Array.isArray(m))return null;const check=validateMonitor(m);if(check.errors.length||!/^MON-[a-zA-Z0-9-]+$/.test(m.id)||ids.has(m.id))return null;ids.add(m.id);if(m.seed&&!['web','tickets','api','dns','push','maintenance','paused'].includes(m.seed))return null;const value=saveable(m);if(value.maintenance&&(!Number.isFinite(value.maintenance.start)||!Number.isFinite(value.maintenance.end)||value.maintenance.end<=value.maintenance.start))return null;monitors.push(value);}
    return monitors;
  }
  function safePublic(monitors,statusFn){return monitors.filter(m=>m.public).map(m=>({name:m.publicName||m.name,status:statusFn(m)}));}
  return {NOW,PERIODS,STATUSES,summarize,currentStatus,validateMonitor,samplesFor,bucket,incidents,saveable,restore,safePublic};
});
