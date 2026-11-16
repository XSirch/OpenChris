(() => {
  'use strict';
  const amountFields = ['reserve_current_cents', 'reserve_target_cents', 'commitments_30_days_cents', 'minimum_checking_cents', 'protected_checking_cents'];
  function parseCents(value) {
    let text = String(value ?? '').trim();
    if (!text) return null;
    if (/^\d{1,3}(?:\.\d{3})+(?:,\d{1,2})?$/.test(text)) text = text.replaceAll('.', '');
    if (!/^\d+(?:[.,]\d{1,2})?$/.test(text)) throw Error('Informe um valor em reais com até duas casas decimais, como 1234,56.');
    const [whole, decimal = ''] = text.replace(',', '.').split('.');
    const cents = Number(whole) * 100 + Number(decimal.padEnd(2, '0'));
    if (!Number.isSafeInteger(cents) || cents > 1000000000000) throw Error('O valor informado excede o limite do cadastro. Confira o valor em reais.');
    return cents;
  }
  function profileInput(values) {
    const result = {version:values.version, confirmed:values.confirmed === true, goals:(values.goals || []).map(goal => ({name:goal.name.trim(), target_cents:parseCents(goal.target), target_date:goal.target_date})), risk_profile:values.risk_profile || '', experience:values.experience || '', restrictions:values.restrictions || ''};
    if (result.goals.length > 20) throw Error('Você pode cadastrar até 20 objetivos. Remova um objetivo antes de adicionar outro.');
    for (const goal of result.goals) {
      if (!goal.name || [...goal.name].length > 100 || !goal.target_cents || !/^\d{4}-\d{2}-\d{2}$/.test(goal.target_date || '') || goal.target_date < '1900-01-01' || goal.target_date > '2100-12-31') throw Error('Complete o nome, o valor e a data de cada objetivo, ou remova o objetivo que ainda não deseja cadastrar.');
    }
    for (const name of amountFields) result[name] = parseCents(values[name]);
    result.liquidity_days = values.liquidity_days === '' || values.liquidity_days == null ? null : Number(values.liquidity_days);
    if (result.liquidity_days !== null && (!Number.isInteger(result.liquidity_days) || result.liquidity_days < 0 || result.liquidity_days > 3650)) throw Error('Informe um prazo de liquidez entre 0 e 3650 dias.');
    return result;
  }
  function safeActionURL(value) {
    if (typeof value !== 'string' || !/^\/(?:finance\/profile(?:#[a-z_]+)?|expenses(?:\?[^#]*)?|dashboard#connections-panel)$/.test(value)) return '/finance/profile';
    return value;
  }
  if (typeof module !== 'undefined' && module.exports) { module.exports = {parseCents, profileInput, safeActionURL}; return; }
  const panels = [...document.querySelectorAll('[data-personal-finance]')];
  const form = document.getElementById('personal-profile-form');
  const classificationForms = [...document.querySelectorAll('[data-classification-form]')];
  if (!panels.length && !form && !classificationForms.length) return;
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content || '';
  const node = (tag, text) => { const element = document.createElement(tag); if (text !== undefined) element.textContent = text; return element; };
  const money = (value, currency = 'BRL') => Number.isSafeInteger(value) && /^[A-Z]{3}$/.test(currency) ? new Intl.NumberFormat('pt-BR', {style:'currency', currency}).format(value / 100) : 'Não informado';
  const date = value => value && Number.isFinite(new Date(value).getTime()) ? new Intl.DateTimeFormat('pt-BR', {timeZone:'America/Sao_Paulo',dateStyle:'short',timeStyle:'short'}).format(new Date(value)) : 'Ainda não registrado';
  const inputMoney = value => value == null ? '' : (value / 100).toFixed(2).replace('.', ',');
  let version = 0, loading = false, lastSummaryRevision = '';
  async function request(url, body) {
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 15000);
    try {
      const options = {credentials:'same-origin',cache:'no-store',signal:controller.signal,headers:{Accept:'application/json'}};
      if (body !== undefined) { options.method = 'POST'; options.headers['Content-Type'] = 'application/json'; options.headers['X-CSRF-Token'] = csrf; options.body = JSON.stringify(body); }
      const response = await fetch(url, options);
      if (response.status === 401) { location.assign('/login?return_to=' + encodeURIComponent(location.pathname + location.search + location.hash)); throw Error('Sua sessão expirou. Entre novamente para continuar.'); }
      if (response.status === 409) throw Error('Estes dados mudaram desde a abertura da página. Recarregue os dados antes de salvar.');
      const data = await response.json();
      if (!response.ok) throw Error(response.status === 400 || response.status === 422 ? data.error || 'Confira os campos informados antes de salvar.' : 'Não foi possível concluir a solicitação. Tente novamente.');
      return data;
    } catch (error) { if (error.name === 'AbortError') throw Error('A solicitação demorou mais que o esperado. Seus campos foram mantidos; tente novamente.'); throw error; }
    finally { clearTimeout(timeout); }
  }
  function render(data) {
    for (const panel of panels) {
      const issues = panel.querySelector('[data-personal-issues]');
      const pending = data.pending_items || [];
      panel.querySelector('[data-personal-status]').textContent = pending.length ? `${pending.length} ${pending.length === 1 ? 'pendência precisa' : 'pendências precisam'} da sua atenção.` : data.can_recommend ? 'Seus dados permitem avaliar sugestões personalizadas.' : 'Dados consultados. Confira a disponibilidade e a análise mais recente.';
      issues.replaceChildren(...pending.map(item => {
        const li = node('li'), link = node('a', item.title); link.href = safeActionURL(item.url);
        li.append(link, node('p', item.detail)); return li;
      }));
      const cash = panel.querySelector('[data-personal-cash]');
      if (cash) {
        cash.replaceChildren();
        for (const item of data.cash || []) { const row = node('div'); row.append(node('dt', `Saldo em conta (${item.currency})`), node('dd', money(item.amount_cents, item.currency))); cash.append(row); }
        const available = node('div'); available.append(node('dt', 'Disponível para investir (BRL)'), node('dd', data.available_to_invest_cents == null ? 'Complete as pendências' : money(data.available_to_invest_cents))); cash.append(available);
      }
      const stamp = panel.querySelector('[data-personal-stamp]');
      if (stamp) stamp.textContent = 'Última sincronização: ' + date(data.last_synced_at) + '. O saldo pode incluir valores reservados.';
    }
  }
  function addGoal(goal = {}) {
    const container = document.getElementById('profile-goals'), row = document.createElement('fieldset');
    row.className = 'profile-goal'; row.dataset.goal = '';
    row.append(node('legend', 'Objetivo'));
    for (const [key, label, type, value] of [['name','Nome do objetivo','text',goal.name || ''], ['target','Valor desejado, em reais','text',inputMoney(goal.target_cents)], ['target_date','Data desejada','date',goal.target_date || '']]) {
      const labelNode = node('label', label), input = node('input'); input.type = type; input.dataset.goalField = key; input.value = value; input.maxLength = key === 'name' ? 100 : 30;
      if (key === 'target') input.inputMode = 'decimal';
      if (key === 'target_date') { input.min = '1900-01-01'; input.max = '2100-12-31'; }
      labelNode.append(input); row.append(labelNode);
    }
    const remove = node('button', 'Remover objetivo'); remove.type = 'button'; remove.className = 'button-quiet';
    remove.addEventListener('click', () => { row.remove(); form.elements.confirmed.checked = false; document.getElementById('profile-add-goal').focus(); });
    row.append(remove); container.append(row); return row;
  }
  function populate(profile) {
    version = profile.version || 0;
    for (const name of amountFields) form.elements[name].value = inputMoney(profile[name]);
    for (const name of ['risk_profile','experience','restrictions','liquidity_days']) form.elements[name].value = profile[name] ?? '';
    form.elements.confirmed.checked = profile.confirmed === true;
    document.getElementById('profile-goals').replaceChildren();
    for (const goal of profile.goals || []) addGoal(goal);
    if (!profile.goals?.length) addGoal();
    const confirmation = document.getElementById('profile-last-confirmed');
    confirmation.textContent = profile.confirmed_at ? 'Última confirmação: ' + date(profile.confirmed_at) : 'Seus dados ainda não foram confirmados.';
    form.querySelector('fieldset[data-profile-fields]').disabled = false;
  }
  async function load(fillForm = false) {
    if (loading) return false;
    loading = true;
    try { const data = await request('/api/finance/personal'); render(data); if (typeof data.revision === 'string') lastSummaryRevision = data.revision; if (form && fillForm) populate(data.profile || {}); return true; }
    catch (error) { for (const panel of panels) panel.querySelector('[data-personal-status]').textContent = error.message; if (form) document.getElementById('profile-feedback').textContent = error.message; return false; }
    finally { loading = false; }
  }
  if (form) {
    document.getElementById('profile-add-goal').addEventListener('click', () => {
      if (form.querySelectorAll('[data-goal]').length >= 20) { document.getElementById('profile-feedback').textContent = 'Você pode cadastrar até 20 objetivos. Remova um objetivo antes de adicionar outro.'; return; }
      const row = addGoal(); form.elements.confirmed.checked = false; row.querySelector('input').focus();
    });
    document.getElementById('profile-reload').addEventListener('click', () => load(true));
    form.addEventListener('input', event => { if (event.target.name !== 'confirmed') form.elements.confirmed.checked = false; });
    form.addEventListener('submit', async event => {
      event.preventDefault(); const button = document.getElementById('profile-save'), feedback = document.getElementById('profile-feedback');
      if (button.disabled) return;
      try {
        const values = {version, confirmed:form.elements.confirmed.checked, goals:[...form.querySelectorAll('[data-goal]')].map(row => Object.fromEntries([...row.querySelectorAll('[data-goal-field]')].map(input => [input.dataset.goalField, input.value])))};
        values.goals = values.goals.filter(goal => goal.name.trim() || goal.target.trim() || goal.target_date);
        for (const name of [...amountFields,'risk_profile','experience','liquidity_days','restrictions']) values[name] = form.elements[name].value;
        const body = profileInput(values); button.disabled = true; button.textContent = 'Salvando…'; feedback.textContent = '';
        await request('/api/finance/profile', body);
        if (await load(true)) feedback.textContent = body.confirmed ? 'Dados confirmados e salvos. As próximas análises usarão este perfil.' : 'Dados salvos. Você pode completar as pendências e confirmar quando terminar.';
        else feedback.textContent = 'Dados salvos, mas não foi possível atualizar a tela. Use “Recarregar meus dados” antes de editar novamente.';
      } catch (error) { feedback.textContent = error.message; }
      finally { button.disabled = false; button.textContent = 'Salvar meus dados'; }
    });
  }
  for (const entry of classificationForms) entry.addEventListener('submit', async event => {
    event.preventDefault(); const button = entry.querySelector('button[type=submit]'), feedback = entry.querySelector('[data-classification-feedback]');
    if (button.disabled) return;
    button.disabled = true; feedback.textContent = 'Salvando classificação…';
    try {
      await request('/api/finance/classifications', {transaction_id:entry.dataset.transactionId,version:Number(entry.dataset.version),category:entry.elements.category.value.trim(),kind:entry.elements.kind.value,apply_rule:entry.elements.apply_rule.checked});
      location.reload();
    } catch (error) { feedback.textContent = error.message; button.disabled = false; }
  });
  if (panels.length || form) load(Boolean(form));
  window.addEventListener('finance-summary', async event => {
    const revision = event.detail?.personal_revision;
    if (form || !panels.length || typeof revision !== 'string' || !revision || revision === lastSummaryRevision) return;
    if (await load(false)) lastSummaryRevision = revision;
  });
})();
