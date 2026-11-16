(() => {
  'use strict';
  const $ = id => document.getElementById(id), status = $('finance-live-state');
  if (!status) return;
  let stream = null, stopped = false, probing = false;
  const query = status.dataset.month ? "?month=" + encodeURIComponent(status.dataset.month) : "";
  const stamp = value => new Intl.DateTimeFormat('pt-BR',{timeZone:'America/Sao_Paulo',day:'2-digit',month:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'}).format(new Date(value));
  function connection(text, state) {
    status.textContent = text; status.dataset.state = state;
    const screenStatus = $('office-finance-state');
    if (screenStatus) { screenStatus.textContent = text; screenStatus.dataset.state = state; }
    window.dispatchEvent(new CustomEvent('finance-connection',{detail:{state}}));
  }
  function update(data) {
    if (!data || !['manual','bank'].includes(data.mode) || typeof data.expense_text !== 'string' || typeof data.net_worth_text !== 'string') return;
    if (document.body?.dataset.production === 'true' && document.body?.dataset.codexEnabled !== 'true') data = {...data,activity_text:'IA não configurada'};
    const text = {'live-net-worth':data.net_worth_text,'live-expenses':data.expense_text,'live-pending':data.pending_text,'live-top-category':data.top_category,'live-top-amount':data.top_category_text,'live-activity':data.activity_text,
      'office-finance-wealth':data.net_worth_text,'office-finance-expenses':data.expense_text,'office-finance-pending':data.pending_text,
      'office-finance-category':data.top_category,'office-finance-category-amount':data.top_category_text,'office-finance-activity':data.activity_text,'office-finance-month':data.month_label};
    for (const [id,value] of Object.entries(text)) if ($(id)) $(id).textContent = value;
    $('live-wealth-source').textContent = data.net_worth_date ? 'Patrimônio informado por você · referência ' + data.net_worth_date.split('-').reverse().join('/') : 'Informe ativos e dívidas para calcular seu patrimônio manual.';
    if ($('live-finance-month')) $('live-finance-month').textContent = data.month_label + ' · acompanhamento financeiro';
    $('finance-live-stamp').textContent = 'Conferido em ' + stamp(data.snapshot_at) + (data.mode === 'bank' ? ' · gastos bancários via Pluggy, em BRL.' : ' · gastos informados por você.');
    if ($('office-finance-stamp')) $('office-finance-stamp').textContent = 'Conferido em ' + stamp(data.snapshot_at);
    if ($('office-finance-source')) $('office-finance-source').textContent = data.net_worth_date ? 'Ativos menos dívidas · referência ' + data.net_worth_date.split('-').reverse().join('/') : 'Cadastre seus ativos e dívidas para calcular o patrimônio. Os gastos aparecem após o primeiro lançamento.';
    // Preserve an in-progress edit when another tab changes the snapshot.
    if (!$('net-worth-editor').open) {
      if ($('net-worth-assets')) $('net-worth-assets').value = data.assets_text || '';
      if ($('net-worth-liabilities')) $('net-worth-liabilities').value = data.liabilities_text || '';
    }
    connection('Ao vivo','live');
    window.dispatchEvent(new CustomEvent('finance-summary',{detail:data}));
  }
  function start() {
    if (stopped || stream || document.hidden) return;
    connection('Conectando…','connecting');
    stream = new EventSource('/api/finance/events' + query);
    stream.addEventListener('finance.snapshot',event => { try { update(JSON.parse(event.data)); } catch { connection('Resposta indisponível','stale'); } });
    stream.addEventListener('finance.unavailable',() => connection('Dados indisponíveis','stale'));
    stream.addEventListener('session.expired',() => { stopped = true; stream.close(); location.assign('/login'); });
    stream.onopen = () => connection('Ao vivo','live');
    stream.onerror = async () => {
      connection('Reconectando…','stale');
      if (probing || stopped || document.hidden) return;
      probing = true;
      try {
        const response = await fetch('/api/finance/summary' + query,{credentials:'same-origin'});
        if (response.status === 401 && !document.hidden) { stopped = true; stream?.close(); location.assign('/login'); }
      } catch {} finally { probing = false; }
    };
  }
  document.addEventListener('visibilitychange',() => {
    if (document.hidden) { stream?.close(); stream = null; connection('Pausado nesta aba','paused'); }
    else start();
  });
  window.addEventListener('pagehide',() => { stream?.close(); stream = null; });
  window.addEventListener('pageshow',start);
  start();
})();
