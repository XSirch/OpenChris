(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const root = $('integrations-root');
  if (!root) return;
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
  const sdkURL = 'https://cdn.pluggy.ai/pluggy-connect/v2.8.2/pluggy-connect.js';
  const pending = new Set();
  let current = {}, refreshing = false, loadingData = false, widgetBusy = false, sdkPromise = null;
  const list = value => Array.isArray(value) ? value : [];
  const object = value => value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  const scalar = value => typeof value === 'string' || typeof value === 'number' ? String(value) : '';
  const payload = row => object(object(row).payload || row);
  // Normalized database fields carry exact decimals; provider JSON may contain numbers.
  const record = row => ({...payload(row),...object(row)});
  function node(tag, text, className) {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (className) element.className = className;
    return element;
  }
  function feedback(text, state = 'ready') {
    $('integrations-status').textContent = text;
    $('integrations-status').dataset.state = state;
  }
  function stamp(value) {
    if (!value) return 'Ainda não registrado';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? 'Data não informada' : new Intl.DateTimeFormat('pt-BR', {timeZone:'America/Sao_Paulo',dateStyle:'short',timeStyle:'short'}).format(date);
  }
  function statusLabel(value) {
    const labels = {healthy:'Disponível',connected:'Conectado',connected_with_attention:'Conectado, com pendências',ready:'Pronto para verificar',configured:'Configuração disponível',waiting_setup:'Aguardando registro do webhook',ok:'Disponível',active:'Ativo',success:'Atualizado',updated:'Atualizado',posted:'Confirmada',waiting_user_input:'Aguardando sua autorização',user_action_required:'Autorização necessária',login_error:'Autorização necessária',outdated:'Atualização necessária',stale:'Dados desatualizados',expired:'Autorização vencida',revoked:'Autorização revogada',partial:'Dados parciais',updating:'Atualizando',pending:'Pendente',queued:'Na fila',running:'Atualizando',syncing:'Sincronizando',error:'Falha',failed:'Falha',degraded:'Requer atenção',unavailable:'Indisponível',disabled:'Desativado',not_configured:'Não configurado'};
    return labels[scalar(value).toLowerCase()] || 'Estado não informado';
  }
  function health(target, pairs) {
    target.replaceChildren();
    for (const [label, value] of pairs) {
      const row = node('div'); row.append(node('dt', label), node('dd', value)); target.append(row);
    }
  }
  function controls(id, visible) { $(id).hidden = !visible; }
  function count(value) { return Number.isSafeInteger(value) && value >= 0 ? String(value) : 'Não informado'; }
  function render() {
    const telegram = object(current.telegram), pluggy = object(current.pluggy), models = object(current.models);
    const modelLabels = {available:'Codex disponível',busy:'Codex ocupado',not_authenticated:'Codex aguardando login',rate_limited:'Limite da IA atingido',policy_violation:'IA bloqueada para revisão',auth_storage_failed:'Falha ao preservar o login',unavailable:'Codex indisponível',disabled:'Não configurado',not_configured:'Não configurado'};
    if ($('models-status')) $('models-status').textContent = modelLabels[models.status] || 'Estado não informado';
    const telegramReady = telegram.enabled === true && telegram.configured === true;
    const pluggyReady = pluggy.enabled === true && pluggy.configured === true;
    const items = list(pluggy.items);
    const currentItems = items.filter(item => !item.superseded_by);
    const connectedItems = currentItems.filter(item => ['connected','updated','UPDATED','healthy','ok'].includes(item.status));
    const attentionItems = currentItems.filter(item => ['stale','revoked','expired','error','failed','user_action_required','login_error'].includes(item.status));
    const usableCount = Number.isSafeInteger(pluggy.usable_connections) ? pluggy.usable_connections : connectedItems.length;
    const attentionCount = Number.isSafeInteger(pluggy.attention_connections) ? pluggy.attention_connections : attentionItems.length;
    $('telegram-status').textContent = telegram.enabled !== true ? 'Desativado' : telegram.configured !== true ? 'Credenciais incompletas' : statusLabel(telegram.status);
    $('telegram-detail').textContent = !telegram.enabled ? 'Ative o Telegram nas configurações do servidor para usar este canal.' : !telegram.configured ? 'Complete o token do bot e a identidade autorizada no EasyPanel. Depois, reinicie a aplicação.' : telegram.allowed_identity_configured !== true ? 'Configure a identidade autorizada no EasyPanel antes de usar este canal.' : telegram.unknown > 0 ? 'Há um envio sem confirmação. Confira a conversa no Telegram antes de solicitar outro envio.' : models.configured ? 'O canal usa apenas a identidade autorizada. O resumo diário chega às 8h, no horário de São Paulo. As análises ficam no painel; novos resultados também recebem um aviso com link.' : 'O canal usa apenas a identidade autorizada. A IA continua desativada; nenhuma resposta de modelo será gerada.';
    controls('telegram-setup', telegramReady);
    health($('telegram-health'), telegramReady ? [['Identidade autorizada', telegram.allowed_identity_configured === true ? 'Configurada' : 'Não configurada'], ['Última mensagem recebida', stamp(telegram.last_received_at)], ['Última mensagem enviada', stamp(telegram.last_sent_at)], ['Aguardando processamento', count(telegram.pending)], ['Aguardando envio', count(telegram.queued)], ['Falhas registradas', count(telegram.failed)], ['Envios sem confirmação', count(telegram.unknown)]] : []);
    const failed = ['failed','error','degraded','unavailable'].includes(scalar(pluggy.status).toLowerCase());
    $('pluggy-status').textContent = !pluggy.enabled ? 'Desativado' : !pluggy.configured ? 'Credenciais incompletas' : usableCount ? `${usableCount} ${usableCount === 1 ? 'conexão atualizada' : 'conexões atualizadas'}${attentionCount ? '; ' + attentionCount + (attentionCount === 1 ? ' requer atenção' : ' requerem atenção') : ''}` : failed ? 'Falha na conexão' : currentItems.length ? statusLabel(pluggy.status) : 'Aguardando conexão';
    $('pluggy-detail').textContent = !pluggy.enabled ? 'Ative a Pluggy nas configurações do servidor para conectar uma instituição.' : !pluggy.configured ? 'Complete as credenciais e o conector autorizado no EasyPanel. Depois, reinicie a aplicação.' : 'Autorize a instituição na janela da Pluggy. O CONSELHO consulta contas, transações e investimentos, sem movimentar recursos.';
    for (const id of ['pluggy-setup','pluggy-connect','pluggy-register-form']) controls(id, pluggyReady);
    controls('pluggy-data-open', pluggyReady && items.length > 0);
    $('pluggy-connect').disabled = widgetBusy;
    health($('pluggy-health'), pluggyReady ? [['Conexões atuais', String(currentItems.length)], ['Conexões anteriores', String(items.length - currentItems.length)], ['Última importação pelo CONSELHO', stamp(pluggy.last_synced_at)], ['Falhas atuais', count(pluggy.jobs?.failed)], ['Falhas no histórico', count(pluggy.jobs?.historical_failed)], ['Serviço', statusLabel(pluggy.status)]] : []);
    if ($('bank-source-label')) $('bank-source-label').textContent = pluggyReady ? items.length ? 'Consultar dados bancários' : 'Conectar instituição' : 'Status das instituições';
    const target = $('pluggy-items'); target.replaceChildren();
    const previous = node('details'), previousList = node('ul', undefined, 'integration-items');
    previous.append(node('summary', 'Conexões anteriores'), previousList);
    for (const row of items) {
      const item = object(row), data = payload(item), id = scalar(item.item_id || item.id || data.id);
      const sourceSync = item.last_source_sync || data.last_source_sync;
      const entry = node('li'), header = node('div', undefined, 'integration-item-heading');
      header.append(node('strong', scalar(item.connector_name || object(data.connector).name || data.name) || 'Instituição autorizada'), node('span', statusLabel(item.status || data.status)));
      entry.append(header, node('p', 'Referência: ' + (id || 'Não informada'), 'integration-reference'), node('p', 'Atualização da instituição: ' + (sourceSync ? stamp(sourceSync) : 'não informada pela fonte'), 'integration-note'), node('p', 'Importado pelo CONSELHO: ' + stamp(item.last_synced_at || item.last_sync_at), 'integration-note'));
      if (item.superseded_by) entry.append(node('p', 'Esta autorização foi substituída por uma conexão mais recente.', 'integration-note'));
      else if (pluggyReady && scalar(item.status || data.status).toLowerCase() === 'stale') entry.append(node('p', 'Atualize a conexão no MeuPluggy se a data da instituição estiver antiga. Depois, use “Sincronizar dados” para importar os dados disponíveis.', 'integration-note'));
      if (pluggyReady && id && !item.superseded_by) {
        const button = node('button', 'Sincronizar dados', 'button-quiet'); button.type = 'button'; button.dataset.syncItem = id;
        button.disabled = pending.has(id); entry.append(button);
      }
      if (item.superseded_by) previousList.append(entry); else target.append(entry);
    }
    if (previousList.children.length) { const entry = node('li'); entry.append(previous); target.append(entry); }
    if (!pluggyReady) { $('pluggy-data').hidden = true; $('pluggy-data-open').setAttribute('aria-expanded','false'); $('pluggy-data-content').replaceChildren(); }
  }
  function errorMessage(error) {
    if (error?.name === 'AbortError') return 'A consulta demorou mais que o esperado. Tente atualizar novamente.';
    if (error?.status === 401) return 'Sua sessão expirou. Entre novamente para consultar as conexões.';
    if (error?.status === 403) return 'A sessão não permite esta ação. Entre novamente e tente outra vez.';
    if (error?.status === 429) return 'O serviço atingiu o limite de consultas. Aguarde antes de tentar novamente.';
    if ([502,503,504].includes(error?.status)) return 'O serviço está indisponível. Confira a configuração no EasyPanel e tente novamente.';
    if (error?.status === 409) return 'Há uma sincronização em andamento ou a conexão precisa de atenção. Atualize o status antes de tentar novamente.';
    if (error?.status === 400 || error?.status === 422) return 'Não foi possível validar a conexão. Confira a referência e a configuração do conector.';
    if (error?.code === 'connector') return 'O conector autorizado não foi informado. Confira a configuração no EasyPanel.';
    if (error?.code === 'sdk') return 'A janela da Pluggy não carregou. Confira a conexão e tente conectar novamente.';
    return 'Não foi possível consultar o serviço. Confira a conexão e tente novamente.';
  }
  async function request(path, body) {
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 15000);
    try {
      const options = {credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:{Accept:'application/json'}};
      if (body !== undefined) { options.method = 'POST'; options.headers['Content-Type'] = 'application/json'; options.headers['X-CSRF-Token'] = csrf; options.body = JSON.stringify(body); }
      const response = await fetch('/api/integrations' + path, options);
      if (!response.ok) {
        if (response.status === 401) location.assign('/login');
        const error = new Error('integration request failed'); error.status = response.status; throw error;
      }
      return response.status === 204 ? {} : await response.json();
    } finally { clearTimeout(timeout); }
  }
  async function refresh(message) {
    if (refreshing) return;
    refreshing = true; $('integrations-refresh').disabled = true; root.setAttribute('aria-busy','true');
    feedback('Consultando as conexões…', 'loading');
    try { current = object(await request('')); render(); feedback(message || 'Status conferido em ' + stamp(new Date().toISOString()) + '.'); }
    catch (error) { feedback(errorMessage(error), 'error'); }
    finally { refreshing = false; $('integrations-refresh').disabled = false; root.removeAttribute('aria-busy'); }
  }
  async function setup(provider) {
    const button = $(provider + '-setup');
    if (button.disabled || button.hidden) return;
    button.disabled = true;
    feedback('Registrando o recebimento de eventos…', 'loading');
    try { await request('/' + provider + '/setup', {}); await refresh('Recebimento de eventos registrado. Confira o estado do serviço abaixo.'); }
    catch (error) { feedback(errorMessage(error), 'error'); }
    finally { button.disabled = false; }
  }
  function loadSDK() {
    if (typeof window.PluggyConnect === 'function') return Promise.resolve();
    if (sdkPromise) return sdkPromise;
    sdkPromise = new Promise((resolve, reject) => {
      const script = document.createElement('script'); script.src = sdkURL; script.async = true; script.referrerPolicy = 'no-referrer';
      const fail = () => { clearTimeout(timeout); script.remove(); const error = new Error('widget unavailable'); error.code = 'sdk'; reject(error); };
      const timeout = setTimeout(fail, 15000);
      script.onload = () => { clearTimeout(timeout); typeof window.PluggyConnect === 'function' ? resolve() : fail(); };
      script.onerror = fail; document.head.append(script);
    }).catch(error => { sdkPromise = null; throw error; });
    return sdkPromise;
  }
  const itemID = value => /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(scalar(value)) ? scalar(value) : '';
  async function connect() {
    const button = $('pluggy-connect');
    if (widgetBusy || button.hidden || button.disabled) return;
    widgetBusy = true; button.disabled = true;
    feedback('Preparando a janela segura da Pluggy…', 'loading');
    let registering = false, outcome = false, closed = false;
    const release = () => { widgetBusy = false; button.disabled = false; };
    try {
      await loadSDK();
      const token = await request('/pluggy/connect-token', {});
      if (!Number.isSafeInteger(token.connectorId) || token.connectorId <= 0 || typeof token.accessToken !== 'string' || !token.accessToken) {
        const error = new Error('invalid connector configuration'); error.code = 'connector'; throw error;
      }
      const widget = new window.PluggyConnect({connectToken:token.accessToken,selectedConnectorId:token.connectorId,connectorIds:[token.connectorId],includeSandbox:false,
        products:['ACCOUNTS','CREDIT_CARDS','TRANSACTIONS','INVESTMENTS'],
        onSuccess:async result => {
          if (registering || outcome) return;
          const id = itemID(object(result).item?.id);
          if (!id) { outcome = true; feedback('A Pluggy não informou uma referência válida. Atualize o status antes de conectar novamente.', 'error'); release(); return; }
          registering = true; outcome = true;
          try { await request('/pluggy/items', {item_id:id}); await refresh('Conexão cadastrada. A sincronização foi solicitada; consulte os dados após a atualização.'); }
          catch (error) { feedback(errorMessage(error) + ' Se a autorização já terminou, use a referência da conexão para recuperá-la.', 'error'); }
          finally { registering = false; if (closed) release(); }
        },
        onError:() => { outcome = true; feedback('A instituição não concluiu a conexão. Confira a autorização na Pluggy e atualize o status.', 'error'); },
        onClose:() => { closed = true; if (!outcome) feedback('Janela fechada. Você pode continuar a conexão quando quiser.'); if (!registering) release(); }
      });
      widget.init(); feedback('Conclua a autorização na janela da Pluggy.');
    } catch (error) { feedback(errorMessage(error), 'error'); release(); }
  }
  async function register(event) {
    event.preventDefault(); const button = $('pluggy-register');
    if (button.disabled || $('pluggy-register-form').hidden) return;
    const id = itemID($('pluggy-item-id').value.trim());
    if (!id) { feedback('Informe a referência válida de uma conexão que você já autorizou na Pluggy.', 'error'); $('pluggy-item-id').focus(); return; }
    button.disabled = true;
    try { await request('/pluggy/items', {item_id:id}); $('pluggy-item-id').value = ''; await refresh('Referência cadastrada. A sincronização foi solicitada.'); }
    catch (error) { feedback(errorMessage(error), 'error'); }
    finally { button.disabled = false; }
  }
  async function sync(button) {
    const id = itemID(button.dataset.syncItem);
    if (!id || pending.has(id) || button.disabled) return;
    pending.add(id); button.disabled = true; feedback('Solicitando a sincronização da fonte…', 'loading');
    try { await request('/pluggy/sync', {item_id:id}); await refresh('Sincronização solicitada. A atualização depende da disponibilidade da instituição.'); }
    catch (error) { feedback(errorMessage(error), 'error'); }
    finally { pending.delete(id); render(); }
  }
  function recordDetails(entry, pairs) {
    const details = node('dl', undefined, 'integration-record-values');
    health(details, pairs.map(([label,value]) => [label,scalar(value) || 'Não informado'])); entry.append(details);
  }
  function renderData(data) {
    const target = $('pluggy-data-content'); target.replaceChildren();
    target.append(node('p','Fonte: Pluggy · dados consultados na instituição autorizada. Valores preservados na moeda e no formato da fonte. Não entram nos totais manuais. Investimentos com consentimento válido também aparecem na visão geral e ficam disponíveis para as análises.','integration-note'));
    const groups = [
      ['accounts','Contas','Nenhuma conta foi disponibilizada nesta consulta.', row => scalar(row.name || row.type) || 'Conta', row => [['Saldo', row.balance],['Moeda',row.currency || row.currencyCode],['Atualização na fonte',stamp(row.source_updated_at || row.updatedAt || row.updated_at)]]],
      ['transactions','Transações','Nenhuma transação foi disponibilizada nesta consulta.', row => scalar(row.description || row.descriptionRaw) || 'Transação', row => [['Valor',row.amount],['Moeda',row.currency || row.currencyCode],['Data',row.date || row.paymentDate],['Estado',statusLabel(row.status)]]],
      ['investments','Investimentos','Nenhum investimento foi disponibilizado nesta consulta.', row => scalar(row.name || row.type) || 'Investimento', row => [['Valor',row.balance ?? row.amount ?? row.value],['Moeda',row.currency || row.currencyCode],['Data',row.date || row.dueDate]]]
    ];
    for (const [key,title,empty,label,fields] of groups) {
      const section = node('section',undefined,'integration-data-group'), records = list(data[key]);
      section.append(node('h4',title + ' · ' + records.length));
      if (!records.length) section.append(node('p',empty,'integration-note'));
      else {
        const entries = node('ul',undefined,'integration-records');
        for (const row of records.slice(0,100)) {
          const details = record(row), entry = node('li'), pairs = fields(details);
          entry.append(node('strong',label(details)));
          if (details.observed_at) pairs.push(['Coletado em',stamp(details.observed_at)]);
          if (details.active === false) pairs.push(['Observação','Registro anterior, fora da coleta atual']);
          recordDetails(entry,pairs); entries.append(entry);
        }
        section.append(entries);
        if (records.length > 100) section.append(node('p','Exibindo os primeiros 100 registros desta consulta.','integration-note'));
      }
      target.append(section);
    }
  }
  async function openData() {
    if (loadingData || $('pluggy-data-open').hidden) return;
    loadingData = true; $('pluggy-data-open').disabled = true; $('pluggy-data-open').setAttribute('aria-expanded','true'); $('pluggy-data').hidden = false;
    $('pluggy-data-status').textContent = 'Consultando os dados sincronizados…'; $('pluggy-data-content').replaceChildren();
    $('pluggy-data').setAttribute('aria-busy','true');
    try { renderData(object(await request('/pluggy/data'))); $('pluggy-data-status').textContent = 'Consulta conferida em ' + stamp(new Date().toISOString()) + '.'; }
    catch (error) { $('pluggy-data-status').textContent = errorMessage(error); }
    finally { loadingData = false; $('pluggy-data-open').disabled = false; $('pluggy-data').removeAttribute('aria-busy'); }
  }
  $('integrations-refresh').addEventListener('click', () => refresh());
  $('telegram-setup').addEventListener('click', () => setup('telegram'));
  $('pluggy-setup').addEventListener('click', () => setup('pluggy'));
  $('pluggy-connect').addEventListener('click', connect);
  $('pluggy-register-form').addEventListener('submit', register);
  $('pluggy-data-open').addEventListener('click', openData);
  $('pluggy-items').addEventListener('click', event => { const button = event.target.closest('[data-sync-item]'); if (button) sync(button); });
  $('integrations-refresh').hidden = false;
  window.addEventListener('workspace-panel-change', event => { if (event.detail?.panel === 'connections') refresh(); });
  if (document.body.dataset.drawerOpen === 'connections') refresh();
})();
