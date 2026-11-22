import {
  esc,
  date,
  isoToday,
  has,
  money,
  icon,
  field,
  amountField,
  choice,
  action,
  buttonSubmit,
  form,
  note,
} from './ui.js';
import { kinds, matchingCharges } from './finance-views.js';

const baseClasses = ['Renda fixa', 'Ações', 'Fundos imobiliários', 'Exterior'];
const options = (labels) => Object.fromEntries(labels.map((x) => [x, x]));
const dt = (name, label, value, required = true) => field(name, label, { type: 'date', value, required });
const integer = (name, label, value) =>
  field(name, label, { type: 'number', value, attr: 'min="1" step="1" inputmode="numeric"' });
export function positionForm(fin, p = {}) {
  const classes = [
    ...new Set([
      ...(fin.profile.target_classes || baseClasses),
      ...fin.portfolio.positions.map((c) => c.asset_class).filter(Boolean),
    ]),
  ];
  const id = p.id ? `<input type="hidden" name="external_id" value="${esc(p.external_id)}">` : '';
  return form(
    'position',
    id +
      field('name', 'Nome do investimento', { required: true, value: p.name }) +
      field('value', 'Valor atual na moeda da posição', {
        type: 'number',
        value: p.value,
        attr: 'step="0.01" min="0" inputmode="decimal"',
        required: true,
      }) +
      field('currency', 'Moeda', {
        value: p.currency || 'BRL',
        attr: 'maxlength="3" pattern="[A-Za-z]{3}"',
        required: true,
      }) +
      choice('asset_class', 'Classe', { '': 'Classificar depois', ...options(classes) }, p.asset_class) +
      field('identifier', 'Código ou identificador', { value: p.identifier }) +
      field('institution', 'Instituição', { value: p.institution }) +
      /* HTML */ `<details class="accordion" ${p.issuer || p.indexer || p.maturity ? 'open' : ''}>
        <summary><strong>Dados de renda fixa</strong>${icon('down', 15, 'rotate')}</summary>
        <div class="form-grid details-body">
          ${field('issuer', 'Emissor', { value: p.issuer }) + field('indexer', 'Indexador', { value: p.indexer }) + dt('maturity', 'Vencimento', p.maturity, false)}
        </div>
      </details>`,
    'Salvar posição',
  );
}
export function entryForm(existing) {
  if (existing) {
    return form('entry',
      `<input type="hidden" name="external_id" value="${esc(existing.external_id)}">` +
      field('description', 'Descrição', { value: existing.description, required: true }) +
      amountField('amount', 'Valor', existing.amount) +
      dt('date', 'Data do lançamento ou da parcela', existing.date), 'Salvar alterações');
  }
  return form(
    'entry',
    field('description', 'Descrição', { required: true }) +
      amountField('amount', 'Valor') +
      dt('date', 'Data do lançamento ou da parcela', isoToday()) +
      choice('kind', 'Tipo', kinds, 'expense') +
      field('category', 'Categoria') +
      field('merchant', 'Estabelecimento') +
      /* HTML */ `<details class="accordion">
        <summary><strong>Compra parcelada</strong>${icon('down', 15, 'rotate')}</summary>
        <div class="form-grid details-body">
          ${note('Registre cada parcela no mês da respectiva fatura. Use o mesmo grupo em todas as parcelas da compra.') + field('installment_group', 'Nome do grupo de parcelas') + integer('installment_index', 'Número desta parcela', '')}
        </div>
      </details>`,
    'Salvar lançamento',
  );
}
export function correctForm({ entry: e, notice, can_retry: canRetry }) {
  return (
    `<p class="dialog-copy">${esc(e.description)} · ${money(e.amount)}</p>` +
    (notice ? `<p class="note" data-classification-notice role="status">${esc(notice)}</p>` : '') +
    (canRetry ? action('retry-classification', 'Pedir nova análise', 'text-btn', `data-id="${esc(e.id)}"`) : '') +
    form(
      'correct',
      `<input type="hidden" name="entry_id" value="${esc(e.id)}">` +
        choice('kind', 'Tipo', kinds, e.kind, true) +
        field('category', 'Categoria', { value: e.category || '' }) +
        '<label class="check"><input type="checkbox" name="remember" checked>Aplicar a lançamentos semelhantes</label>',
      'Salvar correção',
    )
  );
}
export function profileForm(p) {
  const risk = {
    '': 'Escolha uma opção',
    conservative: 'Conservadora',
    moderate: 'Moderada',
    aggressive: 'Alta',
  };
  return form(
    'perfil',
    field('goal', 'Para que você está investindo?', { value: p.goal, required: true }) +
      field('horizon_months', 'Prazo do objetivo', {
        type: 'number',
        value: p.horizon_months,
        unit: 'meses',
        attr: 'min="1" step="1"',
        required: true,
      }) +
      amountField('reserve', 'Reserva de emergência atual', p.reserve) +
      choice('risk', 'Tolerância a perdas', risk, p.risk, true),
    'Salvar perfil',
  );
}
export function balanceForm(p, c) {
  return form(
    'balance',
    amountField('balance', 'Saldo em conta', p.balance, false, {
      attr: 'step="0.01" inputmode="decimal"',
      help:
        c.balance_source === 'bank'
          ? 'Deixe em branco para usar o saldo do banco.'
          : 'Deixe em branco para usar um saldo bancário quando disponível.',
    }) +
      amountField('commitments', 'Contas até a próxima entrada', p.commitments, false, {
        help: 'Deixe em branco para usar as contas previstas.',
      }) +
      amountField('minimum', 'Saldo mínimo a manter', p.minimum) +
      dt('next_income_date', 'Data da próxima entrada', p.next_income_date),
    'Salvar valores',
  );
}
export function targetsForm(p) {
  const classes = [...new Set([...(p.target_classes || baseClasses), ...Object.keys(p.targets || {})])];
  const inputs = classes
    .map((c, i) =>
      field('target_' + i, c, {
        type: 'number',
        unit: '%',
        value: p.targets?.[c] ?? '',
        attr: `data-class="${esc(c)}" min="0" max="100" step="0.01"`,
      }),
    )
    .join('');
  return (
    note('Distribua 100% entre as classes que deseja manter na carteira.') +
    form('targets', inputs, 'Salvar metas') +
    (p.targets_manual ? form('reset-targets', note('Você também pode voltar à distribuição calculada para seu perfil.'), 'Usar metas do perfil') : '')
  );
}
export function simulateForm(c) {
  return form('simulate', amountField('amount', 'Simular distribuição de', c.simulated), 'Ver distribuição');
}
export function advanceForm(fin, group) {
  const s = fin.spending,
    all = s.installments || [];
  if (!all.length) return note('Não há parcelas futuras disponíveis para antecipar.');
  const groups = [...new Set(all.map((e) => e.installment_group))];
  const selected = groups.includes(group) ? group : groups[0];
  const entries = all.filter((e) => e.installment_group === selected);
  const charges = (s.entries || []).filter(
    (e) => ['expense', 'unknown'].includes(e.kind) && !e.advanced_by && !all.some((p) => p.id === e.id),
  );
  return form(
    'advance',
    choice('installment_group', 'Grupo de parcelas', options(groups), selected) +
      `<fieldset class="fieldset"><legend>Parcelas incluídas na cobrança</legend>${entries.map((e) => /* HTML */ `<label class="check"><input type="checkbox" name="entry_ids" value="${esc(e.id)}" />${esc(e.description)} · ${date(e.date)} · ${money(e.amount)}</label>`).join('')}</fieldset>` +
      dt('date', 'Data da cobrança', isoToday()) +
      amountField('amount', 'Total efetivamente cobrado') +
      choice('charge_entry_id', 'Cobrança correspondente na fatura', {
        '': 'Criar lançamento da cobrança',
        ...Object.fromEntries(charges.map((e) => [e.id, escUnformatted(e)])),
      }),
    'Registrar antecipação',
  );
}
export function linkChargeForm(a, entries) {
  const matches = matchingCharges(a, entries);
  return (
    note(esc(a.description) + ' · ' + date(a.date) + ' · ' + money(a.amount)) +
    form(
      'link-charge',
      `<input type="hidden" name="advance_id" value="${esc(a.id)}">` +
        choice(
          'charge_entry_id',
          'Lançamento que corresponde à cobrança',
          Object.fromEntries(matches.map((e) => [e.id, escUnformatted(e)])),
          String(matches[0]?.id || ''),
          true,
        ),
      'Vincular cobrança',
    )
  );
}
function escUnformatted(e) {
  return `${e.description} · ${date(e.date)} · ${money(e.amount)}`;
}
export function importForm(conn, kind) {
  const cols = conn.csv_columns?.[kind] || [];
  return form(
    'import',
    choice(
      'kind',
      'Conteúdo do arquivo',
      { positions: 'Posições de investimento', entries: 'Lançamentos' },
      kind,
    ) +
      field('file', 'Arquivo CSV', { type: 'file', attr: 'accept=".csv,text/csv"', required: true }) +
      (cols.length
        ? /* HTML */ `<details class="accordion" open>
            <summary><strong>Formato do CSV</strong>${icon('down', 15, 'rotate')}</summary>
            <div class="details-body">
              <p class="note">Cabeçalho do arquivo</p>
              <p class="dialog-copy">${esc(cols.join(', '))}</p>
              ${action('csv-model', 'Baixar cabeçalho CSV', 'text-btn', `data-kind="${esc(kind)}"`)}
            </div>
          </details>`
        : ''),
    'Importar arquivo',
  );
}
export function connections(conn) {
  const fmtItem = (i) =>
    /* HTML */ `<article class="asset">
      <h3>${esc(i.name || i.institution || 'Conexão bancária')}</h3>
      <p class="note">
        ${esc(i.status || 'Sem status')}${i.updated_at ? ' · ' + date(i.updated_at, { dateStyle: 'short', timeStyle: 'short' }) : ''}
      </p>
      <small class="note">ID ${esc(i.id)}</small>
    </article>`;
  return /* HTML */ `<div class="stack">
    <section>
      ${conn.items?.length ? conn.items.map(fmtItem).join('') : note('Nenhuma instituição conectada.')}${conn.pluggy_configured ? action('connect', 'Conectar instituição', 'primary') : note('A conexão com a Pluggy ainda precisa ser configurada no servidor.')}${
        conn.pluggy_configured
          ? /* HTML */ `<details class="accordion table-foot">
              <summary><strong>Registrar conexão já existente</strong>${icon('down', 15, 'rotate')}</summary>
              <div class="details-body">
                ${form('connect-id', field('item_id', 'ID da conexão na Pluggy', { required: true }), 'Registrar conexão')}
              </div>
            </details>`
          : ''
      }
    </section>
    <section>
      <h3>Importar por arquivo</h3>
      <div class="actions">
        ${action('import-positions', 'Investimentos') + action('import-entries', 'Lançamentos')}
      </div>
    </section>
  </div>`;
}
