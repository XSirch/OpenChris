const icons = {
  hoje: '<path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1z"/>',
  carteira:
    '<path d="M19 7V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h15a1 1 0 0 0 1-1V9a2 2 0 0 0-2-2H5a2 2 0 0 1 0-4"/><path d="M21 12h-5v4h5"/>',
  gastos: '<path d="m5 3 2.3 1.6L9.7 3 12 4.6 14.3 3l2.4 1.6L19 3v18l-2.3-1.6-2.4 1.6L12 19.4 9.7 21l-2.4-1.6L5 21z"/><path d="M8.7 9.3h6.6m-6.6 4.4h5"/>',
  aportes:
    '<ellipse cx="15" cy="6" rx="6" ry="3"/><path d="M9 6v4c0 1.7 2.7 3 6 3s6-1.3 6-3V6M21 10v4c0 1.7-2.7 3-6 3"/><ellipse cx="8" cy="14" rx="6" ry="3"/><path d="M2 14v4c0 1.7 2.7 3 6 3s6-1.3 6-3v-4"/>',
  mais: '<rect x="4" y="4" width="6" height="6" rx="1.25"/><rect x="14" y="4" width="6" height="6" rx="1.25"/><rect x="4" y="14" width="6" height="6" rx="1.25"/><rect x="14" y="14" width="6" height="6" rx="1.25"/>',
  chat: '<path d="M19 13a7 7 0 0 1-7 5.5H9l-4 2v-4A7 7 0 0 1 10 4.5h3m5-2v6m-3-3h6"/>',
  basket: '<path d="M3 10h18l-2.2 9a1 1 0 0 1-1 .8H6.2a1 1 0 0 1-1-.8L3 10zm4 0 3-6m7 6-3-6M9 13.5l.6 3m5.4-3-.6 3"/>',
  alert: '<path d="m10.3 4.3-8 14a1.6 1.6 0 0 0 1.4 2.4h16.6a1.6 1.6 0 0 0 1.4-2.4l-8-14a2 2 0 0 0-3.4 0zM12 9v4.2m0 3v.2"/>',
  info: '<circle cx="12" cy="12" r="8.8"/><path d="M12 11v5m0-9v.2"/>',
  check: '<path d="m4.5 12 5.1 5.1 10-10.4"/>',
  chart:
    '<path d="m4 17 6-6 4 3 7-9"/><circle cx="4" cy="17" r="1.6"/><circle cx="10" cy="11" r="1.6"/><circle cx="14" cy="14" r="1.6"/><circle cx="21" cy="5" r="1.6"/>',
  right: '<path d="m8 5 7 7-7 7"/>',
  left: '<path d="m15 5-7 7 7 7"/>',
  down: '<path d="m5 9 7 7 7-7"/>',
  arrow: '<path d="M3 12h17m-6-6 6 6-6 6"/>',
  plus: '<path d="M12 4v16M4 12h16"/>',
  economia:
    '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4m10-4v4M3 10h18m-14 4h4m2 0h3"/>',
  calculadoras:
    '<rect x="5" y="2" width="14" height="20" rx="2"/><path d="M8 7h8M8 12h1m5 0h1m-7 5h1m5 0h1"/>',
  perfil:
    '<circle cx="12" cy="12" r="10"/><circle cx="12" cy="9" r="3.2"/><path d="M5 19a7.3 7.3 0 0 1 14 0"/>',
  banco: '<path d="m3 8 9-5 9 5M4 9h16M6 11v7m6-7v7m6-7v7M3 21h18"/>',
  pesquisa: '<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6"/>',
  sair: '<path d="M12 4H4v16h8m3-13 5 5-5 5M8 12h12"/>',
  importar: '<path d="M12 17V3m-5 5 5-5 5 5M4 15v6h16v-6"/>',
};
export function icon(key, size = 21, cls = '') {
  return `<svg aria-hidden="true" class="${cls}" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="1.65">${icons[key] || ''}</svg>`;
}
export function esc(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c],
  );
}
export const has = (value) => value !== null && value !== undefined && value !== '';
export function money(value, currency = 'BRL') {
  if (!has(value) || !Number.isFinite(Number(value))) return '—';
  try {
    return new Intl.NumberFormat('pt-BR', {
      style: 'currency',
      currency,
      minimumFractionDigits: Number(value) % 1 ? 2 : 0,
      maximumFractionDigits: 2,
    }).format(Number(value));
  } catch {
    return `${Number(value).toLocaleString('pt-BR')} ${esc(currency)}`;
  }
}
export const percent = (value) =>
  has(value) ? `${Number(value).toLocaleString('pt-BR', { maximumFractionDigits: 1 })}%` : '—';
export const isoToday = () =>
  new Intl.DateTimeFormat('sv-SE', { timeZone: 'America/Sao_Paulo' }).format(new Date());
export function date(value, opts = { day: 'numeric', month: 'short', year: 'numeric' }) {
  if (!value) return 'Sem data';
  const dayOnly = /^\d{4}-\d{2}-\d{2}$/.test(value);
  const instant = new Date(
    dayOnly ? value + 'T12:00:00Z' : /^\d{4}-\d\d-\d\dT[\d:.]+$/.test(value) ? value + 'Z' : value,
  );
  return Number.isNaN(instant.valueOf())
    ? 'Sem data'
    : new Intl.DateTimeFormat('pt-BR', { timeZone: 'America/Sao_Paulo', ...opts }).format(instant);
}
export function ref(url, label) {
  try {
    const target = new URL(url);
    if (['https:', 'http:'].includes(target.protocol))
      return `<a href="${esc(target.href)}" target="_blank" rel="noopener noreferrer">${esc(label || target.hostname)}</a>`;
  } catch {}
  return esc(label || 'Fonte sem link');
}
export function action(id, label, style = 'text-btn', data = '') {
  return `<button type="button" class="${style}" data-action="${id}" ${data}>${label}</button>`;
}
export const link = (path, label, cls = 'text-btn') =>
  `<a data-route href="/${path}" class="${cls}">${label}</a>`;
export const rowLink = (route, label, value = '', tone = '') =>
  link(
    route,
    `<span>${label}</span><span class="end"><strong class="number ${tone}">${value}</strong>${icon('right', 16)}</span>`,
    'row-link',
  );
export const row = (label, value) =>
  /* HTML */ `<div class="row"><span>${label}</span><span class="number">${value}</span></div>`;
export const header = (name, lead = '', back = false) =>
  /* HTML */ `<header class="page-head">
    ${back ? link('mais', icon('left', 14) + 'Mais', 'back') : ''}${lead ? `<p>${esc(lead)}</p>` : ''}
    <h1>${name}</h1>
  </header>`;
export const note = (text) => `<p class="note">${text}</p>`;
export const empty = (text) => `<p class="empty">${text}</p>`;
export const metric = (label, value, cls = '') =>
  `<p class="metric-label">${label}</p><p class="value ${cls}">${value}</p>`;
export const buttonSubmit = (label) => `<button type="submit" class="primary block">${label}</button>`;
export function field(
  name,
  label,
  { type = 'text', value = '', required = false, attr = '', help = '', unit = '', prefix = false } = {},
) {
  const input = `<input name="${name}" type="${type}" value="${esc(value)}" ${required ? 'required' : ''} ${attr}>`;
  const suffix = `<span class="unit" aria-hidden="true">${esc(unit)}</span>`;
  return /* HTML */ `<label class="field"
    ><span>${esc(label)}${unit ? `<span class="sr"> (${esc(unit)})</span>` : ''}</span
    >${unit ? `<span class="unit-input">${prefix ? suffix : ''}${input}${prefix ? '' : suffix}</span>` : input}${help ? `<small>${esc(help)}</small>` : ''}</label
  >`;
}
export function choice(name, label, options, value = '', required = false) {
  return /* HTML */ `<label class="field"
    ><span>${esc(label)}</span
    ><select name="${name}" ${required ? 'required' : ''}>
      ${Object.entries(options)
        .map(
          ([key, text]) =>
            `<option value="${esc(key)}" ${key === value ? 'selected' : ''}>${esc(text)}</option>`,
        )
        .join('')}
    </select></label
  >`;
}
export function amountField(name, label, value = '', required = true, options = {}) {
  return field(name, label, {
    type: 'number',
    unit: 'R$',
    prefix: true,
    attr: 'step="0.01" min="0" inputmode="decimal"',
    value,
    required,
    ...options,
  });
}
export function form(id, fields, submit) {
  return /* HTML */ `<form data-form="${id}">
    <div class="form-grid">${fields}</div>
    ${buttonSubmit(submit)}
  </form>`;
}
export async function api(path, data, signal) {
  const opts = { headers: { Accept: 'application/json' }, signal, cache: 'no-store' };
  if (data !== undefined) {
    opts.method = 'POST';
    opts.headers['X-CSRF-Token'] = document.querySelector('meta[name=csrf-token]').content;
    if (data instanceof FormData) opts.body = data;
    else {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(data);
    }
  }
  let response;
  try {
    response = await fetch(path, opts);
  } catch (err) {
    if (err.name === 'AbortError') throw err;
    throw new Error('A conexão falhou. Confira a rede e tente novamente.');
  }
  if (response.status === 401 || (response.redirected && new URL(response.url).pathname === '/login')) {
    location.assign('/login?next=' + encodeURIComponent(location.pathname + location.search));
    throw new Error('A sessão terminou. Entre novamente.');
  }
  const result = await response.json().catch(() => null);
  if (!response.ok || result === null)
    throw new Error(
      typeof result?.error === 'string' ? result.error : 'O servidor não concluiu o pedido. Tente novamente.',
    );
  return result;
}
