(() => {
  'use strict';
  const $ = id => document.getElementById(id), panel = $('investment-panel');
  if (!panel) return;
  const states = {available:'Fonte sincronizada',partial:'Cobertura parcial',stale:'Coleta desatualizada',disabled:'Pluggy desativado',unavailable:'Dados indisponíveis',awaiting_authorization:'Aguardando dados autorizados'};
  const types = {FIXED_INCOME:'Renda fixa',MUTUAL_FUND:'Fundo de investimento',EQUITY:'Renda variável',ETF:'ETF',COE:'COE',SECURITY:'Título',OTHER:'Outros'};
  const stamp = value => {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? 'N/D' : new Intl.DateTimeFormat('pt-BR',{timeZone:'America/Sao_Paulo',day:'2-digit',month:'2-digit',year:'numeric',hour:'2-digit',minute:'2-digit'}).format(date);
  };
  const node = (tag,text,className) => { const el = document.createElement(tag); if (text !== undefined) el.textContent = text; if (className) el.className = className; return el; };
  const note = text => node('p',text,'investment-note');
  let signature = '';
  function update(p) {
    if (!p || typeof p.state !== 'string') return;
    const next = JSON.stringify(p); if (next === signature) return; signature = next;
    panel.dataset.state = p.state;
    $('investment-state').textContent = states[p.state] || states.unavailable;
    const totals = [];
    for (const total of p.totals || []) {
      const dl = node('dl');
      dl.append(node('dt','Posições ativas · ' + total.currency + (total.count !== total.valued_count ? ' · valor parcial' : '')),node('dd',total.balance_text || 'N/D'));
      totals.push(dl);
    }
    if (!totals.length) totals.push(note(['available','partial','stale'].includes(p.state) ? 'Nenhuma posição ativa reportada nesta coleta.' : 'Consulte a autorização e a coleta em Conexões.'));
    $('investment-totals').replaceChildren(...totals);
    const active = p.active_count || 0, withdrawn = p.withdrawn_count || 0;
    const coverage = [active + (active === 1 ? ' ativa' : ' ativas'),withdrawn + (withdrawn === 1 ? ' resgatada' : ' resgatadas')];
    if (p.unknown_count) coverage.push(p.unknown_count + ' com estado desconhecido');
    if (p.missing_value_count) coverage.push(p.missing_value_count + ' sem valor ou moeda');
    if (p.withheld_count) coverage.push(p.withheld_count + ' fora do consentimento atual');
    $('investment-coverage').textContent = coverage.join(' · ');
    $('investment-stamp').textContent = p.last_synced_at ? 'Última coleta: ' + stamp(p.last_synced_at) + '. Fonte mais antiga: ' + stamp(p.oldest_synced_at) + '.' : 'A fonte ainda não tem uma coleta disponível.';
    const positions = [];
    for (const position of p.positions || []) {
      const li = node('li'), heading = node('div',undefined,'investment-position-heading');
      heading.append(node('h3',position.name || 'Investimento sem nome'),node('span',position.status === 'ACTIVE' ? 'Ativo' : position.status === 'TOTAL_WITHDRAWAL' ? 'Resgatado' : 'Estado não confirmado'));
      li.append(heading,node('p',position.balance_text || 'N/D','investment-position-value'),note((types[position.type] || position.type || 'Tipo não informado') + ' · ' + (position.currency || 'N/D')));
      if (position.issuer) li.append(note('Emissor: ' + position.issuer));
      if (position.maturity) li.append(note('Vencimento informado: ' + position.maturity));
      li.append(note(position.source_updated_at ? 'Atualização na fonte: ' + stamp(position.source_updated_at) : 'Coletado em: ' + stamp(position.observed_at)));
      positions.push(li);
    }
    if (!positions.length) positions.push(node('li','Nenhuma posição disponível para detalhar.','investment-note'));
    $('investment-positions').replaceChildren(...positions);
    $('investment-limit').textContent = p.omitted ? p.omitted + ' posições não exibidas. Os totais consideram todas as posições ativas autorizadas.' : '';
  }
  window.addEventListener('finance-summary',event => update(event.detail?.investments));
  window.addEventListener('finance-connection',event => {
    const state = event.detail?.state;
    $('investment-update-state').textContent = state === 'live' ? 'Painel atualizado automaticamente.' : state === 'paused' ? 'Atualização pausada nesta aba.' : state === 'stale' ? 'Atualização interrompida. Os valores exibidos são da última consulta.' : 'Conectando a atualização do painel…';
  });
})();
