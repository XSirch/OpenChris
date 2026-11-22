import {
  esc,
  has,
  money,
  percent,
  date,
  isoToday,
  icon,
  header,
  metric,
  row,
  rowLink,
  action,
  link,
  empty,
  note,
} from './ui.js';

export const kinds = {
  unknown: 'A classificar',
  expense: 'Despesa',
  income: 'Receita',
  transfer: 'Transferência',
  refund: 'Estorno',
  bill_payment: 'Pagamento de fatura',
};
const origins = {
  manual: 'Cadastro manual',
  csv: 'Extrato CSV',
  pluggy: 'Pluggy',
  bank: 'Banco',
  forecast: 'Previsão',
};
const origin = (key) => origins[key] || esc(key) || 'Sem origem';
const op = (id, text, kind = 'text-btn') => action(id, text, kind);
const sectionTitle = (text) => `<h2 class="section-title">${text}</h2>`;
const noAmount = 'A informar';
const changeLabel = (value) =>
  Number(value) > 0 ? 'acima da média' : Number(value) < 0 ? 'abaixo da média' : 'na média';
function priority(fin) {
  const { portfolio, spending, contribution } = fin;
  if (Number(contribution.shortfall) > 0 && contribution.confirmed)
    return [
      'Seu saldo precisa de ' + money(contribution.shortfall),
      'Os compromissos e o saldo mínimo superam o dinheiro disponível.',
      'Conferir o cálculo',
      'aportes',
    ];
  const top = [...spending.categories]
    .filter((c) => has(c.change) && Number(c.change) > 0)
    .sort((a, b) => Number(b.change) - Number(a.change))[0];
  if (top)
    return [
      esc(top.category) + ' subiu ' + money(top.change),
      'Em relação à média dos períodos anteriores até o mesmo dia.',
      'Entender a diferença',
      'gastos?categoria=' + encodeURIComponent(top.category),
    ];
  if (Number(spending.pending) > 0)
    return [
      'Revise ' + spending.pending + (Number(spending.pending) === 1 ? ' lançamento' : ' lançamentos'),
      'A categoria ainda precisa de confirmação.',
      'Revisar gastos',
      'gastos',
    ];
  if (!portfolio.positions.length)
    return [
      'Monte sua carteira',
      'Importe um extrato ou registre seu primeiro investimento.',
      'Adicionar investimentos',
      'carteira',
    ];
  if (!contribution.confirmed)
    return [
      'Quanto você pode aportar?',
      'Informe o saldo, as contas e o mínimo que deseja manter.',
      'Calcular meu aporte',
      'aportes',
    ];
  return [
    'Aporte disponível de ' + money(contribution.available),
    'Confira como esse valor se distribui entre as classes da carteira.',
    'Ver meu aporte',
    'aportes',
  ];
}
export function today(fin, economy) {
  const [title, desc, cta, route] = priority(fin);
  const { portfolio: p, spending: s, contribution: c } = fin;
  const upcoming = economy?.events
    ?.filter((e) => e.date >= isoToday())
    .sort((a, b) => a.date.localeCompare(b.date))[0];
  const agenda = upcoming
    ? rowLink('economia?evento=' + encodeURIComponent(upcoming.id), esc(upcoming.title))
    : empty(economy ? 'A agenda ainda não tem eventos futuros.' : 'A agenda está indisponível.') +
      link('economia', 'Consultar economia');
  return (
    header('Hoje', date(fin.as_of || isoToday(), { day: 'numeric', month: 'long' })) +
    /* HTML */ `<div class="columns">
      <div class="stack">
        <section class="hero-note">
          ${icon('chart', 31)}
          <h2>${title}</h2>
          <p>${desc}</p>
          ${link(route, cta + ' ' + icon('arrow', 18))}
        </section>
        <section class="metric-group">
          ${metric(Object.keys(p.foreign_totals || {}).length ? 'Patrimônio investido em reais' : 'Patrimônio investido', money(p.total))}
          <div class="metrics">
            ${rowLink('gastos', 'Gastos do mês', money(s.total), Number(s.change) > 0 ? 'warm' : '')}${rowLink('aportes', 'Aporte disponível', c.confirmed ? money(c.available) : noAmount)}
          </div>
        </section>
      </div>
      <section>${sectionTitle('Na agenda')}${agenda}</section>
    </div>`
  );
}
function assetClass(position) {
  return position.asset_class || 'A classificar';
}

function asset(position) {
  const facts = [
    ['Classe', assetClass(position)],
    ['Emissor', position.issuer],
    ['Indexador', position.indexer],
    ['Vencimento', position.maturity && date(position.maturity)],
    ['Identificador', position.identifier],
    ['Atualização', position.updated_at && date(position.updated_at, { dateStyle: 'short', timeStyle: 'short' })],
  ];
  return /* HTML */ `
    <details class="accordion portfolio-position">
      <summary>
        <strong>${esc(position.name)}<small>${esc(position.institution || 'Instituição não identificada')}</small></strong>
        <span class="numeric">${money(position.value, position.currency || 'BRL')}</span>
        ${icon('down', 16, 'rotate')}
      </summary>
      <div class="details-body">
        <dl class="meta">
          ${facts.filter(([, value]) => has(value)).map(([label, value]) =>
            `<div><dt>${label}</dt><dd>${esc(value)}</dd></div>`).join('')}
          <div><dt>Origem</dt><dd>${origin(position.source)}</dd></div>
        </dl>
        ${position.needs_review ? note('Classificação pendente de confirmação.') : ''}
        <div class="actions">
          ${action('asset', 'Ver ficha e fontes', 'text-btn', `data-id="${esc(position.id)}"`)}
          ${action('edit-position', 'Editar posição', 'text-btn', `data-id="${esc(position.id)}"`)}
        </div>
      </div>
    </details>`;
}

const groupLabels = { class: 'Classe', institution: 'Instituição', asset: 'Ativo', issuer: 'Emissor' };
const fixedIncome = (position) => assetClass(position).toLocaleLowerCase('pt-BR') === 'renda fixa';
const searchKey = (value) => value.toLocaleLowerCase('pt-BR').normalize('NFD').replace(/\p{M}/gu, '');
const cents = (value) => Math.round(Number(value) * 100);
const share = (part, total) => total > 0 ? 100 * part / total : null;

function distribution(positions, mode, targets) {
  const groups = new Map();
  for (const position of positions) {
    let name, key;
    if (mode === 'asset') {
      name = position.name;
      key = position.identifier ? 'code:' + position.identifier.toUpperCase() : 'id:' + position.id;
    } else if (mode === 'institution') {
      name = position.institution || 'Instituição não identificada';
      key = searchKey(name);
    } else if (mode === 'issuer') {
      name = position.issuer || 'Emissor não informado';
      key = searchKey(name);
    } else {
      name = key = assetClass(position);
    }
    if (!groups.has(key)) groups.set(key, { key, name, value: 0, positions: [] });
    const group = groups.get(key);
    group.value += cents(position.value);
    group.positions.push(position);
  }
  for (const name of Object.keys(targets)) {
    if (!groups.has(name)) groups.set(name, { key: name, name, value: 0, positions: [] });
  }
  return [...groups.values()].sort((a, b) => b.value - a.value || a.name.localeCompare(b.name, 'pt-BR'));
}

function allocationBar(group, index, total, currency, target, selected) {
  const weight = share(group.value, total);
  const diff = Math.round(((weight ?? 0) - (target ?? 0)) * 10) / 10;
  const distance = diff
    ? ` · ${Math.abs(diff).toLocaleString('pt-BR')} pp ${diff > 0 ? 'acima' : 'abaixo'}`
    : ' · Na meta';
  return /* HTML */ `
    <button type="button" class="portfolio-bar" data-group="${index}"
      aria-pressed="${selected}" aria-controls="portfolio-results">
      <span class="portfolio-bar-label">
        <strong>${esc(group.name)}</strong>
        <span class="numeric">${money(group.value / 100, currency)}${weight === null ? '' : ' · ' + percent(weight)}</span>
      </span>
      <span class="portfolio-track" aria-hidden="true">
        <span class="portfolio-fill" style="inline-size:${weight ?? 0}%"></span>
        ${has(target) ? `<span class="portfolio-marker" style="inset-inline-start:${target}%"></span>` : ''}
      </span>
      ${has(target) ? `<small>Meta ${percent(target)}${weight === null ? '' : distance}</small>` : ''}
    </button>`;
}

export function portfolio(fin) {
  const p = fin.portfolio;
  const currencies = Object.entries(p.foreign_totals || {}).sort(([a], [b]) => a.localeCompare(b));
  const missing = p.missing_connections;
  return header('Carteira') + /* HTML */ `
    <div class="portfolio-page">
      <section class="portfolio-balance" aria-label="Patrimônio investido">
        <div>
          ${p.positions.length
            ? metric('Patrimônio investido em reais', money(p.total))
            : empty('A carteira está vazia. Cadastre uma posição ou importe um extrato para começar.')}
          ${currencies.length ? `<ul class="portfolio-currencies">${currencies.map(([code, value]) =>
            `<li>${esc(code)} <strong class="numeric">${money(value, code)}</strong></li>`).join('')}</ul>` : ''}
          ${missing ? `<p class="portfolio-coverage">${missing === 1 ? '1 conexão sem dados' : `${missing} conexões sem dados`}.
            ${link('mais?abrir=conexoes', 'Ver conexões')}</p>` : ''}
        </div>
        <div class="actions portfolio-actions">
          ${op('position', 'Cadastrar posição', p.positions.length ? 'text-btn' : 'primary')}
          ${op('import-positions', 'Importar extrato')}
          ${missing ? '' : link('mais?abrir=conexoes', 'Ver conexões')}
        </div>
      </section>
      ${p.positions.length ? `
        <div class="portfolio-grid">
          <section class="portfolio-analysis" aria-labelledby="distribution-heading">
            <div class="portfolio-section-head">
              <h2 id="distribution-heading">Distribuição</h2>
              ${currencies.length ? `<label class="field portfolio-currency">Moeda
                <select id="portfolio-currency">
                  ${['BRL', ...currencies.map(([code]) => code)].map((code) =>
                    `<option value="${esc(code)}">${esc(code)}</option>`).join('')}
                </select></label>` : ''}
            </div>
            <div class="portfolio-modes" role="group" aria-label="Agrupar distribuição">
              ${Object.entries(groupLabels).map(([key, title]) =>
                `<button type="button" data-portfolio-mode="${key}" aria-pressed="${key === 'class'}"${key === 'issuer' ? ' hidden' : ''}>${title}</button>`).join('')}
            </div>
            <div class="portfolio-chart" id="portfolio-chart"></div>
            <button id="portfolio-more" type="button" class="text-btn" aria-expanded="false" aria-controls="portfolio-chart" hidden>Ver todos os ativos</button>
            <div id="portfolio-context" class="portfolio-context"></div>
          </section>
          <section class="portfolio-list" aria-labelledby="portfolio-heading">
            <h2 id="portfolio-heading" tabindex="-1">Posições</h2>
            <label class="field">Buscar posição
              <input id="portfolio-search" type="search" placeholder="Nome, código ou emissor">
            </label>
            <div class="portfolio-filter">
              <p id="portfolio-count" class="muted" role="status"></p>
              <button id="portfolio-clear" type="button" class="text-btn" hidden>Limpar filtro</button>
            </div>
            <div id="portfolio-results" class="details-list"></div>
          </section>
        </div>` : ''}
    </div>`;
}

export function mountPortfolio(root, fin, view) {
  const chart = root.querySelector('#portfolio-chart');
  if (!chart) return;
  const modes = [...root.querySelectorAll('[data-portfolio-mode]')];
  const currencyControl = root.querySelector('#portfolio-currency');
  const search = root.querySelector('#portfolio-search');
  const count = root.querySelector('#portfolio-count');
  const heading = root.querySelector('#portfolio-heading');
  const clear = root.querySelector('#portfolio-clear');
  const more = root.querySelector('#portfolio-more');
  const context = root.querySelector('#portfolio-context');
  const list = root.querySelector('#portfolio-results');
  const profile = fin.profile;
  let groups = [];

  if (currencyControl) {
    currencyControl.value = view.currency;
    if (!currencyControl.value) currencyControl.value = 'BRL';
  }
  view.currency = currencyControl?.value || 'BRL';
  search.value = view.query;

  function renderPositions() {
    const group = groups.find((g) => g.key === view.group);
    const pool = group?.positions || fin.portfolio.positions.filter((p) => p.currency === view.currency);
    const terms = searchKey(view.query.trim()).split(/\s+/).filter(Boolean);
    const positions = pool.filter((position) => {
      const text = searchKey([position.name, position.identifier, position.issuer].filter(Boolean).join(' '));
      return terms.every((term) => text.includes(term));
    });
    const quantity = positions.length + (positions.length === 1 ? ' posição' : ' posições');
    count.textContent = group ? `${group.name} · ${quantity}` : quantity;
    clear.hidden = !group;
    list.innerHTML = positions.length ? positions.map(asset).join('') : empty(
      terms.length ? 'Nenhuma posição encontrada para essa busca.' : 'Nenhuma posição para este filtro.',
    );
  }

  function renderChart() {
    const positions = fin.portfolio.positions.filter((p) => p.currency === view.currency);
    const fixed = positions.filter(fixedIncome);
    modes.find((button) => button.dataset.portfolioMode === 'issuer').hidden = !fixed.length;
    if (!fixed.length && view.mode === 'issuer') {
      view.mode = 'class';
      view.group = null;
    }
    const included = view.mode === 'issuer' ? fixed : positions;
    const total = included.reduce((sum, position) => sum + cents(position.value), 0);
    const targets = view.mode === 'class' && view.currency === 'BRL' ? profile.targets : {};
    const showTargets = Object.keys(targets).length > 0;
    groups = distribution(included, view.mode, targets);
    if (!groups.some((group) => group.key === view.group)) view.group = null;
    for (const button of modes) {
      button.setAttribute('aria-pressed', button.dataset.portfolioMode === view.mode);
    }
    const hasMore = view.mode === 'asset' && groups.length > 5;
    const visible = hasMore && !view.expanded ? groups.slice(0, 5) : groups;
    more.hidden = !hasMore;
    more.setAttribute('aria-expanded', view.expanded);
    more.textContent = view.expanded ? 'Mostrar só os cinco maiores' : 'Ver todos os ativos';
    chart.innerHTML = visible.map((group, index) => allocationBar(
      group, index, total, view.currency, showTargets ? Number(targets[group.name] || 0) : null,
      group.key === view.group,
    )).join('') || empty('Nenhuma posição nesta moeda.');
    const topShare = share(groups.slice(0, 5).reduce((sum, group) => sum + group.value, 0), total);
    context.innerHTML = [
      hasMore && topShare !== null ? note(`Os cinco maiores ativos representam ${percent(topShare)} do total em ${esc(view.currency)}.`) : '',
      view.mode === 'issuer' ? note('Percentuais sobre o total da renda fixa nesta moeda.') : '',
      showTargets ? note((profile.targets_manual ? 'Metas definidas por você.' : 'Metas sugeridas pelo seu perfil.')
        + ' O traço indica a meta em cada barra. pp significa pontos percentuais.') : '',
      view.mode === 'class' && view.currency === 'BRL' ? link('aportes?abrir=simulacao', 'Simular aporte') : '',
    ].join('');
    renderPositions();
  }

  for (const button of modes) {
    button.addEventListener('click', () => {
      view.mode = button.dataset.portfolioMode;
      view.group = null;
      view.expanded = false;
      renderChart();
    });
  }
  currencyControl?.addEventListener('change', () => {
    view.currency = currencyControl.value;
    view.group = null;
    view.expanded = false;
    renderChart();
  });
  search.addEventListener('input', () => {
    view.query = search.value;
    renderPositions();
  });
  chart.addEventListener('click', (event) => {
    const button = event.target.closest('[data-group]');
    if (!button) return;
    const key = groups[Number(button.dataset.group)].key;
    view.group = view.group === key ? null : key;
    view.query = search.value = '';
    renderChart();
    heading.focus();
  });
  clear.addEventListener('click', () => {
    view.group = null;
    renderChart();
    search.focus();
  });
  more.addEventListener('click', () => {
    view.expanded = !view.expanded;
    if (!view.expanded && groups.slice(5).some((group) => group.key === view.group)) view.group = null;
    renderChart();
    if (view.expanded) chart.querySelector('[data-group="5"]')?.focus();
  });
  renderChart();
}
export function matchingCharges(advance, entries) {
  return entries.filter(
    (e) =>
      e.id !== advance.id &&
      !e.advanced_by &&
      e.source !== 'advance' &&
      !e.installment_group &&
      ['unknown', 'expense'].includes(e.kind) &&
      e.date === advance.date &&
      Number(e.amount) === Number(advance.amount),
  );
}
export function editableEntry(e, advances = []) {
  return e.source === 'manual' && !e.advanced_by && !advances.some((advance) => advance.id === e.id);
}

function entry(e, advances) {
  const credit = e.kind === 'income' || e.kind === 'refund';
  const debit = e.kind === 'expense';
  const tone = credit ? 'positive' : debit ? 'expense-value' : '';
  const inflow = e.bank_direction === 'inflow';
  const outflow = e.bank_direction === 'outflow';
  const sign = credit ? '+' : debit ? '−' : inflow ? '+' : outflow ? '−' : '';
  const direction = credit || debit ? '' : inflow ? 'Entrada no extrato' : outflow ? 'Saída no extrato' : '';
  const kind = e.kind === 'refund' && e.category === 'Desconto de antecipação' ? 'Crédito na fatura' : kinds[e.kind];
  return /* HTML */ `<article class="entry">
    <div class="split">
      <h3>${esc(e.description)}</h3>
      <strong class="numeric ${tone}">${sign}${money(e.amount)}</strong>
    </div>
    ${note([date(e.date, { day: 'numeric', month: 'short' }), esc(kind), direction, e.category !== kind ? esc(e.category) : '', e.installment_group ? 'Parcela ' + esc(e.installment_index || '') : '', e.advanced_by ? 'Antecipada' : ''].filter(Boolean).join(' · '))}
    <div class="actions">
      ${editableEntry(e, advances) ? action('edit-entry', 'Editar lançamento', 'text-btn', `data-id="${esc(e.id)}"`) : ''}
      ${action('correct', 'Corrigir classificação', 'text-btn', `data-id="${esc(e.id)}"`)}
    </div>
  </article>`;
}
function comparison(c, cutoff) {
  const avg = has(c.average),
    max = Math.max(Number(c.total), Number(c.average), 1);
  const bar = (name, v) =>
    /* HTML */ `<div class="bar-line">
      <span>${name}</span
      ><progress value="${Math.max(0, Number(v))}" max="${max}" aria-label="${name}"></progress
      ><span>${money(v)}</span>
    </div>`;
  const changes = (c.merchants || []).filter((m) => Number(m.change) !== 0);
  return `<h2>${esc(c.category)}</h2><p class="value">${money(c.total)}</p>${
    avg
      ? /* HTML */ `<div class="bars">
            ${bar('Este mês', c.total)}${bar('Média até dia ' + Number(cutoff.slice(-2)), c.average)}
          </div>
          <h3 class="${Number(c.change) > 0 ? 'warm' : 'positive'}">
            ${Number(c.change) !== 0 ? money(Math.abs(Number(c.change))) + ' ' : ''}${changeLabel(c.change)}
          </h3>
          ${changes.length ? /* HTML */ `<div class="table-foot">${changes.map((m) => row(esc(m.merchant || m.description), `${Number(m.change) > 0 ? '+' : ''}${money(m.change)}`)).join('')}</div>` : ''}`
      : note('A comparação aparece quando houver lançamentos nos meses anteriores.')
  }`;
}
export function spending(fin, state) {
  const s = fin.spending,
    categories = s.categories,
    cutoff = s.cutoff || fin.as_of;
  const category =
    categories.find((c) => c.category === state.category) ||
    [...categories].sort(
      (a, b) => Number(b.change || 0) - Number(a.change || 0) || Number(b.total) - Number(a.total),
    )[0];
  const chooser =
    categories.length > 1
      ? /* HTML */ `<label class="field"
          ><span>Categoria</span
          ><select id="category">
            ${categories.map((c) => `<option ${c === category ? 'selected' : ''}>${esc(c.category)}</option>`).join('')}
          </select></label
        >`
      : '';
  const entries = s.entries || [];
  const advances = (s.advances || []).filter(
    (a) => a.source === 'advance' && matchingCharges(a, entries).length,
  );
  const reconciliation = advances
    .map(
      (a) =>
        /* HTML */ `<div class="inset">
          <strong>${esc(a.description)}</strong
          >${note(date(a.date) + ' · ' + money(a.amount))}${action('link-charge', 'Vincular cobrança da fatura', 'text-btn', `data-id="${esc(a.id)}"`)}
        </div>`,
    )
    .join('');
  const totalLabel =
    fin.month > fin.as_of.slice(0, 7)
      ? 'Gastos contabilizados'
      : 'Gastos até o dia ' + Number(cutoff.slice(-2));
  return (
    header('Gastos') +
    /* HTML */ `<div class="columns">
      <div class="stack">
        <section>
          <div class="nav-month">
            ${action('prev-month', icon('left'), 'icon-btn', 'aria-label="Mês anterior"')}<input
              id="month"
              type="month"
              aria-label="Mês dos gastos"
              value="${esc(fin.month)}"
            />${action('next-month', icon('right'), 'icon-btn', 'aria-label="Próximo mês"')}
          </div>
          ${metric(totalLabel, money(s.total))}
        </section>
        <section class="stack tight">
          ${category ? chooser + '<div>' + comparison(category, cutoff) + '</div>' : sectionTitle('Gastos por categoria') + empty('Nenhuma despesa registrada neste período.')}${has(category?.change) ? /* HTML */ `<div class="inset split"><span>Efeito estimado no aporte</span><strong>${money(-Number(category.change))}</strong></div>` : ''}
        </section>
        ${
          s.history?.length
            ? /* HTML */ `<details class="accordion">
                <summary><strong>Meses usados na comparação</strong>${icon('down', 16, 'rotate')}</summary>
                ${s.history.map((h) => row('Até ' + date(h.through), money(h.total))).join('')}
              </details>`
            : ''
        }
      </div>
      <div class="stack">
        <section>
          ${sectionTitle('Lançamentos')}
          <div class="actions">
            ${op('entry', 'Novo lançamento', 'primary')}${op('import-entries', 'Importar extrato', 'secondary')}${op('finance-chat', 'Conversar com a IA', 'secondary')}
          </div>
          ${s.installments?.length ? op('advance', 'Antecipar parcelas') : ''}
        </section>
        ${reconciliation}
        <section aria-label="Lista de lançamentos">
          ${entries.length ? entries.map((e) => entry(e, s.advances)).join('') : empty('Os lançamentos deste mês aparecerão aqui.')}
        </section>
      </div>
    </div>`
  );
}
export function contributions(fin, state) {
  const c = fin.contribution,
    p = fin.profile,
    allocations = c.allocation || [];
  const assigned = allocations.filter((a) => Number(a.amount) > 0);
  return (
    header('Aportes') +
    /* HTML */ `<div class="columns">
      <div class="stack">
        <section>
          <p class="metric-label">Disponível para aportar</p>
          <div class="split">
            <p class="value accent ${c.confirmed ? '' : 'unknown'}">
              ${c.confirmed ? money(c.available) : noAmount}
            </p>
            ${op('balance', 'Ajustar')}
          </div>
          ${c.warnings?.length
            ? c.warnings.map((warning) => note(esc(warning))).join('')
            : !c.confirmed ? note('Complete os valores abaixo para calcular o aporte.') : ''}
        </section>
        <div class="rows">
          ${row('Saldo em conta', money(c.balance))}${row('Contas até a próxima entrada', has(c.commitments) ? '−' + money(c.commitments) : '—')}${row('Saldo mínimo', has(c.minimum) ? '−' + money(c.minimum) : '—')}${has(c.next_income_date) ? row('Próxima entrada', date(c.next_income_date)) : ''}${Number(c.shortfall) > 0 ? row('Valor que falta', '<strong class="warm">' + money(c.shortfall) + '</strong>') : ''}
        </div>
        ${c.uncertain_commitments?.length ? note('Há compras de cartão sem fatura identificada. Confira a cobrança e informe o total das contas em Ajustar.') : ''}${
          c.breakdown?.length
            ? /* HTML */ `<details class="accordion">
                <summary><strong>Contas previstas</strong>${icon('down', 16, 'rotate')}</summary>
                ${c.breakdown.map((b) => row(esc(b.description) + ' · ' + date(b.date), money(b.amount))).join('')}
              </details>`
            : ''
        }
        <section>
          ${sectionTitle('Valor da simulação')}
          <div class="split">
            <span class="numeric">${has(c.simulated) ? money(c.simulated) : 'A definir'}</span
            >${op('simulate', 'Alterar valor')}
          </div>
          ${has(state.amount) ? op('clear-simulation', 'Usar aporte disponível') : ''}
        </section>
      </div>
      <div class="stack">
        <section>
          ${sectionTitle('Distribuição sugerida')}${
            assigned.length
              ? assigned
                  .map(
                    (a) =>
                      /* HTML */ `<div class="allocation">
                        <div class="split">
                          <strong>${esc(a.asset_class)}</strong
                          ><span>${money(a.amount)} · ${percent(a.percent)}</span>
                        </div>
                        <progress
                          max="100"
                          value="${Number(a.percent)}"
                          aria-label="${esc(a.asset_class)}"
                        ></progress>
                      </div>`,
                  )
                  .join('')
              : empty(
                  !p.goal
                    ? 'Preencha seu perfil para definir as metas por classe.'
                    : !Number(c.simulated)
                      ? 'Com um valor para simular, você verá quanto destinar a cada classe.'
                      : 'Revise as metas de distribuição para este aporte.',
                )
          }${!p.goal ? op('perfil', 'Preencher perfil') : ''}
        </section>
        ${
          allocations.length
            ? /* HTML */ `<section>
                ${sectionTitle('Carteira depois do aporte')}
                <div class="rows">
                  ${allocations.map((a) => row(esc(a.asset_class), money(a.projected_value) + ' · ' + percent(a.projected_percent))).join('')}
                </div>
              </section>`
            : ''
        }
        <section>
          <details class="accordion">
            <summary><strong>Por que essas classes?</strong>${icon('down', 16, 'rotate')}</summary>
            <div class="details-body">
              ${note(esc(p.target_reason || 'As metas levam em conta seu prazo, sua reserva e sua tolerância a perdas. Os aportes vão para as classes abaixo da meta.'))}
            </div>
          </details>
          <div class="actions">${op('targets', 'Ajustar metas')}${op('perfil', 'Perfil financeiro')}</div>
        </section>
      </div>
    </div>`
  );
}
