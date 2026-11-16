(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  if (!$('autonomy-panel')) return;
  const node = (tag, text, className) => {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    if (className) el.className = className;
    return el;
  };
  const stamp = value => {
    const date = value ? new Date(value) : null;
    return !date || Number.isNaN(date.getTime()) ? 'Ainda não registrado' : new Intl.DateTimeFormat('pt-BR', {timeZone:'America/Sao_Paulo', day:'2-digit', month:'2-digit', hour:'2-digit', minute:'2-digit'}).format(date);
  };
  const states = {pending:'Na fila', waiting_budget:'Aguardando cota', running:'Em execução', complete:'Concluída', partial:'Parcial', failed:'Falhou', failed_unknown:'Resultado não confirmado', stale:'Contexto alterado', await_data:'Aguardando dados', cancelled:'Cancelada', skipped:'Não executada'};
  const topics = {personal_finance:'Suas finanças', brazil_macro:'Cenário brasileiro', fixed_income:'Renda fixa', funds:'Fundos', long_term:'Longo prazo', global:'Cenário global', crypto:'Criptoativos'};
  const stages = ['Pesquisa pública', 'Estratégia', 'Risco', 'Crítica independente', 'Síntese'];
  let agents = {};
  window.addEventListener('conselho-agents',event => { const next = Object.fromEntries(event.detail.map(agent => [agent.id,agent.name]));
    if (JSON.stringify(next) !== JSON.stringify(agents)) {
      agents = next; listSignature = '';
      if (current) render(current);
    } });
  const stageLabel = task => (agents[task.agent_id] || task.agent_id || 'Conselho') + ' · ' + (stages[task.stage] || 'Análise');
  const sourceStates = {ok:'Disponível', failed:'Consulta indisponível', error:'Consulta indisponível', pending:'Aguardando consulta', unknown:'Aguardando consulta', unchanged:'Sem alterações'};
  const errors = {waiting_budget:'Aguardando cota para concluir a análise', personal_data_missing:'Complete e confirme seus dados e objetivos', product_evidence_missing:'Faltam fontes atuais sobre o produto', risk_profile_unknown:'Perfil de risco ainda não confirmado', risk_profile_unconfirmed:'Perfil de risco ainda não confirmado', source_unavailable:'Fonte indisponível', dated_evidence_missing:'Falta evidência com data de publicação', required_data_missing:'Faltam dados para avaliar a proposta', context_changed:'Os dados mudaram durante a análise', evidence_changed:'Uma fonte foi atualizada durante a análise', rate_limited:'Limite diário atingido', failed_unknown:'Resultado da execução não confirmado', not_authenticated:'Runner aguardando autenticação', unavailable:'Runner indisponível', paused:'Acompanhamento pausado'};
  const taskState = task => task.error_code === 'rate_limited' && task.status === 'pending' ? 'Aguardando cota' : states[task.status] || task.status;
  let current, timer, busy = false, listSignature = '', strategySignature = '', selectedRun = '', detailSignature = '';
  async function request(url, options = {}) {
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const response = await fetch(url, {...options, credentials:'same-origin', signal:controller.signal, headers:{Accept:'application/json', ...options.headers}});
      if (response.status === 401) { location.assign('/login?return_to='+encodeURIComponent(selectedRun ? '/reports?run='+selectedRun : location.pathname+location.search)); throw Error('Entre novamente para consultar o acompanhamento.'); }
      const body = await response.json();
      if (!response.ok) throw Error(body.error || 'A consulta não foi concluída. Tente novamente.');
      return body;
    } finally { clearTimeout(timeout); }
  }
  function link(value, title) {
    const el = node('a', title || value);
    try {
      const url = new URL(value);
      if (url.protocol !== 'https:' || url.username || url.password) return node('span', title || 'Fonte sem endereço válido');
      el.href = url.href; el.target = '_blank'; el.rel = 'noopener noreferrer';
    } catch { return node('span', title || 'Fonte sem endereço válido'); }
    return el;
  }
  function render(status) {
    current = status;
    const fresh = status.heartbeat_at && Date.now() - new Date(status.heartbeat_at).getTime() < 180000;
    const label = !status.enabled ? 'Desativado' : status.paused ? 'Pausado' : status.state === 'waiting_budget' ? 'Aguardando cota para concluir a análise' : fresh ? 'Supervisor ativo' : status.heartbeat_at ? 'Sem atualização recente' : 'Aguardando o supervisor';
    $('autonomy-state').textContent = label;
    $('autonomy-panel').dataset.state = !status.enabled ? 'disabled' : status.paused ? 'paused' : fresh ? 'active' : 'stale';
    $('autonomy-heartbeat').textContent = 'Último sinal: ' + stamp(status.heartbeat_at);
    $('autonomy-next-run').textContent = status.paused ? 'Pausada' : stamp(status.next_run_at);
    $('autonomy-next-collect').textContent = status.paused ? 'Pausada' : stamp(status.next_collect_at);
    $('autonomy-budget').textContent = `${status.auto_attempts || 0} de ${status.auto_daily_limit || 20} tentativas automáticas · ${status.attempts || 0} de ${status.daily_limit || 30} no total hoje. ${status.reserved_calls || 0} reservadas para concluir a análise em andamento. Suas finanças têm prioridade; pesquisas gerais usam a cota restante.`;
    $('autonomy-pause').textContent = status.paused ? 'Retomar' : 'Pausar';
    $('autonomy-pause').disabled = busy || !status.enabled;
    $('autonomy-run').disabled = busy || !status.enabled || status.paused;
    const signature = JSON.stringify([status.sources, status.runs, status.tasks]);
    if (signature === listSignature) return;
    listSignature = signature;
    const sources = (status.sources || []).map(source => {
      const li = node('li');
      li.append(link(source.url, source.name), node('p', (sourceStates[source.state] || source.state || 'Sem estado') + ' · Último sucesso: ' + stamp(source.last_success_at), 'autonomy-note'));
      return li;
    });
    $('autonomy-sources').replaceChildren(...(sources.length ? sources : [node('li', 'As fontes aparecerão após a primeira consulta.')]));
    const runs = (status.runs || []).map(run => {
      const li = node('li'), button = node('button', (topics[run.topic] || 'Rodada de análise') + ' · ' + (states[run.status] || run.status), 'autonomy-run-link');
      button.type = 'button'; button.addEventListener('click', () => showRun(run.id));
      li.append(button, node('p', stamp(run.created_at) + (run.error_code ? ' · ' + (errors[run.error_code] || 'Consulte as limitações da rodada') : ''), 'autonomy-note'));
      return li;
    });
    $('autonomy-runs').replaceChildren(...(runs.length ? runs : [node('li', 'Nenhuma rodada registrada.')]));
    const latest = status.runs?.[0]?.id;
    const tasks = (status.tasks || []).filter(task => task.run_id === latest).map(task => node('li', stageLabel(task) + ' · ' + taskState(task)));
    $('autonomy-tasks').replaceChildren(...tasks);
  }
  function renderStrategies(strategies) {
    const signature = JSON.stringify(strategies);
    if (signature === strategySignature) return;
    strategySignature = signature;
    const entries = strategies.map(strategy => {
      const li = node('li'), detail = node('details');
      detail.append(node('summary', (strategy.title || 'Estudo de estratégia') + ' · versão ' + (strategy.version || 1)));
      detail.append(node('p', strategy.hypothesis || 'Hipótese não informada.'));
      detail.append(node('p', 'Estado: ' + (strategy.status === 'research' ? 'Em estudo' : strategy.status || 'Em estudo') + ' · Horizonte: ' + (strategy.time_horizon || 'não informado'), 'autonomy-note'));
      for (const [key, label] of [['conditions','Condições'], ['invalidation_conditions','O que invalida a hipótese'], ['eligible_instruments','Instrumentos em estudo'], ['risks','Riscos']]) {
        if (!strategy[key]?.length) continue;
        detail.append(node('h4', label)); const list = node('ul');
        for (const value of strategy[key]) list.append(node('li', value));
        detail.append(list);
      }
      li.append(detail); return li;
    });
    $('autonomy-strategies').replaceChildren(...(entries.length ? entries : [node('li', 'As estratégias ficam registradas após uma rodada concluída.')]));
  }
  async function showRun(id) {
    selectedRun = id;
    $('autonomy-detail').hidden = false;
    $('autonomy-detail').replaceChildren(node('p', 'Consultando a rodada…'));
    detailSignature = '';
    await refreshDetail();
  }
  async function refreshDetail() {
    if (!selectedRun) return;
    const id = selectedRun;
    try {
      const detail = await request('/api/autonomy/runs/' + encodeURIComponent(id));
      if (id !== selectedRun) return;
      const signature = JSON.stringify(detail); if (signature === detailSignature) return; detailSignature = signature;
      const title = node('h3', (topics[detail.run.topic] || 'Rodada') + ' · ' + (states[detail.run.status] || detail.run.status));
      const entries = [title, node('p', 'Início: ' + stamp(detail.run.created_at) + ' · Fim: ' + stamp(detail.run.completed_at), 'autonomy-note')];
      for (const task of detail.tasks || []) {
        const section = node('section', undefined, 'autonomy-report');
        section.append(node('h4', stageLabel(task) + ' · ' + taskState(task)));
        const result = task.result;
	    if (task.stage === 0 && result) section.append(node('p', result.web_search_used ? 'Busca web registrada pelo runner.' : 'Busca web não registrada nesta execução.', 'autonomy-note'));
        if (result?.reply || result?.summary) section.append(node('p', result.reply || result.summary, 'autonomy-prose'));
        if (result?.unknowns?.length) section.append(node('p', 'Dados não confirmados: ' + result.unknowns.join(' '), 'autonomy-note'));
        section.append(node('p', 'Início: ' + stamp(task.started_at) + ' · Fim: ' + stamp(task.completed_at) + ' · Tentativas: ' + task.attempts, 'autonomy-note'));
        if (task.error_code) section.append(node('p', errors[task.error_code] || 'A etapa encontrou uma limitação. Consulte o resultado disponível.', 'autonomy-note'));
        entries.push(section);
      }
      entries.push(node('h4', 'Fontes verificadas desta rodada'));
      const sources = node('ul');
      for (const source of detail.evidence || []) {
        const li = node('li'); li.append(link(source.url, source.title), node('p', 'Publicação: ' + stamp(source.published_at) + ' · Consulta: ' + stamp(source.observed_at), 'autonomy-note')); sources.append(li);
      }
      entries.push(sources);
      $('autonomy-detail').replaceChildren(...entries);
    } catch (error) { $('autonomy-detail').replaceChildren(node('p', error.message, 'autonomy-note')); }
  }
  let refreshing = false, refreshAgain = false;
  async function refresh(includeArchive = true) {
    if (document.hidden) return;
    if (refreshing) { refreshAgain = true; return; }
    refreshing = true;
    clearTimeout(timer);
    try {
      const status = await request('/api/autonomy');
      if (!refreshAgain) {
        render(status);
        window.dispatchEvent(new CustomEvent('autonomy-status', {detail:status}));
      }
    } catch (error) {
      window.dispatchEvent(new Event('autonomy-status-unavailable'));
      $('autonomy-feedback').textContent = error.message;
      $('autonomy-state').textContent = 'Consulta indisponível';
    } finally {
      refreshing = false;
      if (refreshAgain) { refreshAgain = false; refresh(false); }
      else timer = setTimeout(refresh, 30000);
    }
    if (includeArchive) {
      try { renderStrategies(await request('/api/autonomy/strategies')); await refreshDetail(); }
      catch (error) { $('autonomy-feedback').textContent = error.message; }
    }
  }
  window.addEventListener('office-event',event => {
    if (event.detail.event_type === 'autonomy.changed') refresh(false);
  });
  async function action(path, body) {
    if (busy) return;
    busy = true; if (current) render(current);
    try {
      await request('/api/autonomy/' + path, {method:'POST', headers:{'Content-Type':'application/json', 'X-CSRF-Token':document.querySelector('meta[name="csrf-token"]').content}, body:JSON.stringify(body)});
      $('autonomy-feedback').textContent = path === 'run' ? 'Solicitação registrada. A execução respeita a fila e os limites de hoje.' : body.paused ? 'Acompanhamento pausado.' : 'Acompanhamento retomado.';
      await refresh();
    } catch (error) { $('autonomy-feedback').textContent = error.message; }
    finally { busy = false; if (current) render(current); }
  }
  window.addEventListener('office-stream-open',() => refresh(false));
  $('autonomy-pause').addEventListener('click', () => action('pause', {paused:!current?.paused}));
  $('autonomy-run').addEventListener('click', () => action('run', {}));
  document.addEventListener('visibilitychange', () => { clearTimeout(timer); if (!document.hidden) refresh(); });
  window.addEventListener('load', () => {
    const id = new URLSearchParams(location.search || '').get('run');
    if (id && /^[a-f0-9-]{36}$/i.test(id)) {
      window.dispatchEvent(new CustomEvent('workspace-open', {detail:{panel:'activity'}}));
      showRun(id);
    }
  });
  refresh();
})();
