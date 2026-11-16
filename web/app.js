(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  let production = document.body.dataset.production === 'true';
	let conversationConfigured = !production, agentNames = new Map();
  const node = (tag, text, cls) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (cls) el.className = cls; return el; };
  const stamp = value => new Intl.DateTimeFormat('pt-BR', {timeZone:'America/Sao_Paulo',day:'2-digit',month:'2-digit',hour:'2-digit',minute:'2-digit'}).format(new Date(value));
  const channelLabel = value => value === 'telegram' ? production ? 'Telegram · histórico' : 'Telegram simulado' : 'Dashboard';
  let csrf = document.querySelector('meta[name="csrf-token"]').content;
  let cursor = 0, pending = null, lastReceipt = null, refreshTimer, searchTimer, bridgeTimer, stream;
  let stateSequence = 0, messageSignature = '', factSignature = '', episodeSignature = '', activeFilter = 'all';
  let initialized = false, sending = false, historyGeneration = 0;
  let episodes = [], expandedEpisodes = new Set();

  function feedback(text, error = false) { $('feedback').textContent = text; $('feedback').classList.toggle('error', error); }
  async function request(path, options) {
    const response = await fetch(path, {credentials:'same-origin', ...options});
    const data = await response.json();
    if (!response.ok) {
      if (response.status === 401) location.assign('/login');
      throw new Error(data.error || 'Não foi possível concluir. Tente novamente.');
    }
    return data;
  }
  function connection(text, connected = true) {
    const label = node('span', text);
    $('connection').replaceChildren(...(connected ? [node('span', undefined, 'status-dot'), label] : [label]));
  }
  function transfer() {
    const bridge = document.querySelector('.context-bridge');
    if (!bridge || production) return;
    bridge.classList.remove('is-transferring');
    requestAnimationFrame(() => bridge.classList.add('is-transferring'));
    clearTimeout(bridgeTimer);
    bridgeTimer = setTimeout(() => bridge.classList.remove('is-transferring'), 1500);
  }
  function renderMessages(messages) {
    const signature = messages.map(m => m.id + ':' + m.status).join('|');
    if (signature === messageSignature && initialized) return;
    messageSignature = signature;
    const previousIDs = new Set([...$('messages').children].map(el => el.dataset.id));
    const nearBottom = $('messages').scrollHeight - $('messages').scrollTop - $('messages').clientHeight < 90;
    const previousScroll = $('messages').scrollTop;
    const fragment = document.createDocumentFragment();
    [...messages].reverse().forEach(m => {
      const item = node('li', undefined, 'message ' + m.direction);
      item.dataset.id = m.id;
      if (initialized && !previousIDs.has(String(m.id))) item.classList.add('is-new');
      const incoming = m.direction === 'incoming';
      const avatar = node('span', incoming ? 'v.' : 'a.', 'message-avatar');
      avatar.setAttribute('aria-hidden', 'true');
      const content = node('div', undefined, 'message-content'), meta = node('div', undefined, 'message-meta');
      const time = node('time', stamp(m.created_at)); time.dateTime = m.created_at;
      meta.append(node('strong', incoming ? 'Você' : m.analysis ? agentNames.get(m.agent_id) || 'Conselho' : 'Atlas'), node('span', incoming ? channelLabel(m.channel) : m.analysis ? 'Análise · opinião' : production ? 'Registro do histórico' : 'Determinístico'), time);
      content.append(meta, node('p', m.text));
      if (incoming && ['failed','failed_unknown','blocked','stale'].includes(m.status)) content.append(node('p', m.status === 'stale' ? 'Os dados mudaram durante a análise. Envie uma nova pergunta.' : 'Análise não concluída. A mensagem foi preservada. Consulte Conexões para verificar a IA.', 'integration-status'));
      if (m.analysis && m.source_event_id) {
        const sources = node('button', 'Ver fontes e limites', 'button-quiet'); sources.type = 'button';
        sources.addEventListener('click', () => loadAnalysis(m.source_event_id, content, sources)); content.append(sources);
      }
      item.append(avatar, content); fragment.append(item);
    });
    if (!messages.length) {
      const empty = node('li', undefined, 'conversation-empty'), monogram = node('span', 'a.');
      monogram.setAttribute('aria-hidden','true');
      empty.append(monogram, node('h3', production ? 'Nenhuma conversa registrada' : 'Podemos começar por você.'), node('p', production ? conversationConfigured ? 'Envie uma pergunta. A análise e suas fontes ficam neste histórico.' : 'Consulte a disponibilidade da IA em Conexões.' : 'Compartilhe uma preferência. Ela continua disponível quando você muda de canal.'));
      fragment.append(empty);
    }
    $('messages').replaceChildren(fragment);
    $('messages').scrollTop = !initialized || nearBottom || sending ? $('messages').scrollHeight : previousScroll;
  }
  function renderFacts(facts) {
    const signature = facts.map(f => f.id + ':' + f.version).join('|');
    $('fact-count').textContent = facts.length;
    if (signature === factSignature && initialized) return;
    const changed = initialized && signature !== factSignature;
    factSignature = signature;
    const fragment = document.createDocumentFragment();
    facts.forEach(f => {
      const button = node('button', undefined, 'fact-open');
      button.type = 'button'; button.setAttribute('aria-expanded', String(!$('fact-history').hidden)); button.setAttribute('aria-controls', 'fact-history');
      const heading = node('span', undefined, 'fact-heading'), value = node('span', f.value + ' ', 'fact-value'), source = node('span', undefined, 'fact-source');
      heading.append(node('span', 'Preferência de reserva'), node('span', 'v' + f.version));
      value.append(node('em', 'meses'));
      source.append(node('span', channelLabel(f.channel) + ' · ' + stamp(f.created_at)), node('span', 'Origem e versões ↗'));
      button.append(heading, value, source); fragment.append(button);
    });
    if (!facts.length) {
      const empty = node('div', undefined, 'memory-empty');
      empty.append(node('h3', production ? 'Nenhuma preferência registrada' : 'Há espaço para o que importa.'), node('p', production ? 'As preferências existentes ficam disponíveis para consulta. Novos registros por conversa dependem da configuração da IA.' : 'Uma preferência registrada aparece aqui, com origem e histórico.'));
      if (!production) {
        const prompt = node('button', 'Registrar a primeira ↗');
        prompt.type = 'button'; prompt.dataset.prompt = 'Minha reserva é de 6 meses'; empty.append(prompt);
      }
      fragment.append(empty);
    }
    $('facts').replaceChildren(fragment);
    if (changed) {
      $('facts').classList.remove('is-updated');
      requestAnimationFrame(() => $('facts').classList.add('is-updated'));
      transfer();
      if (!$('fact-history').hidden) loadHistory();
    }
  }
  function renderEpisodes(force = false) {
    const shown = episodes.filter(e => activeFilter === 'all' || e.channel === activeFilter);
    const signature = shown.map(e => e.id).join('|') + ':' + activeFilter;
    $('episode-count').textContent = episodes.length;
    $('search-count').textContent = shown.length + (shown.length === 1 ? ' episódio' : ' episódios');
    if (!force && signature === episodeSignature && initialized) return;
    episodeSignature = signature;
    const fragment = document.createDocumentFragment();
    shown.forEach(e => {
      const item = node('li'), meta = node('div', undefined, 'episode-meta'), time = node('time', stamp(e.created_at));
      time.dateTime = e.created_at;
      meta.append(node('strong', channelLabel(e.channel)), time);
      const button = node('button', undefined, 'episode-disclosure'), expanded = expandedEpisodes.has(e.id);
      button.type = 'button'; button.dataset.episode = e.id; button.setAttribute('aria-expanded', String(expanded));
      button.append(node('span', e.text), node('small', expanded ? 'Recolher · evento #' + e.source_event_id : 'Ver registro · evento #' + e.source_event_id));
      item.append(meta, button); fragment.append(item);
    });
    if (!shown.length) fragment.append(node('li', $('search').value || activeFilter !== 'all' ? 'Nenhum episódio para esta busca e canal.' : production ? 'Nenhum episódio registrado. A IA continua desativada.' : 'As conversas processadas ficam disponíveis aqui.', 'empty'));
    $('episodes').replaceChildren(fragment);
  }
  function render(data) {
    production = production || data.mode === 'production' || data.processor === 'disabled' || data.capabilities?.conversation === 'not_configured';
    conversationConfigured = !production || data.capabilities?.conversation === 'configured';
    agentNames = new Map((data.agents || []).map(agent => [agent.id, agent.name]));
    const modelLabels = {available:'IA disponível',busy:'IA ocupada · mensagens entram na fila',not_authenticated:'Codex aguardando login',rate_limited:'Limite da IA atingido',policy_violation:'IA bloqueada para revisão',auth_storage_failed:'Falha ao preservar o login',disabled:'IA não configurada',not_configured:'IA não configurada',configured:'IA disponível'};
    if ($('model-status')) $('model-status').textContent = modelLabels[data.model?.state || data.capabilities?.models] || 'IA indisponível';
    updateComposer();
    csrf = data.csrf;
    const s = data.snapshot; cursor = Math.max(cursor, s.cursor);
    $('queue-status').textContent = !conversationConfigured ? 'Somente leitura' : s.pending ? production ? 'Analisando…' : 'Registrando…' : s.failed ? s.failed + ' com falha' : production ? 'Pronto para sua pergunta' : 'Tudo registrado';
    const submitted = s.messages.find(m => m.id === lastReceipt && m.direction === 'incoming');
    if (submitted?.status === 'complete') feedback(production ? 'Análise concluída. Consulte as fontes e os limites junto à resposta.' : 'Registrado. O conselho já pode recuperar esse contexto nos dois canais.');
    if (submitted && ['failed','failed_unknown','blocked','stale'].includes(submitted.status)) feedback('A mensagem foi preservada, mas a análise não foi concluída. Consulte o estado em Conexões.', true);
    renderMessages(s.messages); renderFacts(s.facts);
    episodes = s.episodes; renderEpisodes();
    connection('Histórico sincronizado');
    window.dispatchEvent(new CustomEvent('conselho-state', {detail:s}));
    window.dispatchEvent(new CustomEvent('conselho-agents', {detail:data.agents || []}));
    window.dispatchEvent(new CustomEvent('conselho-capabilities', {detail:{capabilities:data.capabilities,model:data.model}}));
    initialized = true;
    if (s.pending && conversationConfigured) scheduleRefresh();
  }
  function scheduleRefresh() { clearTimeout(refreshTimer); if (!document.hidden) refreshTimer = setTimeout(refresh, production ? 3000 : 650); }
  async function loadAnalysis(id, content, button) {
    button.disabled = true;
    try {
      const analysis = await request('/api/analyses/' + encodeURIComponent(id));
      if (!analysis.result) throw new Error('Esta análise ainda não tem resultado disponível.');
      const details = node('details'), summary = node('summary', 'Fontes e limites');
      details.append(summary);
      const used = new Set(analysis.result.evidence || []);
      const sources = node('ul');
      for (const source of analysis.sources || []) if (used.has(source.id)) sources.append(node('li', (source.type === 'investment' ? 'Investimentos via Pluggy' : source.type === 'financial' ? 'Registros manuais' : source.type === 'agent' ? 'Análise anterior' : 'Mensagem ou preferência confirmada') + ' · ' + source.id + ' · ' + stamp(source.observed_at)));
      details.append(sources);
      if (!used.size) details.append(node('p', 'Sem referência específica citada pelo modelo.'));
      for (const assumption of analysis.result.assumptions || []) details.append(node('p', 'Hipótese: ' + assumption));
      for (const unknown of analysis.result.unknowns || []) details.append(node('p', 'Limite: ' + unknown));
      if (analysis.as_of) details.append(node('p', 'Contexto consultado em ' + stamp(analysis.as_of) + '.'));
      details.open = true; content.append(details); button.hidden = true;
    } catch(error) { button.disabled = false; feedback(error.message, true); }
  }
  async function refresh() {
    const sequence = ++stateSequence;
    try {
      const data = await request('/api/state?q=' + encodeURIComponent($('search').value));
      if (sequence === stateSequence) {
        if (data.snapshot.cursor < cursor) scheduleRefresh();
        else render(data);
      }
    } catch(error) {
      if (sequence !== stateSequence) return;
      window.dispatchEvent(new Event('conselho-state-unavailable'));
      connection('Sem conexão. O histórico continua preservado.', false); feedback(error.message, true);
    }
  }
  function selectTab(name, focus = false) {
    for (const tab of document.querySelectorAll('.context-tabs [role=tab]')) {
      const selected = tab.id === 'tab-' + name;
      tab.setAttribute('aria-selected', String(selected)); tab.tabIndex = selected ? 0 : -1;
      $(tab.getAttribute('aria-controls')).hidden = !selected;
      if (selected && focus) tab.focus();
    }
    document.querySelector('.context-tabs').dataset.active = name;
  }
  document.querySelector('.context-tabs').addEventListener('click', event => {
    const tab = event.target.closest('[role=tab]'); if (tab) selectTab(tab.id.slice(4));
  });
  document.querySelector('.context-tabs').addEventListener('keydown', event => {
    if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
    event.preventDefault();
    const active = document.querySelector('.context-tabs [role=tab][aria-selected=true]').id;
    selectTab(event.key === 'Home' ? 'facts' : event.key === 'End' ? 'episodes' : active === 'tab-facts' ? 'episodes' : 'facts', true);
  });
  function setChannel() {
    const selected = document.querySelector('input[name=channel]:checked');
    if (production || !selected) return;
    const channel = selected.value;
    const bridge = document.querySelector('.context-bridge');
    if (bridge) bridge.dataset.channel = channel;
    try { localStorage.setItem('conselho-channel', channel); } catch {}
  }
  try { const saved = localStorage.getItem('conselho-channel'); if (!production && ['telegram','dashboard'].includes(saved)) document.querySelector('input[value="' + saved + '"]').checked = true; } catch {}
  setChannel();
  document.querySelectorAll('input[name=channel]').forEach(input => input.addEventListener('change', setChannel));
  function updateComposer() {
    if (!conversationConfigured) {
      if ($('message')) $('message').disabled = true;
      if ($('send')) $('send').disabled = true;
      return;
    }
    if (!$('message') || !$('send') || !$('character-count')) return;
    $('message').disabled = false;
    $('character-count').textContent = $('message').value.length + ' / 2000';
    $('send').disabled = sending || !$('message').value.trim();
  }
  $('message')?.addEventListener('input', updateComposer);
  $('message')?.addEventListener('keydown', event => { if (conversationConfigured && (event.ctrlKey || event.metaKey) && event.key === 'Enter' && !$('send')?.disabled) { event.preventDefault(); $('message-form')?.requestSubmit(); } });
  document.addEventListener('click', event => {
    const prompt = event.target.closest('[data-prompt]');
    if (prompt && conversationConfigured && $('message')) { $('message').value = prompt.dataset.prompt; updateComposer(); $('message').focus(); }
    const fact = event.target.closest('.fact-open');
    if (fact) { if ($('fact-history').hidden) { $('fact-history').hidden = false; fact.setAttribute('aria-expanded','true'); $('fact-history').focus({preventScroll:true}); loadHistory(); } else closeHistory(); }
    const episode = event.target.closest('[data-episode]');
    if (episode) {
      const id = Number(episode.dataset.episode), expanded = episode.getAttribute('aria-expanded') !== 'true';
      episode.setAttribute('aria-expanded', String(expanded));
      if (expanded) expandedEpisodes.add(id); else expandedEpisodes.delete(id);
      const source = episodes.find(e => e.id === id);
      episode.querySelector('small').textContent = (expanded ? 'Recolher' : 'Ver registro') + ' · evento #' + source.source_event_id;
    }
  });
  $('message-form')?.addEventListener('submit', async event => {
    event.preventDefault(); if (!conversationConfigured || sending) return;
    const text = $('message').value.trim(), channel = production ? 'dashboard' : new FormData(event.currentTarget).get('channel');
    if (!text) return;
    if (!pending || pending.text !== text || pending.channel !== channel) pending = {text, channel, key:crypto.randomUUID()};
    const submission = pending; sending = true; updateComposer(); feedback('Registrando sua mensagem…');
    try {
      const receipt = await request(channel === 'telegram' ? '/demo/telegram' : '/api/conversations/turn', {method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({text:submission.text,idempotency_key:submission.key})});
      lastReceipt = receipt.id; transfer(); feedback(production ? 'Mensagem salva. A análise entrou na fila.' : 'Mensagem salva. O conselho está registrando o contexto.');
      if ($('message').value.trim() === submission.text) $('message').value = '';
      pending = null; await refresh(); scheduleRefresh();
    } catch(error) { feedback(error.message + ' Você pode reenviar sem duplicar o registro.', true); }
    finally { sending = false; updateComposer(); }
  });
  $('search-form').addEventListener('submit', event => { event.preventDefault(); clearTimeout(searchTimer); refresh(); });
  $('search').addEventListener('input', () => { clearTimeout(searchTimer); searchTimer = setTimeout(refresh, 300); });
  document.querySelector('.episode-filters').addEventListener('click', event => {
    const button = event.target.closest('[data-filter]'); if (!button) return;
    activeFilter = button.dataset.filter;
    document.querySelectorAll('[data-filter]').forEach(item => item.setAttribute('aria-pressed', String(item === button)));
    renderEpisodes(true);
  });
  async function loadHistory() {
    const generation = ++historyGeneration;
    $('history-status').textContent = 'Recuperando as versões…';
    try {
      const data = await request('/api/memory/history');
      if (generation !== historyGeneration || $('fact-history').hidden) return;
      $('history-status').textContent = data.versions.length + (data.versions.length === 1 ? ' versão preservada.' : ' versões preservadas.');
      const fragment = document.createDocumentFragment();
      data.versions.forEach(v => {
        const item = node('li'), meta = node('div', undefined, 'version-meta');
        meta.append(node('strong', 'v' + v.version + ' · ' + v.value + ' meses'), node('span', v.status === 'active' ? 'Em vigor' : 'Substituída'));
        item.append(meta, node('blockquote', '“' + v.source_text + '”'), node('p', channelLabel(v.channel) + ' · ' + stamp(v.created_at) + ' · evento #' + v.source_event_id));
        if (v.valid_to) item.append(node('p', 'Substituída em ' + stamp(v.valid_to)));
        fragment.append(item);
      });
      $('version-list').replaceChildren(fragment);
    } catch(error) { if (generation === historyGeneration) $('history-status').textContent = error.message; }

  }
  function closeHistory() {
    $('fact-history').hidden = true; historyGeneration++;
    document.querySelectorAll('.fact-open').forEach(button => button.setAttribute('aria-expanded','false'));
    document.querySelector('.fact-open')?.focus();
  }
  $('close-history').addEventListener('click', closeHistory);
  $('fact-history').addEventListener('keydown', event => { if (event.key === 'Escape') { event.preventDefault(); closeHistory(); } });
  document.addEventListener('keydown', event => {
    if (event.key === '/' && !event.ctrlKey && !event.metaKey && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName) && !document.activeElement.isContentEditable) { event.preventDefault(); window.dispatchEvent(new CustomEvent('workspace-open',{detail:{panel:'memory'}})); selectTab('episodes'); $('search').focus(); }
  });
  $('logout').addEventListener('click', async () => {
    try { await request('/logout', {method:'POST',headers:{'X-CSRF-Token':csrf}}); location.assign('/login'); }
    catch(error) { feedback(error.message, true); }
  });
  function connectStream() {
    if (stream || document.hidden) return;
    stream = new EventSource('/api/office/events?after=' + cursor);
    stream.onmessage = event => {
      try {
        const data = JSON.parse(event.data); cursor = Math.max(cursor, data.sequence);
        window.dispatchEvent(new CustomEvent('office-event',{detail:data}));
        if (['agent.started','agent.completed','agent.failed'].includes(data.event_type) || data.event_type?.startsWith('analysis.')) refresh();
        else if (['conversation.received','memory.episode_saved'].includes(data.event_type)) scheduleRefresh();
      } catch { $('event-state').textContent = 'Um evento não pôde ser lido. Atualize para recuperar o estado.'; }
    };
    let opened = false;
    stream.onopen = () => {
      connection('Histórico sincronizado');
      if (opened) refresh();
      opened = true;
      window.dispatchEvent(new Event('office-stream-open'));
    };
    stream.onerror = () => { connection('Reconectando ao histórico…', false); scheduleRefresh(); };
  }
  document.addEventListener('visibilitychange', () => {
    document.body.dataset.pageHidden = String(document.hidden);
    if (document.hidden) { stream?.close(); stream = null; clearTimeout(refreshTimer); }
    else refresh().then(connectStream);
  });
  window.addEventListener('pagehide', () => { stream?.close(); stream = null; clearTimeout(refreshTimer); clearTimeout(searchTimer); clearTimeout(bridgeTimer); });
  window.addEventListener('pageshow', event => { if (event.persisted) refresh().then(connectStream); });
  updateComposer();
  refresh().then(connectStream);
})();
