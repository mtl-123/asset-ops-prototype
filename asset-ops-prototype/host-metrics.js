(function () {
  'use strict';
  // 主机指标客户端：只负责取数、轮询与广播，渲染留给 infra.js。
  // 服务端每 60 秒通过 SSH 采样一次；这里拿到的是最近一次结果加上历史曲线。
  const state = {
    items: new Map(), interval: 60, fetchedAt: 0, timer: 0,
    loading: false, error: null, listeners: new Set()
  };

  function api() { return window.QiyunAPI; }
  function emit() {
    state.listeners.forEach(fn => { try { fn(state); } catch (e) { console.warn('host metrics listener failed', e); } });
  }
  function put(item) {
    if (!item || !item.asset_id) return;
    state.items.set(item.asset_id, item);
  }

  async function refresh() {
    if (state.loading || !api()?.request) return state.items;
    state.loading = true;
    try {
      const data = await api().request('/api/v1/metrics');
      const next = new Map();
      (data.items || []).forEach(item => next.set(item.asset_id, item));
      state.items = next;
      state.interval = data.interval_seconds || 0;
      state.fetchedAt = Date.now();
      state.error = null;
    } catch (error) {
      state.error = error;
    } finally {
      state.loading = false;
      emit();
    }
    return state.items;
  }

  async function collect(assetId) {
    if (!api()?.request) throw new Error('服务未就绪');
    const item = await api().request('/api/v1/metrics/' + encodeURIComponent(assetId) + '/collect', { method: 'POST', body: '{}' });
    put(item);
    emit();
    return item;
  }

  async function history(assetId, limit = 60) {
    const data = await api().request('/api/v1/metrics/' + encodeURIComponent(assetId) + '/history?limit=' + limit);
    return data.samples || [];
  }

  function get(assetId) { return state.items.get(assetId) || null; }

  function summary() {
    const items = Array.from(state.items.values());
    return {
      total: items.length,
      ok: items.filter(item => item.status === 'ok').length,
      failed: items.filter(item => item.status === 'error').length,
      pending: items.filter(item => item.status === 'pending').length,
      interval: state.interval,
      fetchedAt: state.fetchedAt,
      error: state.error
    };
  }

  function start() {
    stop();
    const seconds = Math.max(30, state.interval || 60);
    state.timer = setInterval(() => {
      // 后台标签页不轮询，避免无意义的 SSH 采样。
      if (!document.hidden) refresh();
    }, seconds * 1000);
  }
  function stop() {
    if (state.timer) clearInterval(state.timer);
    state.timer = 0;
  }
  function on(fn) { state.listeners.add(fn); return () => state.listeners.delete(fn); }

  window.HostMetrics = { refresh, collect, history, get, summary, on, start, stop };
  refresh().then(start).catch(() => {});
})();
