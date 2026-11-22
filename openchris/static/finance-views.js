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
function asset(pos) {
  const labels = {
    issuer: 'Emissor',
    indexer: 'Indexador',
    maturity: 'Vencimento',
    institution: 'Instituição',
    identifier: 'Identificador',
  };
  const meta = Object.entries(labels)
    .filter(([key]) => has(pos[key]))
    .map(
      ([key, label]) =>
        /* HTML */ `<div>
          <dt>${label}</dt>
          <dd>${key === 'maturity' ? date(pos[key]) : esc(pos[key])}</dd>
        </div>`,
    )
    .join('');
  return /* HTML */ `<article class="asset">
    <div class="split">
      <h3>${esc(pos.name)}</h3>
      <span class="numeric">${money(pos.value, pos.currency || 'BRL')}</span>
    </div>
    <dl class="meta">
      ${meta}
      <div class="full">
        <dt>Origem</dt>
        <dd>${origin(pos.source)}${pos.updated_at ? ' · ' + date(pos.updated_at) : ''}</dd>
      </div>
    </dl>
    ${pos.needs_review ? note('Classificação pendente de confirmação.') : ''}
    <div class="actions">
      ${action('asset', 'Ver ficha e fontes', 'text-btn', `data-id="${esc(pos.id)}"`)}${action('edit-position', 'Editar posição', 'text-btn', `data-id="${esc(pos.id)}"`)}
    </div>
  </article>`;
}
function classGroup(name, rows, group, idx) {
  const currencies = new Set(rows.map((p) => p.currency || 'BRL'));
  const sorted = [...rows].sort(
    (a, b) => (a.institution || '').localeCompare(b.institution || '') || a.name.localeCompare(b.name),
  );
  const byInstitution = new Map();
  for (const p of sorted) {
    const institution = p.institution || 'Instituição não informada';
    if (!byInstitution.has(institution)) byInstitution.set(institution, []);
    byInstitution.get(institution).push(p);
  }
  const fixedIncome = name.toLocaleLowerCase('pt-BR') === 'renda fixa';
  const detail = fixedIncome
    ? /* HTML */ `<details class="inset">
        <summary class="text-btn">Ver títulos por instituição ${icon('right', 14)}</summary>
        ${[...byInstitution]
          .map(
            ([inst, items]) =>
              /* HTML */ `<section class="table-foot">
                <h4>${esc(inst)}</h4>
                ${items.map(asset).join('')}
              </section>`,
          )
          .join('')}
      </details>`
    : sorted.map(asset).join('');
  return /* HTML */ `<details class="accordion" ${fixedIncome ? 'open' : ''}>
    <summary>
      <span class="swatch c${idx % 6}"></span><strong>${esc(name)}</strong
      ><span class="pair"
        >${
          group
            ? money(group.value)
            : currencies.size === 1
              ? money(
                  rows.reduce((sum, p) => sum + Number(p.value), 0),
                  [...currencies][0],
                )
              : 'Por moeda'
        }<small>${group ? percent(group.percent) : 'Moeda de origem'}</small></span
      >${icon('down', 16, 'rotate')}
    </summary>
    <div class="details-body">${detail}</div>
  </details>`;
}
export function portfolio(fin) {
  const p = fin.portfolio,
    classes = p.classes,
    positions = p.positions;
  const names = [
    ...new Set([
      ...classes.map((c) => c.asset_class),
      ...positions.map((p) => p.asset_class || 'A classificar'),
    ]),
  ];
  let x = 0;
  const chart = classes
    .map((c, i) => {
      const width = Math.max(0, Math.min(100 - x, Number(c.percent) || 0)),
        start = x;
      x += width;
      return `<svg x="${start}%" width="${width}%" height="100%"><rect width="100%" height="100%" class="c${i % 6}"/>${width >= 13 ? `<text x="50%" y="60%" text-anchor="middle" fill="${i % 6 < 2 ? 'white' : '#101925'}" font-size="12" font-weight="650">${percent(c.percent)}</text>` : ''}</svg>`;
    })
    .join('');
  const updated = positions
    .map((p) => p.updated_at)
    .filter(Boolean)
    .sort()
    .at(-1);
  const foreign = Object.entries(p.foreign_totals || {})
    .map(([currency, value]) => row('Em ' + esc(currency), money(value, currency)))
    .join('');
  return (
    header('Carteira') +
    /* HTML */ `<div class="columns">
      <div class="stack">
        <section class="metric-group">
          ${metric('Patrimônio investido', money(p.total))}${foreign}${chart ? /* HTML */ `<div class="bar-chart" aria-hidden="true"><svg width="100%" height="31">${chart}</svg></div>` : ''}
        </section>
        <section aria-label="Investimentos por classe">
          ${
            names.length
              ? /* HTML */ `<div class="details-list">
                  ${names
                    .map((n, i) =>
                      classGroup(
                        n,
                        positions.filter((p) => (p.asset_class || 'A classificar') === n),
                        classes.find((c) => c.asset_class === n),
                        i,
                      ),
                    )
                    .join('')}
                </div>`
              : sectionTitle('Seus investimentos') +
                empty(
                  'A carteira está vazia. Registre uma posição pelo valor atual ou importe o extrato da instituição.',
                )
          }${updated ? `<p class="note table-foot">Atualizado em ${date(updated, { dateStyle: 'short', timeStyle: 'short' })}.</p>` : ''}
        </section>
      </div>
      <section class="stack tight">
        <h2>Adicionar investimentos</h2>
        <div class="actions">
          ${op('position', 'Cadastrar posição', 'primary')}${op('import-positions', 'Importar extrato', 'secondary')}
        </div>
        ${link('mais?abrir=conexoes', 'Ver conexões bancárias')}
      </section>
    </div>`
  );
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
  return /* HTML */ `<article class="entry">
    <div class="split">
      <h3>${esc(e.description)}</h3>
      <strong class="numeric ${tone}">${sign}${money(e.amount)}</strong>
    </div>
    ${note([date(e.date, { day: 'numeric', month: 'short' }), esc(kinds[e.kind]), direction, e.category !== kinds[e.kind] ? esc(e.category) : '', e.installment_group ? 'Parcela ' + esc(e.installment_index || '') : '', e.advanced_by ? 'Antecipada' : ''].filter(Boolean).join(' · '))}
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
