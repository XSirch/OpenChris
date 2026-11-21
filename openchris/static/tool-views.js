import {
  esc,
  money,
  date,
  icon,
  header,
  action,
  link,
  amountField,
  field,
  buttonSubmit,
  choice,
  ref,
  empty,
  note,
  row,
} from './ui.js';

const calculatorModes = {
  compound: 'Juros compostos',
  time: 'Tempo até a meta',
  monthly: 'Aporte necessário',
};
export function calculators() {
  const amountOptions = { type: 'text', attr: 'inputmode="decimal"' };
  const conditional = (mode, content) =>
    `<fieldset class="calc-field" data-hide-in="${mode}" ${mode === 'compound' ? 'hidden disabled' : ''}>${content}</fieldset>`;
  return (
    header('Calculadoras', '', true) +
    /* HTML */ `<section class="narrow">
      <form data-form="calc">
        <fieldset class="calc-modes">
          <legend class="sr">Escolha uma calculadora</legend>
          ${Object.entries(calculatorModes)
            .map(
              ([id, label]) =>
                `<label><input class="sr" type="radio" name="mode" value="${id}" ${id === 'compound' ? 'checked' : ''}>${label}</label>`,
            )
            .join('')}
        </fieldset>
        <div class="form-grid paired">
          ${amountField('principal', 'Valor inicial (opcional)', '', false, amountOptions)}
          ${conditional('monthly', amountField('monthly', 'Aporte mensal (opcional)', '', false, amountOptions))}
          <div class="calc-rate full">
            ${field('rate', 'Taxa de juros', { unit: '%', attr: 'inputmode="decimal"', required: true })}
            ${choice('rate_period', 'Período da taxa', { month: 'Ao mês', year: 'Ao ano' }, 'month')}
          </div>
          ${conditional('time', field('months', 'Prazo', {
            type: 'number',
            unit: 'meses',
            attr: 'step="1" min="1" max="1200" inputmode="numeric"',
            required: true,
          }))}
          ${conditional('compound', amountField('target', 'Valor da meta', '', true, amountOptions))}
        </div>
        <div class="actions calc-actions">
          <button type="submit" class="primary">Calcular</button>
          ${action('clear-calculator', 'Limpar', 'secondary')}
        </div>
        <div id="calc-result" role="status" class="result"></div>
      </form>
    </section>`
  );
}
function duration(totalMonths) {
  if (totalMonths === 0) return 'Meta já atingida';
  const years = Math.floor(totalMonths / 12);
  const months = totalMonths % 12;
  return [
    years ? years + (years === 1 ? ' ano' : ' anos') : '',
    months ? months + (months === 1 ? ' mês' : ' meses') : '',
  ].filter(Boolean).join(' e ');
}

export function calcResult(mode, period, result) {
  const label = {
    compound: 'Valor final estimado',
    time: 'Tempo estimado',
    monthly: 'Aporte mensal necessário',
  }[mode];
  const rate = Number(result.equivalent_rate);
  const equivalent = rate.toLocaleString('pt-BR', { maximumFractionDigits: Math.abs(rate) < 0.0001 ? 10 : 4 });
  return /* HTML */ `<section class="inset calc-summary">
    <p class="metric-label">${label}</p>
    <p class="value">
      ${result.unit === 'months' ? duration(result.value) : money(result.value)}
    </p>
    <div class="rows">
      ${mode === 'compound' ? row('Total aplicado', money(result.invested)) + row('Rendimento estimado', money(result.earnings)) : ''}
      ${row('Taxa equivalente', `${equivalent}% ao ${period === 'month' ? 'ano' : 'mês'}`)}
    </div>
    ${note('Projeção bruta, com aportes no fim de cada mês.')}
  </section>`;
}
function eventItem(e, active) {
  const a = e.analysis,
    phase =
      { scheduled: /Copom/i.test(e.title) ? 'Antes da decisão' : 'Divulgação prevista', published: 'Fato publicado', awaiting: 'Aguardando publicação' }[
        e.phase
      ] || 'Evento';
  return /* HTML */ `<article class="event" id="evento-${esc(e.id)}">
    <p class="note">${phase} · ${date(e.date)}</p>
    <h2>${esc(e.title)}</h2>
    ${a?.conclusion ? `<p class="fact"><strong>${esc(a.conclusion)}</strong></p>` : ''}${e.fact ? `<p class="fact"><strong>Fato publicado</strong> · ${esc(e.fact)}</p>` : !a?.conclusion ? note('O resultado ainda não foi publicado.') : ''}${a?.impact ? `<p class="notes">${esc(a.impact)}</p>` : ''}${
      a?.scenarios?.length
        ? `<h3 class="table-foot">Cenários</h3>${a.scenarios
            .map(
              (s) =>
                /* HTML */ `<div class="scenario">
                  ${icon('arrow')}
                  <p>${esc(s)}</p>
                </div>`,
            )
            .join('')}`
        : ''
    }${
      a
        ? /* HTML */ `<details class="accordion" ${active ? 'open' : ''}>
            <summary><strong>Debate dos especialistas</strong>${icon('down', 17, 'rotate')}</summary>
            <div class="details-body stack tight">
              ${(a.arguments || [])
                .map(
                  (s) =>
                    /* HTML */ `<div>
                      <h4>${esc(s.specialist)}</h4>
                      <p>${esc(s.argument)}</p>
                      ${s.source_url ? ref(s.source_url, 'Consultar evidência') : ''}
                    </div>`,
                )
                .join('')}${
                a.divergence
                  ? /* HTML */ `<div>
                      <h4>Divergências</h4>
                      <p>${esc(a.divergence)}</p>
                    </div>`
                  : ''
              }${!a.arguments?.length && !a.divergence ? note('Os argumentos individuais ainda não estão disponíveis.') : ''}
            </div>
          </details>`
        : ''
    }${
      e.previous_analysis?.conclusion
        ? /* HTML */ `<details class="accordion">
            <summary><strong>Análise anterior à divulgação</strong>${icon('down', 16, 'rotate')}</summary>
            <div class="details-body">
              <p>${esc(e.previous_analysis.conclusion)}</p>
              ${(e.previous_analysis.scenarios || []).map((s) => `<p class="scenario">${esc(s)}</p>`).join('')}
            </div>
          </details>`
        : ''
    }
    <div class="sources">
      ${e.source_url ? `<span>Fonte: ${ref(e.source_url)}</span>` : ''}${e.updated_at ? `<span>Atualizado em ${date(e.updated_at)}</span>` : ''}
    </div>
  </article>`;
}
export function economy(data, state) {
  const selected = String(state.event || ''),
    analyses = state.ecoTab === 'analises';
  const events = (data.events || []).filter((e) => !analyses || e.analysis);
  const order = (a, b) => {
    if (a.id === b.id) return 0;
    if (String(a.id) === selected) return -1;
    if (String(b.id) === selected) return 1;
    if (analyses) return (b.updated_at || b.date).localeCompare(a.updated_at || a.date);
    const upcoming = a.phase === 'scheduled',
      otherUpcoming = b.phase === 'scheduled';
    if (upcoming !== otherUpcoming) return upcoming ? -1 : 1;
    return upcoming ? a.date.localeCompare(b.date) : b.date.localeCompare(a.date);
  };
  const sorted = [...events].sort(order);
  const sources = (data.sources || [])
    .map(
      (s) =>
        /* HTML */ `<div class="asset">
          ${ref(s.url, s.name)}${s.checked_at ? note('Consultada em ' + date(s.checked_at, { dateStyle: 'short', timeStyle: 'short' })) : ''}${s.error ? note('Falha na última consulta.') : ''}
        </div>`,
    )
    .join('');
  return (
    header('Economia', '', true) +
    /* HTML */ `<div class="columns">
      <div>
        <div class="tabs" role="group" aria-label="Visualização de economia">
          ${['agenda', 'analises'].map((key) => action('eco-tab', key === 'agenda' ? 'Agenda' : 'Análises', '', `data-id="${key}" aria-pressed="${state.ecoTab === key}"`)).join('')}
        </div>
        ${selected && !data.events?.some((e) => String(e.id) === selected) ? note('O evento solicitado não está disponível.') : ''}${
          sorted.length
            ? sorted.map((e) => eventItem(e, String(e.id) === selected)).join('')
            : /* HTML */ `<section>
                <h2 class="section-title">
                  ${analyses ? 'Nenhuma análise publicada' : 'Agenda sem eventos'}
                </h2>
                ${empty(analyses ? 'As análises aparecerão após a pesquisa das fontes.' : 'Os eventos aparecerão após a próxima coleta de fontes econômicas.')}
              </section>`
        }
      </div>
      <section>
        <h2 class="section-title">Fontes acompanhadas</h2>
        ${sources || empty('Ainda não há registro de consulta às fontes.')}
      </section>
    </div>`
  );
}
const tools = [
  ['calculadoras', 'Calculadoras', 'Juros compostos, prazo e aporte mensal.'],
  ['economia', 'Economia', 'Agenda, cenários e análises.'],
  ['perfil', 'Perfil financeiro', 'Objetivos, reserva e tolerância a perdas.'],
  ['banco', 'Conexões e extratos', 'Bancos conectados e importação de arquivos.'],
  ['pesquisa', 'Agentes e pesquisas', 'Modelos, esforço de raciocínio e fila de tarefas.'],
  ['sair', 'Sair da conta', ''],
];
export function more() {
  return (
    header('Mais') +
    /* HTML */ `<div class="tools-list narrow">
      ${tools
        .map(([key, title, subtitle], i) => {
          const content =
            icon(key, 24) +
            `<span class="tool-title">${title}${subtitle ? `<small>${subtitle}</small>` : ''}</span>` +
            icon('right', 16);
          return i < 2 ? link(key, content, 'row-link') : action(key, content, 'row-link');
        })
        .join('')}
    </div>`
  );
}
const efforts = { none: 'Nenhum', minimal: 'Mínimo', low: 'Baixo', medium: 'Médio', high: 'Alto',
  xhigh: 'Muito alto', max: 'Máximo', ultra: 'Ultra' };
const effortLabel = (value) => efforts[value] || value;

export function effortChoice(models, model, selected = '') {
  const info = models[model];
  const options = { '': info?.default ? 'Padrão do modelo (' + effortLabel(info.default) + ')' : 'Padrão do modelo' };
  for (const level of info?.efforts || []) options[level] = effortLabel(level);
  if (selected && !Object.hasOwn(options, selected)) options[selected] = effortLabel(selected) + ' (indisponível)';
  return choice('effort', 'Esforço de raciocínio', options, selected);
}

export function agentSettings(data, kind) {
  const { model = '', effort = '' } = data.preferences[kind] || {};
  const models = { '': 'Padrão do Codex', ...Object.fromEntries(Object.keys(data.models).map((id) => [id, id])) };
  if (model && !Object.hasOwn(data.models, model)) models[model] = model + ' (indisponível)';
  return `<form data-form="agent-settings" data-kind="${esc(kind)}">
    <div class="form-grid">
      ${choice('model', 'Modelo', models, model)}
      <div data-effort-choice>${effortChoice(data.models, model, effort)}</div>
      ${note('A escolha vale a partir da próxima tarefa deste agente.')}
    </div>
    ${buttonSubmit('Salvar escolha')}
    ${action('pesquisa', 'Voltar aos agentes')}
  </form>`;
}

export function researchers(data) {
  const queue = data.queue || {};
  const agents = Object.entries(data.roles).map(([kind, label]) => {
    const { model, effort } = data.preferences[kind] || {};
    const setting = model ? model + ' · ' + (effort ? effortLabel(effort) : 'Esforço padrão') : 'Padrão do Codex';
    return action('agent-settings', `<span class="tool-title">${esc(label)}<small>${esc(setting)}</small></span>${icon('right', 16)}`,
      'row-link', `data-id="${esc(kind)}"`);
  }).join('');
  return /* HTML */ `<div class="stack tight">
    <h3>Modelo por agente</h3>
    ${Object.keys(data.models).length ? '' : note('Aguardando a lista de modelos do Codex.')}
    <div class="tools-list">${agents}</div>
    <h3>Fila de tarefas</h3>
    ${note(data.enabled ? 'Rotinas automáticas habilitadas.' : 'As rotinas automáticas estão desativadas.')}${data.notice ? `<p>${esc(data.notice)}</p>` : ''}
    <div class="rows">
      ${row('Pendentes', Number(queue.pending || 0))}${row('Em execução', Number(queue.running || 0))}${row('Aguardando nova tentativa', Number(queue.retry || 0))}${queue.uncertain ? row('Envios sem confirmação', Number(queue.uncertain)) : ''}${row('Concluídas', Number(queue.done || 0))}
    </div>
    ${note(data.last_success ? 'Última conclusão em ' + date(data.last_success, { dateStyle: 'short', timeStyle: 'short' }) + '.' : 'Nenhuma tarefa concluída até agora.')}
  </div>`;
}
export function assetSheet(data, position) {
  const labels = {
    verified: 'Dados confirmados',
    partial: 'Confirmação parcial',
    unavailable: 'Sem confirmação',
    pending: 'Pesquisa pendente',
    conflict: 'Fontes divergentes',
  };
  const names = {
    value: 'Valor',
    currency: 'Moeda',
    issuer: 'Emissor',
    indexer: 'Indexador',
    maturity: 'Vencimento',
    asset_class: 'Classe',
    institution: 'Instituição',
    name: 'Nome',
    identifier: 'Identificador',
  };
  const conflicts = Object.entries(position?.conflicts || {})
    .map(
      ([key, c]) =>
        /* HTML */ `<div class="inset">
          <strong>${esc(names[key] || key)} a confirmar</strong>
          <p>Na posição: ${esc(position[key])}. Na fonte: ${esc(c.value)}.</p>
          ${ref(c.source, 'Origem da divergência')}
        </div>`,
    )
    .join('');
  const fields = (data.fields || [])
    .map(
      (f) =>
        /* HTML */ `<article class="asset">
          <h3>${esc(names[f.field] || f.field)}</h3>
          <p>${esc(f.value)}</p>
          ${f.quote ? `<p class="note">“${esc(f.quote)}”</p>` : ''}
          <div class="sources">
            ${f.source_url ? ref(f.source_url, 'Documento de origem') : ''}${f.reference_date ? 'Referência: ' + date(f.reference_date) : ''}${f.checked_at ? 'Consulta: ' + date(f.checked_at) : ''}
          </div>
        </article>`,
    )
    .join('');
  const provenance = Object.entries(position?.field_sources || {})
    .map(([key, value]) =>
      row(
        esc(names[key] || key),
        esc(typeof value === 'object' ? value.source || value.origin || 'Registro da posição' : value),
      ),
    )
    .join('');
  return /* HTML */ `<div class="stack tight">
    <div>
      <h3>${esc(position?.name || data.identifier)}</h3>
      ${note(esc(labels[data.status] || data.status))}
    </div>
    ${data.note ? `<p>${esc(data.note)}</p>` : ''}${conflicts}${fields ? '<section>' + fields + '</section>' : empty('Nenhum campo com evidência documental disponível.')}${
      provenance
        ? /* HTML */ `<details class="accordion">
            <summary><strong>Origem dos campos da posição</strong>${icon('down', 15, 'rotate')}</summary>
            ${provenance}
          </details>`
        : ''
    }${data.checked_at ? note('Última pesquisa em ' + date(data.checked_at)) : ''}
  </div>`;
}
