import { api, esc, has, isoToday, icon, action, link, money, date } from './ui.js?v=carbono-4';
import { today, portfolio, mountPortfolio, spending, contributions, contributionResults } from './finance-views.js?v=carbono-4';
import { mountPerformance } from './performance.js?v=carbono-4';
import { calculators, calcResult, economy, more, researchers, agentSettings, effortChoice, assetSheet } from './tool-views.js?v=carbono-4';
import { documentsView, uploadDocumentForm, deleteDocumentForm, chatDocumentChoices, chatDocumentReferences } from './documents.js?v=carbono-4';
import {
  advanceForm,
  linkChargeForm,
  balanceForm,
  connections,
  correctForm,
  entryForm,
  importForm,
  positionForm,
  profileForm,
  targetsForm,
} from './forms.js?v=carbono-4';

const titles = {
  hoje: 'Hoje',
  carteira: 'Carteira',
  gastos: 'Gastos',
  aportes: 'Aportes',
  mais: 'Mais',
  economia: 'Economia',
  calculadoras: 'Calculadoras',
  documentos: 'Documentos',
};
const nav = document.querySelector('#nav-links'),
  main = document.querySelector('main'),
  dlg = document.querySelector('#dialog'),
  body = document.querySelector('#dialog-body');
const notice = document.querySelector('#notice');
const noticeText = document.querySelector('#notice-text');
const noticeStatus = document.querySelector('#notice-status');
const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
const state = {
  route: '',
  month: isoToday().slice(0, 7),
  amount: null,
  category: '',
  ecoTab: 'agenda',
  event: null,
  portfolio: { currency: 'BRL', mode: 'class', group: null, query: '', expanded: false },
};
let finance = null,
  conn = null,
  agents = null,
  economic = null,
  documentLibrary = null,
  loadId = 0,
  dialogId = 0,
  noticeTimer,
  noticeOrigin,
  pointerOnNotice = false;
const currentMonth = () => isoToday().slice(0, 7);
const financePath = (month = state.month, amount = state.amount) =>
  '/api/finance?' + new URLSearchParams({ month, ...(has(amount) ? { amount } : {}) });
function dismissNotice() {
  clearTimeout(noticeTimer);
  if (notice.inert) return;
  const restoreFocus = notice.contains(document.activeElement);
  notice.inert = true;
  pointerOnNotice = false;
  document.removeEventListener('pointermove', trackNoticePointer);
  noticeStatus.replaceChildren();
  if (restoreFocus) (noticeOrigin?.isConnected ? noticeOrigin : main).focus({ preventScroll: true });
}
function scheduleNotice() {
  clearTimeout(noticeTimer);
  if (!notice.inert && !document.hidden && !pointerOnNotice && !notice.matches(':focus-within')) {
    noticeTimer = setTimeout(dismissNotice, 6000);
  }
}
function trackNoticePointer(event) {
  if (event.pointerType === 'touch') return;
  const { left, right, top, bottom } = notice.getBoundingClientRect();
  const inside = event.clientX >= left && event.clientX <= right && event.clientY >= top && event.clientY <= bottom;
  if (pointerOnNotice === inside) return;
  pointerOnNotice = inside;
  scheduleNotice();
}
function notify(message, isError = false, origin = document.activeElement) {
  if (dlg.open) {
    if (isError) dialogError(message);
    else document.querySelector('#dialog-status').textContent = message;
    return;
  }
  noticeOrigin = origin;
  noticeText.textContent = message;
  document.querySelector('#notice-icon').innerHTML = icon(isError ? 'alert' : 'check', 15);
  notice.classList.toggle('is-error', isError);
  notice.classList.remove('in-top');
  const area = notice.getBoundingClientRect();
  const overlaps = [...main.querySelectorAll('form .actions')].some(group => {
    const r = group.getBoundingClientRect();
    return r.bottom > area.top - 8 && r.top < area.bottom + 8 && r.right > area.left && r.left < area.right;
  });
  notice.classList.toggle('in-top', overlaps);
  notice.inert = false;
  noticeStatus.replaceChildren(document.createTextNode(message));
  document.addEventListener('pointermove', trackNoticePointer, { passive: true });
  scheduleNotice();
}
notice.querySelector('button').addEventListener('click', dismissNotice);
notice.addEventListener('focusin', () => clearTimeout(noticeTimer));
notice.addEventListener('focusout', () => queueMicrotask(scheduleNotice));
document.addEventListener('visibilitychange', scheduleNotice);
document.querySelector('#top-date').innerHTML = icon('economia', 17) + date(isoToday(), { day: 'numeric', month: 'long', year: 'numeric' });

function setNav() {
  const selected = ['economia', 'calculadoras', 'documentos'].includes(state.route) ? 'mais' : state.route;
  if (nav.dataset.selected === selected) return;
  nav.dataset.selected = selected;
  nav.innerHTML = ['hoje', 'carteira', 'gastos', 'aportes', 'mais']
    .map(
      (k) =>
        `<a data-route href="/${k}" ${k === selected ? 'aria-current="page"' : ''}><span class="nav-icon">${icon(k, 21)}</span><span>${titles[k]}</span></a>`,
    )
    .join('');
}
function closeDialog() {
  ++dialogId;
  if (dlg.open) dlg.close();
}
function dialog(title, content) {
  dismissNotice();
  const id = ++dialogId;
  document.querySelector('#dialog-title').textContent = title;
  body.innerHTML = content;
  document.querySelector('#dialog-error').textContent = '';
  document.querySelector('#dialog-status').textContent = '';
  if (!dlg.open) dlg.showModal();
  body.scrollTop = 0;
  return id;
}
function dialogError(msg) {
  const error = document.querySelector('#dialog-error');
  error.textContent = msg;
  if (msg && dlg.open) error.scrollIntoView({ block: 'nearest' });
}
function loadingDialog(title) {
  return dialog(title, '<p class="loading" role="status">Carregando…</p>');
}
function refreshFocus() {
  main.focus({ preventScroll: true });
  window.scrollTo(0, 0);
}
function focusDestination(route, params) {
  let target;
  if (route === 'aportes' && params.get('abrir') === 'simulacao') {
    target = main.querySelector('#valor-aporte');
  } else if (route === 'gastos' && (params.has('lancamento') || params.has('lista'))) {
    const entry = params.get('lancamento');
    target = (entry && document.getElementById('lancamento-' + entry)) || main.querySelector('#lancamentos');
  }
  if (target) {
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: 'start' });
  }
}
function go(href) {
  const url = new URL(href, location.href);
  if (url.origin !== location.origin) return;
  if (url.pathname + url.search !== location.pathname + location.search)
    history.pushState(null, '', url.pathname + url.search);
  void render(true);
}
async function render(change = false) {
  const seq = ++loadId,
    url = new URL(location.href);
  const path = url.pathname.split('/').filter(Boolean)[0] || 'hoje';
  const route = Object.hasOwn(titles, path) ? path : 'hoje';
  const changedRoute = Boolean(state.route && state.route !== route);
  if (change) {
    closeDialog();
    dismissNotice();
    if (state.route !== route) {
      state.category = '';
      if (route !== 'aportes') state.amount = null;
      if (route === 'gastos') state.month = currentMonth();
    }
    state.event = url.searchParams.get('evento');
    state.ecoTab = 'agenda';
    state.category = url.searchParams.get('categoria') || '';
  }
  state.route = route;
  document.title = titles[route] + ' · OpenChris';
  setNav();
  main.classList.remove('route-enter');
  main.setAttribute('aria-busy', 'true');
  if (change) {
    main.innerHTML = '<p class="loading" role="status">Carregando…</p>';
    refreshFocus();
  }
  const month = route === 'gastos' ? state.month : currentMonth();
  try {
    let content, performance;
    if (route === 'calculadoras') content = calculators();
    else if (route === 'mais') content = more();
    else if (route === 'documentos') {
      const library = await api('/api/documents');
      if (seq !== loadId) return;
      documentLibrary = library;
      content = documentsView(library);
    }
    else if (route === 'economia') {
      const data = await api(
        '/api/economy' + (state.event ? '?evento=' + encodeURIComponent(state.event) : ''),
      );
      if (seq !== loadId) return;
      economic = data;
      content = economy(economic, state);
    } else {
      const [f, e, perf] = await Promise.all([
        api(financePath(month)),
        route === 'hoje' ? api('/api/economy').catch(() => null) : null,
        ['hoje', 'carteira'].includes(route) ? api('/api/portfolio/performance').catch(() => null) : null,
      ]);
      if (seq !== loadId) return;
      finance = f;
      performance = perf;
      content =
        route === 'hoje'
          ? today(f, e, performance)
          : route === 'carteira'
            ? portfolio(f)
            : route === 'gastos'
              ? spending(f, state)
              : contributions(f, state);
    }
    if (seq !== loadId) return;
    main.innerHTML = content;
    main.classList.toggle('route-enter', changedRoute);
    if (route === 'carteira') {
      mountPortfolio(main, finance, state.portfolio);
      mountPerformance(main, performance);
    }
    main.setAttribute('aria-busy', 'false');
    if (change && route === 'mais' && url.searchParams.get('abrir') === 'conexoes') void openAction('banco');
    if (change) focusDestination(route, url.searchParams);
  } catch (err) {
    if (seq !== loadId) return;
    main.setAttribute('aria-busy', 'false');
    main.innerHTML = /* HTML */ `<div class="page-head"><h1>${titles[route]}</h1></div>
      <div role="alert">
        <p>${esc(err.message)}</p>
        ${action('retry', 'Tentar novamente')}
      </div>`;
  }
}
async function loadFinance() {
  if (finance) return finance;
  const data = await api(financePath(currentMonth()));
  finance = data;
  return data;
}
function swapMonth(offset) {
  const [year, month] = state.month.split('-').map(Number);
  const d = new Date(Date.UTC(year, month - 1 + offset, 1));
  state.month = d.toISOString().slice(0, 7);
  state.category = '';
  void render();
}
function saveCsv(kind) {
  const fields = conn?.csv_columns?.[kind] || [];
  if (!fields.length) return;
  const url = URL.createObjectURL(
    new Blob(['\uFEFF' + fields.join(',') + '\r\n'], { type: 'text/csv;charset=utf-8' }),
  );
  const a = document.createElement('a');
  a.href = url;
  a.download = kind === 'positions' ? 'posicoes.csv' : 'lancamentos.csv';
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
let widgetScript;
async function openWidget() {
  const id = loadingDialog('Conectar instituição');
  if (!window.PluggyConnect) {
    widgetScript ??= new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = 'https://cdn.pluggy.ai/pluggy-connect/v2.7.0/pluggy-connect.js';
      script.onload = resolve;
      script.onerror = () => {
        widgetScript = null;
        script.remove();
        reject(new Error('A Pluggy não carregou. Tente novamente.'));
      };
      document.head.append(script);
    });
    await widgetScript;
  }
  if (dialogId !== id || !dlg.open) return;
  const result = await api('/api/connections/token', {});
  if (dialogId !== id || !dlg.open) return;
  const widget = new window.PluggyConnect({
    connectToken: result.access_token,
    onSuccess: async ({ item }) => {
      try {
        await api('/api/connections', { item_id: item.id });
        finance = null;
        notify('Conexão bancária registrada.');
        await openAction('banco');
      } catch (err) {
        dialog('Conexão bancária', '');
        dialogError(err.message);
      }
    },
    onError: () => {
      dialog('Conexão bancária', '');
      dialogError('A Pluggy não concluiu a conexão. Tente novamente em Conexões e extratos.');
    },
  });
  closeDialog();
  widget.init();
}
async function signOut() {
  const token = document.querySelector('meta[name=csrf-token]').content;
  const response = await fetch('/logout', {
    method: 'POST',
    headers: { 'X-CSRF-Token': token },
    redirect: 'follow',
  });
  if (!response.ok) throw new Error('Não foi possível sair. Tente novamente.');
  location.assign('/login');
}
async function openAction(key, node) {
  const id = node?.dataset.id;
  if (key === 'finance-chat') {
    const chat = dialog('Chat financeiro', `
      <form class="chat-composer" data-form="finance-chat">
        <label class="field">Sua pergunta <textarea name="question" rows="2" maxlength="1000" required></textarea></label>
        <div class="chat-document-control" data-chat-documents></div>
        <div class="chat-actions">
          ${link('documentos', 'Gerenciar documentos')}
          <button type="submit" class="primary" disabled>Enviar pergunta</button>
        </div>
      </form>
      <section class="finance-chat" data-chat-thread aria-label="Conversa financeira" tabindex="0" hidden>
        <details class="accordion chat-history" data-chat-history hidden>
          <summary><strong>Conversas anteriores</strong><span class="note" data-chat-count></span>${icon('down', 16, 'rotate')}</summary>
          <div class="stack tight" data-chat-archive></div>
        </details>
        <div data-chat-log role="log" aria-label="Conversa mais recente"></div>
      </section>`);
    void loadChatDocuments(chat);
    await refreshFinanceChat(chat);
    return;
  }
  if (key === 'document-upload' && documentLibrary?.available) {
    dialog('Adicionar documento', uploadDocumentForm(documentLibrary));
    return;
  }
  if (key === 'document-delete' || key === 'document-toggle') {
    const doc = documentLibrary?.items.find(item => item.id === id);
    if (!doc || state.route !== 'documentos') return;
    if (key === 'document-delete') {
      dialog('Excluir documento', deleteDocumentForm(doc));
      body.querySelector('[data-action="fechar"]').focus();
    } else {
      const seq = loadId;
      node.disabled = true;
      try {
        const result = await api('/api/documents/' + encodeURIComponent(doc.id) + '/chat', { enabled: !doc.chat_enabled });
        if (seq !== loadId) return;
        await render();
        if (state.route !== 'documentos') return;
        const current = [...main.querySelectorAll('[data-action="document-toggle"]')].find(button => button.dataset.id === id);
        current?.focus({ preventScroll: true });
        notify(result.chat_enabled ? 'O chat já pode consultar este documento.' : 'Consulta pelo chat pausada.');
      } finally {
        node.disabled = false;
      }
    }
    return;
  }
  if (key === 'fechar') {
    closeDialog();
    return;
  }
  if (key === 'retry') {
    await render();
    return;
  }
  if (key === 'sair') {
    await signOut();
    return;
  }
  if (key === 'prev-month' || key === 'next-month') {
    swapMonth(key === 'prev-month' ? -1 : 1);
    return;
  }
  if (key === 'clear-simulation') {
    await submitSimulation(node.form, true);
    return;
  }
  if (key === 'clear-calculator') {
    const form = node.form;
    const { mode, rate_period: period, principal } = form.elements;
    const currentMode = mode.value;
    const currentPeriod = period.value;
    form.reset();
    mode.value = currentMode;
    period.value = currentPeriod;
    updateCalculatorMode(form);
    principal.focus();
    return;
  }
  if (key === 'csv-model') {
    saveCsv(node.dataset.kind);
    return;
  }
  if (key === 'eco-tab') {
    state.ecoTab = id;
    main.innerHTML = economy(economic, state);
    main.querySelector(`[data-id="${id}"]`)?.focus();
    return;
  }
  if (key === 'connect') {
    await openWidget();
    return;
  }
  if (key === 'banco' || key.startsWith('import-')) {
    const title = key === 'banco' ? 'Conexões e extratos' : 'Importar extrato',
      d = loadingDialog(title);
    const data = await api('/api/connections');
    if (dialogId !== d || !dlg.open) return;
    conn = data;
    body.innerHTML = key === 'banco' ? connections(data) : importForm(data, key.slice(7));
    return;
  }
  if (key === 'agent-settings' && Object.hasOwn(agents?.roles || {}, id)) {
    dialog(agents.roles[id], agentSettings(agents, id));
    body.querySelector('[name=model]').focus();
    return;
  }
  if (key === 'pesquisa' || key === 'asset') {
    const d = loadingDialog(key === 'asset' ? 'Ficha do investimento' : 'Agentes e pesquisas');
    const data = await api(key === 'asset' ? '/api/assets/' + encodeURIComponent(id) : '/api/agents');
    if (dialogId !== d || !dlg.open) return;
    if (key === 'pesquisa') agents = data;
    body.innerHTML =
      key === 'asset'
        ? assetSheet(
            data,
            finance?.portfolio?.positions.find((p) => String(p.id) === id),
          )
        : researchers(data);
    return;
  }
  if (key === 'entry') {
    dialog('Novo lançamento', entryForm());
    return;
  }
  if (key === 'correct' || key === 'retry-classification') {
    const path = `/api/entries/${encodeURIComponent(id)}/classification`;
    if (key === 'correct') {
      const d = loadingDialog('Corrigir lançamento');
      const review = await api(path);
      if (dialogId === d && dlg.open) body.innerHTML = correctForm(review);
      return;
    }
    const d = dialogId;
    node.disabled = true;
    dialogError('');
    try {
      const { notice } = await api(path, {});
      if (dialogId === d && dlg.open) {
        body.querySelector('[data-classification-notice]').textContent = notice;
        node.remove();
        body.querySelector('[name="kind"]').focus();
      }
    } catch (error) {
      if (dialogId === d && dlg.open) dialogError(error.message);
      else notify(error.message, true);
      node.disabled = false;
    }
    return;
  }
  const labels = {
    perfil: 'Perfil financeiro',
    targets: 'Metas por classe',
    balance: 'Saldo e compromissos',
    advance: 'Antecipar parcelas',
    position: 'Cadastrar posição',
    'edit-position': 'Editar posição',
    'edit-entry': 'Editar lançamento',
    'link-charge': 'Vincular cobrança da fatura',
  };
  if (!Object.hasOwn(labels, key)) return;
  const d = loadingDialog(labels[key]),
    f = await loadFinance();
  if (dialogId !== d || !dlg.open) return;
  const content = {
    perfil: () => profileForm(f.profile),
    targets: () => targetsForm(f.profile),
    balance: () => balanceForm(f.profile, f.contribution),
    advance: () => advanceForm(f),
    position: () => positionForm(f),
    'edit-position': () =>
      positionForm(
        f,
        f.portfolio.positions.find((p) => String(p.id) === id),
      ),
    'link-charge': () =>
      linkChargeForm(
        f.spending.advances.find((a) => String(a.id) === id),
        f.spending.entries,
      ),
    'edit-entry': () => entryForm(f.spending.entries.find((e) => String(e.id) === id)),
  };
  body.innerHTML = content[key]();
  if (key === 'targets') updateTargetTotal(body.querySelector('[data-form="targets"]'));
}
function handleError(err) {
  if (dlg.open) dialogError(err.message);
  else notify(err.message, true);
}
document.addEventListener('click', (ev) => {
  const node = ev.target.closest('[data-route],[data-action]');
  if (!node || ev.defaultPrevented) return;
  if (node.matches('a[data-route]')) {
    if (ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
    ev.preventDefault();
    go(node.href);
    return;
  }
  if (node.dataset.action) {
    ev.preventDefault();
    openAction(node.dataset.action, node).catch(handleError);
  }
});
document.addEventListener('change', (ev) => {
  const el = ev.target;
  if (el.id === 'month' && /^\d{4}-\d{2}$/.test(el.value)) {
    state.month = el.value;
    state.category = '';
    void render();
  } else if (el.id === 'category') {
    state.category = el.value;
    main.innerHTML = spending(finance, state);
    main.querySelector('#category').focus();
  } else if ((el.name === 'mode' || el.name === 'rate_period') && el.form?.dataset.form === 'calc') {
    updateCalculatorMode(el.form);
  } else if (el.name === 'kind' && el.closest('[data-form=import]')) {
    body.innerHTML = importForm(conn, el.value);
    body.querySelector('[name=kind]').focus();
  } else if (el.name === 'installment_group' && el.closest('[data-form=advance]')) {
    body.innerHTML = advanceForm(finance, el.value);
    body.querySelector('[name=installment_group]').focus();
  } else if (el.name === 'model' && el.closest('[data-form=agent-settings]')) {
    const previous = el.form.elements.effort.value;
    const effort = agents.models[el.value]?.efforts.includes(previous) ? previous : '';
    el.form.querySelector('[data-effort-choice]').innerHTML = effortChoice(agents.models, el.value, effort);
  }
});
document.addEventListener('input', (ev) => {
  const field = ev.target;
  const form = field.form;
  if (form?.dataset.form === 'simulate') {
    field.removeAttribute('aria-invalid');
    form.querySelector('#simulation-error').textContent = '';
    dismissNotice();
  }
  if (form?.dataset.form === 'targets') updateTargetTotal(form);
  if (form?.dataset.form !== 'calc') return;
  if (field.name === 'rate' || ['principal', 'monthly', 'target'].includes(field.name)) {
    if (ev.inputType === 'insertText' && ev.data === '.' && !field.value.includes(',')) {
      const caret = field.selectionStart;
      field.value = field.value.slice(0, caret - 1) + ',' + field.value.slice(caret);
      field.setSelectionRange(caret, caret);
    }
    formatCalculatorField(field, false, /^(insertText|delete)/.test(ev.inputType));
  }
  form.querySelector('#calc-result').replaceChildren();
});
document.addEventListener('focusout', (ev) => {
  const field = ev.target;
  if (field.form?.dataset.form === 'calc' && ['principal', 'monthly', 'target'].includes(field.name))
    formatCalculatorField(field, true);
});
dlg.addEventListener('close', () => {
  if (!dlg.open) body.replaceChildren();
});
window.addEventListener('popstate', () => void render(true));
window.addEventListener('pageshow', (ev) => {
  if (ev.persisted) location.reload();
});

function updateCalculatorMode(form) {
  const mode = form.elements.mode.value;
  for (const group of form.querySelectorAll('[data-hide-in]')) {
    group.hidden = group.disabled = group.dataset.hideIn === mode;
  }
  form.querySelector('#calc-result').replaceChildren();
}

function calculatorNumber(value, rate = false) {
  const clean = value.trim().replace(rate ? /\s*%$/ : /^R\$\s*/i, '');
  if (/^-?(?:\d+|\d{1,3}(?:\.\d{3})+),\d*$/.test(clean) ||
      (!rate && /^\d{1,3}(?:\.\d{3})+$/.test(clean)))
    return clean.replaceAll('.', '').replace(',', '.');
  return clean;
}

function updateDistribution(result) {
  const output = main.querySelector('#contribution-result');
  const former = new Map();
  if (!reducedMotion.matches) {
    for (const bar of output.querySelectorAll('[data-allocation]')) {
      const trackWidth = bar.parentElement.getBoundingClientRect().width;
      if (trackWidth > 0) former.set(bar.dataset.allocation, bar.getBoundingClientRect().width / trackWidth);
    }
  }
  output.innerHTML = contributionResults(result);
  if (reducedMotion.matches) return;
  for (const bar of output.querySelectorAll('[data-allocation]')) {
    const fraction = parseFloat(bar.style.inlineSize) / 100;
    const start = former.get(bar.dataset.allocation) ?? 0;
    if (fraction > 0) {
      bar.style.setProperty('--start-scale', start / fraction);
      bar.classList.add('recalculated');
    }
  }
}

async function submitSimulation(form, clear = false) {
  const submit = form.querySelector('[type=submit]');
  const reset = form.querySelector('[data-action="clear-simulation"]');
  const field = form.elements.amount;
  const error = form.querySelector('#simulation-error');
  if (submit.disabled) return;
  const original = field.value;
  const amount = clear ? null : calculatorNumber(original);
  dismissNotice();
  error.textContent = '';
  field.removeAttribute('aria-invalid');
  if (!clear && !/^\d+(?:\.\d{1,2})?$/.test(amount)) {
    error.textContent = 'Informe um valor em reais, por exemplo 1.250,50.';
    field.setAttribute('aria-invalid', 'true');
    field.focus();
    return;
  }
  const sequence = loadId;
  submit.disabled = reset.disabled = true;
  form.setAttribute('aria-busy', 'true');
  try {
    const result = await api(financePath(currentMonth(), amount));
    if (!form.isConnected || sequence !== loadId || field.value !== original) return;
    state.amount = amount;
    finance = result;
    updateDistribution(result);
    reset.hidden = clear;
    if (clear) {
      const available = result.contribution.simulated;
      field.value = has(available) ? String(available).replace('.', ',') : '';
      field.focus({ preventScroll: true });
    }
    notify(clear ? 'Valor inicial restaurado.' : 'Simulação de ' + money(result.contribution.simulated) + ' recalculada.', false, clear ? field : submit);
  } catch (err) {
    if (form.isConnected && sequence === loadId && field.value === original) {
      error.textContent = err.message;
    }
  } finally {
    submit.disabled = reset.disabled = false;
    form.removeAttribute('aria-busy');
  }
}

function formatCalculatorField(field, fixed = false, editing = false) {
  const rate = field.name === 'rate';
  const raw = !rate && editing && !field.value.includes(',') ? field.value.replaceAll('.', '') : field.value;
  const normalized = calculatorNumber(raw, rate);
  if (!/^-?\d+(?:\.\d*)?$/.test(normalized) || (!rate && normalized.startsWith('-'))) return;
  const [whole, decimals] = normalized.split('.');
  const precision = rate ? 8 : 2;
  if ((decimals?.length ?? 0) > precision) return;
  const integer = rate ? whole : whole.replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  const fraction = fixed ? (decimals ?? '').padEnd(2, '0') : decimals;
  const value = fraction === undefined ? integer : integer + ',' + fraction;
  if (value === field.value) return;
  const after = field.value.slice(field.selectionEnd).replace(/\D/g, '').length;
  field.value = value;
  if (!fixed) {
    let caret = value.length;
    for (let left = after; left > 0 && caret > 0;) {
      if (/\d/.test(value[--caret])) left--;
    }
    field.setSelectionRange(caret, caret);
  }
}

async function submitCalculator(form) {
  const fields = new FormData(form);
  const submitted = new URLSearchParams(fields).toString();
  const { mode, rate_period: period, principal, monthly = '', target = '', months, rate } = Object.fromEntries(fields);
  const output = form.querySelector('#calc-result');
  const button = form.querySelector('[type=submit]');
  button.disabled = true;
  output.innerHTML = '<p class="loading">Calculando…</p>';
  let content;
  try {
    const result = await api('/api/calculate', {
      mode,
      principal: calculatorNumber(principal) || '0',
      monthly: calculatorNumber(monthly) || '0',
      target: calculatorNumber(target),
      months,
      [period === 'month' ? 'monthly_rate' : 'annual_rate']: calculatorNumber(rate, true),
    });
    content = calcResult(mode, period, result);
  } catch (err) {
    content = `<p class="error">${esc(err.message)}</p>`;
  } finally {
    button.disabled = false;
  }
  if (form.isConnected && submitted === new URLSearchParams(new FormData(form)).toString()) {
    output.innerHTML = content;
    output.scrollIntoView({ block: 'nearest' });
  }
}

async function loadChatDocuments(id) {
  try {
    const library = await api('/api/documents');
    if (dialogId === id && dlg.open) {
      body.querySelector('[data-chat-documents]').innerHTML = chatDocumentChoices(library);
    }
  } catch {
    if (dialogId === id && dlg.open) {
      body.querySelector('[data-chat-documents]').insertAdjacentHTML('afterbegin',
        '<p class="note">Não foi possível carregar os documentos. Abra o chat novamente para consultá-los.</p>');
    }
  }
}

function chatExchange(message, waiting) {
  const references = message.references?.length
    ? `<p class="note">Pesquisas citadas</p><ul class="chat-references">${message.references.map(ref =>
        `<li>${link('economia?evento=' + encodeURIComponent(ref.id), esc(ref.title) + ' · ' + date(ref.date))}</li>`
      ).join('')}</ul>`
    : '';
  const status = message.state === 'running' ? 'Preparando a resposta…'
    : message.state === 'failed' ? message.notice || 'Envie a pergunta novamente.'
      : waiting || message.notice || 'Pergunta na fila…';
  return `<div class="inset"><p><strong>Você · ${esc(message.month.split('-').reverse().join('/'))}</strong></p>
    <p class="chat-text">${esc(message.question)}</p>
    ${message.answer ? `<p><strong>OpenChris</strong></p><p class="chat-text">${esc(message.answer)}</p>${references}
      ${chatDocumentReferences(message.document_references)}
      ${message.document_notice ? '<p class="note">' + esc(message.document_notice) + '</p>' : ''}`
      : `<p class="note">${esc(status)}</p>`}</div>`;
}

async function refreshFinanceChat(id) {
  if (dialogId !== id || !dlg.open) return;
  const thread = body.querySelector('[data-chat-thread]');
  const history = thread.querySelector('[data-chat-history]');
  const count = history.querySelector('[data-chat-count]');
  const archive = history.querySelector('[data-chat-archive]');
  const log = thread.querySelector('[data-chat-log]');
  let latestContent, archiveContent;
  while (dialogId === id && dlg.open) {
    const { messages, waiting } = await api('/api/finance-chat');
    if (dialogId !== id || !dlg.open) return;
    const latest = messages.at(-1);
    const older = messages.slice(0, -1).reverse();
    const nextLatest = latest ? chatExchange(latest, waiting) : '';
    const nextArchive = older.map(message => chatExchange(message, waiting)).join('');
    if (latestContent !== nextLatest) {
      log.innerHTML = latestContent = nextLatest;
    }
    if (archiveContent !== nextArchive) {
      archive.innerHTML = archiveContent = nextArchive;
      count.textContent = older.length;
    }
    history.hidden = !older.length;
    thread.hidden = !latest;
    const pending = messages.some(m => ['pending', 'running', 'retry'].includes(m.state));
    body.querySelector('form button[type=submit]').disabled = pending;
    if (!pending) return;
    await new Promise(resolve => setTimeout(resolve, 4000));
  }
}

function targetTotal(form) {
  return [...form.querySelectorAll('[data-class]')].reduce((sum, field) =>
    sum + Math.round(Number(field.value || 0) * 100), 0);
}

function updateTargetTotal(form) {
  const total = targetTotal(form);
  const gap = 10000 - total;
  const pretty = (value) => (value / 100).toLocaleString('pt-BR', { maximumFractionDigits: 2 }) + '%';
  form.querySelector('[data-target-total]').textContent = 'Total de ' + pretty(total)
    + (gap ? ' · ' + (gap > 0 ? 'Falta ' : 'Excede em ') + pretty(Math.abs(gap)) : '');
  form.querySelector('[type="submit"]').disabled = gap !== 0;
}

async function submit(form) {
  const key = form.dataset.form;
  if (key === 'calc') return submitCalculator(form);
  if (key === 'simulate') return submitSimulation(form);
  const fd = new FormData(form),
    data = Object.fromEntries(fd),
    btn = form.querySelector('[type=submit]');
  const currentDialog = dialogId;
  btn.disabled = true;
  dialogError('');
  try {
    if (key === 'finance-chat') {
      await api('/api/finance-chat', { question: data.question, month: state.month, document: data.document || 'none' });
      if (dialogId !== currentDialog || !dlg.open) return;
      form.reset();
      const thread = body.querySelector('[data-chat-thread]');
      thread.querySelector('[data-chat-history]').open = false;
      thread.scrollTop = 0;
      body.scrollTop = 0;
      await refreshFinanceChat(currentDialog);
      return;
    }
    if (key === 'document-upload' || key === 'document-delete') {
      const upload = key === 'document-upload';
      if (upload && fd.get('file')?.size > Number(form.dataset.maxSize)) {
        throw new Error('O arquivo excede 5 MB.');
      }
      const endpoint = upload ? '/api/documents' : '/api/documents/' + encodeURIComponent(form.dataset.id) + '/delete';
      const result = await api(endpoint, upload ? fd : {});
      if (currentDialog !== dialogId || !dlg.open) return;
      closeDialog();
      await render();
      if (state.route !== 'documentos') return;
      main.querySelector('#saved-documents')?.focus({ preventScroll: true });
      const message = !upload ? 'Documento excluído.' : result.reading === 'ready' ? 'Documento guardado.'
        : result.reading === 'partial' ? 'Documento guardado. A leitura pelo chat é parcial.'
          : 'Documento guardado. O texto não está disponível para o chat.';
      notify(message);
      return;
    }
    if (key === 'agent-settings') {
      await api('/api/agents/' + encodeURIComponent(form.dataset.kind) + '/settings', data);
      if (currentDialog === dialogId && dlg.open) {
        await openAction('pesquisa');
        body.querySelector(`[data-action="agent-settings"][data-id="${form.dataset.kind}"]`)?.focus();
      }
      notify('Escolha salva.');
      return;
    }
    let endpoint,
      payload = data,
      message;
    if (key === 'import') {
      endpoint = 'import';
      payload = fd;
    } else if (key === 'connect-id') {
      endpoint = 'connections';
      message = 'Conexão registrada.';
    } else if (key === 'entry' || key === 'position') {
      endpoint = key === 'entry' ? 'entries' : 'positions';
      message = key === 'entry' ? 'Lançamento salvo.' : 'Posição salva.';
      payload = Object.fromEntries(Object.entries(data).filter(([, v]) => v !== ''));
      if (key === 'position' && data.external_id) {
        const previous = finance.portfolio.positions.find((p) => p.external_id === data.external_id);
        payload = Object.fromEntries(
          Object.entries(payload).filter(([k, v]) => k === 'external_id' || String(previous[k] ?? '') !== v),
        );
        if (Object.keys(payload).length === 1) {
          closeDialog();
          return;
        }
      }
    } else if (key === 'correct') {
      endpoint = 'corrections';
      payload = { ...data, remember: fd.has('remember') };
      message = 'Correção salva.';
    } else if (key === 'advance') {
      const ids = fd.getAll('entry_ids');
      if (!ids.length) throw new Error('Marque pelo menos uma parcela para antecipar.');
      endpoint = 'advance';
      payload = { entry_ids: ids, date: data.date, amount: data.amount };
      if (data.charge_entry_id) payload.charge_entry_id = data.charge_entry_id;
      message = 'Antecipação registrada.';
    } else if (key === 'link-charge') {
      const previous = finance.spending.advances.find((a) => String(a.id) === data.advance_id);
      endpoint = 'advance';
      payload = {
        entry_ids: previous.entry_ids,
        date: previous.date,
        amount: previous.amount,
        charge_entry_id: data.charge_entry_id,
      };
      message = 'Cobrança vinculada à antecipação.';
    } else if (key === 'reset-targets') {
      endpoint = 'profile';
      payload = { targets: null };
      message = 'Metas do perfil restauradas.';
    } else if (key === 'targets') {
      endpoint = 'profile';
      const targets = Object.fromEntries(
        [...form.querySelectorAll('[data-class]')].map((el) => [el.dataset.class, el.value || '0']),
      );
      if (targetTotal(form) !== 10000)
        throw new Error('As metas devem somar 100%.');
      payload = { targets };
      message = 'Metas atualizadas.';
    } else if (key === 'balance' || key === 'perfil') {
      endpoint = 'profile';
      payload = Object.fromEntries(Object.entries(data).map(([k, v]) => [k, v === '' ? null : v]));
      message = key === 'balance' ? 'Saldo e compromissos atualizados.' : 'Perfil salvo.';
    } else return;
    const result = await api('/api/' + endpoint, payload);
    if (key === 'import')
      message = has(result.imported)
        ? 'Importação concluída. Registros processados: ' + result.imported + '.'
        : 'Importação concluída.';
    finance = null;
    if (currentDialog === dialogId && dlg.open) {
      closeDialog();
      await render();
      if (key === 'connect-id') await openAction('banco');
    }
    notify(message);
  } catch (err) {
    if (currentDialog === dialogId && dlg.open) dialogError(err.message);
    else notify(err.message, true);
  } finally {
    btn.disabled = false;
  }
}
document.addEventListener('submit', (ev) => {
  if (ev.target.matches('form[data-form]')) {
    ev.preventDefault();
    void submit(ev.target);
  }
});
void render(true);
