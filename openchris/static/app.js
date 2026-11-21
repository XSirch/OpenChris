import { api, esc, has, isoToday, icon, action, link, money } from './ui.js';
import { today, portfolio, spending, contributions } from './finance-views.js';
import { calculators, calcResult, economy, more, researchers, assetSheet } from './tool-views.js';
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
  simulateForm,
  targetsForm,
} from './forms.js';

const titles = {
  hoje: 'Hoje',
  carteira: 'Carteira',
  gastos: 'Gastos',
  aportes: 'Aportes',
  mais: 'Mais',
  economia: 'Economia',
  calculadoras: 'Calculadoras',
};
const nav = document.querySelector('#nav-links'),
  main = document.querySelector('main'),
  dlg = document.querySelector('#dialog'),
  body = document.querySelector('#dialog-body');
const state = {
  route: '',
  month: isoToday().slice(0, 7),
  amount: null,
  category: '',
  calcMode: 'compound',
  ecoTab: 'agenda',
  event: null,
};
let finance = null,
  conn = null,
  economic = null,
  loadId = 0,
  dialogId = 0,
  noticeTimer;
const currentMonth = () => isoToday().slice(0, 7);
const financePath = (month = state.month) =>
  '/api/finance?' + new URLSearchParams({ month, ...(has(state.amount) ? { amount: state.amount } : {}) });
function notify(message) {
  clearTimeout(noticeTimer);
  const el = document.querySelector('#notice');
  el.textContent = message;
  noticeTimer = setTimeout(() => (el.textContent = ''), 5000);
}
function setNav() {
  const selected = ['economia', 'calculadoras'].includes(state.route) ? 'mais' : state.route;
  nav.innerHTML = ['hoje', 'carteira', 'gastos', 'aportes', 'mais']
    .map(
      (k) =>
        `<a data-route href="/${k}" ${k === selected ? 'aria-current="page"' : ''}>${icon(k, 23)}<span>${titles[k]}</span></a>`,
    )
    .join('');
}
function closeDialog() {
  ++dialogId;
  if (dlg.open) dlg.close();
}
function dialog(title, content) {
  const id = ++dialogId;
  document.querySelector('#dialog-title').textContent = title;
  body.innerHTML = content;
  document.querySelector('#dialog-error').textContent = '';
  if (!dlg.open) dlg.showModal();
  dlg.scrollTop = 0;
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
  if (change) {
    closeDialog();
    if (state.route !== route) {
      state.calcMode = 'compound';
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
  main.setAttribute('aria-busy', 'true');
  if (change) {
    main.innerHTML = '<p class="loading" role="status">Carregando…</p>';
    refreshFocus();
  }
  const month = route === 'gastos' ? state.month : currentMonth();
  try {
    let content;
    if (route === 'calculadoras') content = calculators(state.calcMode);
    else if (route === 'mais') content = more();
    else if (route === 'economia') {
      const data = await api(
        '/api/economy' + (state.event ? '?evento=' + encodeURIComponent(state.event) : ''),
      );
      if (seq !== loadId) return;
      economic = data;
      content = economy(economic, state);
    } else {
      const [f, e] = await Promise.all([
        api(financePath(month)),
        route === 'hoje' ? api('/api/economy').catch(() => null) : null,
      ]);
      if (seq !== loadId) return;
      finance = f;
      content =
        route === 'hoje'
          ? today(f, e)
          : route === 'carteira'
            ? portfolio(f)
            : route === 'gastos'
              ? spending(f, state)
              : contributions(f, state);
    }
    if (seq !== loadId) return;
    main.innerHTML = content;
    main.setAttribute('aria-busy', 'false');
    if (change && route === 'mais' && url.searchParams.get('abrir') === 'conexoes') void openAction('banco');
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
    state.amount = null;
    await render();
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
  if (key === 'pesquisa' || key === 'asset') {
    const d = loadingDialog(key === 'asset' ? 'Ficha do investimento' : 'Agentes e pesquisas');
    const data = await api(key === 'asset' ? '/api/assets/' + encodeURIComponent(id) : '/api/agents');
    if (dialogId !== d || !dlg.open) return;
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
  const labels = {
    perfil: 'Perfil financeiro',
    targets: 'Metas por classe',
    balance: 'Saldo e compromissos',
    simulate: 'Simular outro valor',
    advance: 'Antecipar parcelas',
    position: 'Cadastrar posição',
    'edit-position': 'Editar posição',
    'edit-entry': 'Editar lançamento',
    'link-charge': 'Vincular cobrança da fatura',
    correct: 'Corrigir lançamento',
  };
  if (!Object.hasOwn(labels, key)) return;
  const d = loadingDialog(labels[key]),
    f = await loadFinance();
  if (dialogId !== d || !dlg.open) return;
  const content = {
    perfil: () => profileForm(f.profile),
    targets: () => targetsForm(f.profile),
    balance: () => balanceForm(f.profile, f.contribution),
    simulate: () => simulateForm(f.contribution),
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
    correct: () => correctForm(f.spending.entries.find((e) => String(e.id) === id)),
  };
  body.innerHTML = content[key]();
}
function handleError(err) {
  if (dlg.open) dialogError(err.message);
  else notify(err.message);
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
  } else if (el.name === 'calcMode') {
    state.calcMode = el.value;
    main.innerHTML = calculators(state.calcMode);
    main.querySelector('input[name=calcMode]:checked').focus();
  } else if (el.name === 'kind' && el.closest('[data-form=import]')) {
    body.innerHTML = importForm(conn, el.value);
    body.querySelector('[name=kind]').focus();
  } else if (el.name === 'installment_group' && el.closest('[data-form=advance]')) {
    body.innerHTML = advanceForm(finance, el.value);
    body.querySelector('[name=installment_group]').focus();
  }
});
document.addEventListener('input', (ev) => {
  if (ev.target.closest('[data-form=calc]')) document.querySelector('#calc-result').replaceChildren();
});
dlg.addEventListener('close', () => {
  if (!dlg.open) body.replaceChildren();
});
window.addEventListener('popstate', () => void render(true));
window.addEventListener('pageshow', (ev) => {
  if (ev.persisted) location.reload();
});

async function submit(form) {
  const key = form.dataset.form,
    fd = new FormData(form),
    data = Object.fromEntries(fd),
    btn = form.querySelector('[type=submit]');
  const currentDialog = dialogId;
  btn.disabled = true;
  dialogError('');
  try {
    if (key === 'calc') {
      const mode = state.calcMode,
        seq = loadId,
        serialized = new URLSearchParams(fd).toString();
      const result = await api('/api/calculate', { ...data, mode });
      if (
        seq === loadId &&
        mode === state.calcMode &&
        form.isConnected &&
        serialized === new URLSearchParams(new FormData(form)).toString()
      )
        document.querySelector('#calc-result').innerHTML = calcResult(mode, result);
      return;
    }
    if (key === 'simulate') {
      state.amount = data.amount;
      closeDialog();
      await render();
      notify('Simulação atualizada.');
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
      if (Object.values(targets).reduce((sum, v) => sum + Math.round(Number(v) * 100), 0) !== 10000)
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
    if (key === 'calc' && form.isConnected) {
      const output = document.querySelector('#calc-result');
      output.innerHTML = `<p class="error">${esc(err.message)}</p>`;
    } else if (currentDialog === dialogId && dlg.open) dialogError(err.message);
    else notify(err.message);
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
