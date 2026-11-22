import { date, esc, has, money, note } from './ui.js?v=carbono-4';

const references = {
  cdi: ['CDI', '#87cfc1', [9, 5]],
  ipca: ['IPCA', '#ffb39d', [3, 5]],
  ibov: ['Ibovespa', '#d1aff6', [12, 4, 3, 4]],
};

export const performancePanel = () => `
  <section class="panel performance" aria-labelledby="performance-title">
    <div class="performance-head">
      <h2 id="performance-title">Desempenho</h2>
      <div class="portfolio-modes" role="group" aria-label="Visão de desempenho">
        <button type="button" data-performance-mode="returns" aria-pressed="true">Rentabilidade</button>
        <button type="button" data-performance-mode="value" aria-pressed="false">Patrimônio</button>
      </div>
    </div>
    <div id="performance-data"></div>
  </section>`;

const shortDay = (value) => date(value, { day: '2-digit', month: 'short' });
const percent = (value) => Number(value).toLocaleString('pt-BR', { maximumFractionDigits: 2 }) + '%';
const range = (start, end) => `${shortDay(start)} a ${shortDay(end)}`;
const lastPoint = (series) => series.at(-1);

export function balanceTrend(performance) {
  const observed = (performance?.observations || []).filter(point => has(point.value) && Number.isFinite(Number(point.value)));
  const monthly = [...new Map(observed.map(point => [point.date.slice(0, 7), point])).values()];
  const points = (monthly.length > 1 ? monthly : observed).slice(-6);
  if (points.length < 2) return '';
  const values = points.map(point => Number(point.value));
  const min = Math.min(...values), max = Math.max(...values);
  const coords = values.map((value, index) => [
    3 + index * 114 / (values.length - 1),
    max === min ? 22 : 38 - (value - min) * 32 / (max - min),
  ]);
  const [lastX, lastY] = coords.at(-1);
  const from = shortDay(points[0].date), through = shortDay(points.at(-1).date);
  const label = performance.partial ? 'Saldo observado nas conexões' : 'Saldo observado';
  const description = `${label} de ${from} a ${through}, de ${money(values[0])} a ${money(values.at(-1))}.`;
  return `<div class="balance-trend">
    <svg viewBox="0 0 120 44" preserveAspectRatio="none" role="img" aria-label="${esc(description)}">
      <defs><linearGradient id="balance-shade" x2="0" y2="1"><stop stop-color="#33494e" stop-opacity=".23"/><stop offset="1" stop-color="#33494e" stop-opacity="0"/></linearGradient></defs>
      <polygon points="${coords.map(pair => pair.join(',')).join(' ')} ${lastX},44 3,44" fill="url(#balance-shade)"/>
      <polyline points="${coords.map(pair => pair.join(',')).join(' ')}" fill="none" stroke="#30464b"
        stroke-width="2" vector-effect="non-scaling-stroke" stroke-linecap="round" stroke-linejoin="round"/>
      <circle cx="${lastX}" cy="${lastY}" r="2.8" fill="#263e43"/>
    </svg>
    <span class="trend-caption" aria-hidden="true"><span>${from}</span><span>${through}</span></span>
    <span aria-hidden="true">${label}</span>
  </div>`;
}

function dataChart(target, traces, format) {
  if (!traces.some(({ points }) => points.length > 1)) return;
  const canvas = target.querySelector('canvas');
  canvas.hidden = false;
  const ctx = canvas.getContext('2d');
  if (!ctx) return;
  const plot = { left: 16, right: 624, top: 16, bottom: 144 };
  const all = traces.flatMap(({ points }) => points);
  const dates = all.map(({ date }) => Date.parse(date));
  const first = Math.min(...dates);
  const span = Math.max(...dates) - first || 1;
  const low = Math.min(...all.map(({ value }) => Number(value)));
  const high = Math.max(...all.map(({ value }) => Number(value)));
  const extent = high - low || 1;
  const y = (value) => plot.bottom - (Number(value) - low) / extent * (plot.bottom - plot.top);
  ctx.strokeStyle = '#384350';
  ctx.lineWidth = 1;
  for (const height of [plot.top, (plot.top + plot.bottom) / 2, plot.bottom]) {
    ctx.beginPath();
    ctx.moveTo(plot.left, height);
    ctx.lineTo(plot.right, height);
    ctx.stroke();
  }
  traces.forEach(({ points, color, dash }, index) => {
    if (points.length < 2) return;
    ctx.lineWidth = index ? 2 : 3;
    ctx.strokeStyle = color;
    ctx.setLineDash(dash);
    ctx.beginPath();
    points.forEach((point, i) => {
      const x = plot.left + (Date.parse(point.date) - first) / span * (plot.right - plot.left);
      if (i) ctx.lineTo(x, y(point.value));
      else ctx.moveTo(x, y(point.value));
    });
    ctx.stroke();
  });
  const values = target.querySelector('[data-performance-values]');
  values.hidden = false;
  values.querySelector('div').innerHTML = traces.map(({ name, points }) => {
    const byMonth = new Map(points.map((point) => [point.date.slice(0, 7), point]));
    return `<section><h4>${name}</h4><dl>${[...byMonth.values()].map(point =>
      `<div><dt>${date(point.date)}</dt><dd>${format(point.value)}</dd></div>`,
    ).join('')}</dl></section>`;
  }).join('');
}

function referenceRows(benchmarks, hasReturn) {
  return Object.entries(references).map(([key, [name, color]]) => {
    const benchmark = benchmarks[key];
    const difference = Number(benchmark?.difference);
    const label = has(benchmark?.difference)
      ? difference === 0 ? 'Carteira no mesmo nível' : `Carteira ${Math.abs(difference).toLocaleString('pt-BR')} p.p. ${difference > 0 ? 'acima' : 'abaixo'}`
      : '';
    return `<div class="benchmark-row">
      <div><span class="benchmark-key" style="--series:${color}" aria-hidden="true"></span>
      ${benchmark ? `<a href="${esc(benchmark.source)}" target="_blank" rel="noopener noreferrer">${name}</a>` : name}
      <small>${benchmark ? range(benchmark.start, benchmark.end) : 'Aguardando dados'}${benchmark?.delayed ? ' · Última leitura' : ''}</small></div>
      <div><strong class="numeric">${benchmark ? percent(benchmark.percent) : '—'}</strong>
      ${label ? `<small>${label}</small>` : hasReturn && benchmark ? '<small>Período de comparação diferente</small>' : ''}</div>
    </div>`;
  }).join('');
}

export function mountPerformance(root, data) {
  const target = root.querySelector('#performance-data');
  if (!target) return;
  const controls = [...root.querySelectorAll('[data-performance-mode]')];
  if (!data) {
    controls.forEach(button => { button.disabled = true; });
    target.innerHTML = note('O desempenho está indisponível agora. Atualize a página para tentar novamente.');
    return;
  }
  const scope = data.institutions.map(esc).join(', ');
  const chart = `<canvas class="performance-chart" width="640" height="160" hidden aria-hidden="true"></canvas>
    <details class="performance-values" data-performance-values hidden><summary>Ver valores por mês</summary><div></div></details>`;
  function render(mode) {
    controls.forEach(button => button.setAttribute('aria-pressed', button.dataset.performanceMode === mode));
    const values = mode === 'value';
    const balancePoints = data.observations.filter(point => has(point.value));
    const wallet = lastPoint(data.return_points);
    const through = data.end && range(data.start, data.end);
    const subtitle = data.start?.startsWith(String(data.year - 1) + '-12-31')
      ? 'Rentabilidade no ano'
      : data.start ? 'Rentabilidade desde ' + shortDay(data.start) : 'Rentabilidade em formação';
    const coverage = scope ? note(`${data.partial ? 'Cobertura parcial · ' : 'Cobertura · '}${scope}`) : '';
    if (values) {
      const latest = lastPoint(balancePoints);
      target.innerHTML = latest ? `
        <div class="performance-total"><p>Último saldo em reais · ${date(latest.date)}</p>
          <strong class="numeric">${money(latest.value)}</strong></div>
        ${coverage}${chart}
        ${balancePoints.length === 1 ? note('A evolução aparece após o próximo saldo observado.') : ''}
        ${has(data.net_flows) ? `<dl class="performance-moves">
          <div><dt>Variação do saldo · ${through}</dt><dd class="numeric">${money(data.balance_change)}</dd></div>
          <div><dt>Aportes líquidos</dt><dd class="numeric">${money(data.net_flows)}</dd></div>
          <div><dt>Resultado dos investimentos</dt><dd class="numeric">${money(data.investment_result)}</dd></div>
          <div><dt>Proventos incluídos no resultado</dt><dd class="numeric">${money(data.income)}</dd></div>
        </dl>` : note('Aportes e rendimentos aparecem quando as movimentações estiverem conciliadas.')}`
        : note('O primeiro saldo aparece após uma sincronização com dados de investimentos.');
      dataChart(target, [{ name: 'Patrimônio', color: '#9db8ff', dash: [], points: balancePoints }], money);
      return;
    }
    target.innerHTML = `
      <div class="performance-total"><p>${subtitle}</p>
        ${wallet ? `<strong class="numeric">${percent(wallet.percent)}</strong><small>${through}</small>` : ''}</div>
      ${wallet ? coverage : note('A comparação da carteira começa com dois saldos e as movimentações conciliadas entre eles.') + coverage}
      ${data.estimated ? note('Rentabilidade estimada com os saldos da última atualização de cada instituição. As posições podem ter datas de avaliação diferentes.') : ''}
      ${data.stale ? note('Último período conciliado. Há leituras mais recentes ainda sem conciliação.') : ''}
      ${chart}
      <div class="performance-references">
        <h3>${wallet ? 'Índices de referência' : 'Referências do ano até a última publicação'}</h3>
        ${referenceRows(data.benchmarks, Boolean(wallet))}
      </div>`;
    if (wallet) {
      const traces = [{ name: 'Carteira', color: '#9db8ff', dash: [], points: data.return_points }];
      for (const [key, [name, color, dash]] of Object.entries(references)) {
        const benchmark = data.benchmarks[key];
        if (benchmark?.start === data.start && benchmark.points.length > 1)
          traces.push({ name, color, dash, points: benchmark.points });
      }
      dataChart(target, traces.map(trace => ({ ...trace, points: trace.points.map(point =>
        ({ date: point.date, value: point.percent })) })), percent);
    }
  }
  controls.forEach(button => button.addEventListener('click', () => render(button.dataset.performanceMode)));
  render('returns');
}
