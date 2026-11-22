const telas = (() => {
  'use strict';
  const m = modelo;
  const reais = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL', maximumFractionDigits: 0, minimumFractionDigits: 0 });
  const fracionado = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' });
  const pct = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 1 });
  const nomes = { hoje: 'Hoje', carteira: 'Carteira', gastos: 'Gastos', aportes: 'Aportes', mais: 'Mais',
    economia: 'Economia', calculadoras: 'Calculadoras', sistema: 'Sistema visual' };
  const principal = { hoje: 'home', carteira: 'wallet', gastos: 'receipt', aportes: 'aporte', mais: 'more' };
  const modos = { juros: 'Juros compostos', prazo: 'Tempo até a meta', mensal: 'Aporte necessário' };
  const esc = texto => String(texto).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  const moeda = n => n === null ? 'A informar' : (Number.isInteger(n) ? reais : fracionado).format(n);
  const percentual = n => pct.format(n) + '%';
  const ic = nome => `<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-${nome}"></use></svg>`;
  const tile = (icone, classe = '') => `<span class="tile ${classe}">${ic(icone)}</span>`;
  const valorGrande = n => `<span class="amount"><span class="currency">R$</span> ${moeda(n).replace(/^R\$\s*/, '')}</span>`;
  const titulo = (nome, descricao = '') => `<div class="page-head"><div><h1 tabindex="-1">${nome}</h1>${descricao ? `<p>${descricao}</p>` : ''}</div></div>`;
  const link = (rota, texto) => `<a class="text-link" href="#${rota}">${texto}${ic('arrow')}</a>`;
  const voltar = (rota, texto) => `<a class="back-link" href="#${rota}">${ic('back')}${texto}</a>`;
  const cabecalho = (nome, acao = '') => `<div class="panel-head"><h2>${nome}</h2>${acao}</div>`;
  const linha = (rotulo, valor) => `<div class="row-data"><dt>${rotulo}</dt><dd>${valor}</dd></div>`;
  const detalhe = (nome, conteudo) => `<details class="breakdown"><summary>${nome}${ic('down')}</summary>${conteudo}</details>`;
  const campo = (nome, label, attrs = '') => `<label class="field"><span>${label}</span><input name="${nome}" inputmode="${nome === 'meses' ? 'numeric' : 'decimal'}" autocomplete="off" ${attrs}></label>`;
  const opcoes = (items, atual) => items.map(([id, nome]) => `<option value="${id}"${String(id) === String(atual) ? ' selected' : ''}>${nome}</option>`).join('');
  const dataMes = (dia, indice) => `${dia} de ${m.meses[indice][1].toLocaleLowerCase('pt-BR')}`;

  function navegacao(ativa) {
    return Object.entries(principal).map(([rota, icone]) => `<a class="nav-link" href="#${rota}"${ativa === rota ? ' aria-current="page"' : ''}>
      <span class="nav-icon-wrap">${ic(icone)}</span>${nomes[rota]}</a>`).join('');
  }

  function curva() {
    const pontos = m.historico.map((item, i) => [3 + i * 38, 48 - (item[1] - 165000) / 34000 * 44]);
    const traco = pontos.map(([x, y], i) => `${i ? 'L' : 'M'}${x} ${y.toFixed(2)}`).join(' ');
    const [x, y] = pontos.at(-1);
    return `<div class="mini-history">
      <svg viewBox="0 0 199 52" preserveAspectRatio="none" role="img" aria-label="Patrimônio investido de junho a novembro, de ${moeda(m.historico[0][1])} a ${moeda(m.patrimonio)}.">
        <defs><linearGradient id="area-historico" x1="0" y1="0" x2="0" y2="1" gradientUnits="objectBoundingBox"><stop stop-color="#33494e" stop-opacity=".23"/><stop offset="1" stop-color="#33494e" stop-opacity="0"/></linearGradient></defs>
        <path d="${traco} L${x} 52 L3 52 Z" fill="url(#area-historico)"/>
        <path d="${traco}" fill="none" stroke="#30464b" stroke-width="2" vector-effect="non-scaling-stroke" stroke-linejoin="round" stroke-linecap="round"/>
        <circle cx="${x}" cy="${y.toFixed(2)}" r="3" fill="#263e43"/>
      </svg><div class="chart-range" aria-hidden="true"><span>Jun</span><span>Nov</span></div></div>`;
  }

  function saldo(fluxo) {
    const anterior = m.historico.at(-2)[1];
    const mudanca = (m.patrimonio / anterior - 1) * 100;
    return `<section class="wealth titanio" aria-label="Resumo financeiro">
      <a class="wealth-link" href="#carteira">
        <div class="wealth-top"><span>${ic('wallet')}Patrimônio investido</span>${ic('forth')}</div>
        <div class="wealth-middle"><div>${valorGrande(m.patrimonio)}
          <div class="wealth-trend">${ic('up-right')}+${percentual(mudanca)} desde outubro</div></div>${curva()}</div>
      </a>
      <div class="metrics-pair">
        <a class="metric" href="#gastos"><span>Gastos do mês</span><strong>${moeda(m.resumoGastos(4).total)}</strong></a>
        <a class="metric" href="#aportes"><span>Aporte disponível</span><strong>${moeda(fluxo.disponivel)}</strong></a>
      </div></section>`;
  }

  function movimentos(items, indice) {
    if (!items.length) return '<p class="empty">Nenhum lançamento neste período.</p>';
    return items.map(l => `<button type="button" class="money-row" data-dialog="lancamento" data-id="${l.id}" data-mes="${indice}">
      ${tile(l.icone, l.categoria)}<span class="row-main"><strong>${l.nome}</strong><span class="sub">${dataMes(l.dia, indice)} · ${m.categorias[l.categoria]}</span></span>
      <span class="amount-end">−${moeda(l.valor)}</span></button>`).join('');
  }

  function barraClasses(classes) {
    return `<div class="segbar" aria-hidden="true">${classes.map(c => `<span style="flex:${c.valor};background:${c.cor}"></span>`).join('')}</div>`;
  }

  function agendaItem(e) {
    return `<a class="agenda-item" href="#economia?evento=${e.id}"><span class="date-block" aria-hidden="true"><b>${e.dia}</b><small>nov</small></span>
      <span class="row-main"><strong>${e.titulo}</strong><span class="sub">${e.dia} de novembro · ${e.tipo}</span></span>
      <span class="row-chevron">${ic('forth')}</span></a>`;
  }

  function aviso(fluxo, amostra = false) {
    const falta = fluxo.falta > 0;
    const pendente = fluxo.disponivel === null;
    const texto = falta ? `Faltam ${moeda(fluxo.falta)}` : pendente ? 'Aporte a informar' : 'Alimentação';
    const contexto = falta ? 'O saldo não cobre as contas e a reserva.' : pendente ? 'Confira as contas até a próxima entrada.'
      : `${moeda(m.resumoGastos(4, 'alimentacao').diferenca)} acima da média até o dia 15.`;
    const tag = amostra ? 'div' : 'a';
    const href = amostra ? '' : `href="#${falta || pendente ? 'aportes' : 'gastos?categoria=alimentacao'}"`;
    return `<${tag} class="attention${falta ? ' warn' : ''}" ${href}>
      ${tile(falta ? 'alert' : pendente ? 'info' : 'basket', falta || pendente ? '' : 'alimentacao')}
      <span class="row-main"><strong>${texto}</strong><span class="sub">${contexto}</span></span>${amostra ? '' : ic('forth')}</${tag}>`;
  }

  function hoje(estado) {
    const fluxo = m.fluxo(estado.cenario);
    const classes = m.composicao();
    return `<div class="page-head home-heading"><div><h1 tabindex="-1">Hoje</h1><p>15 de novembro <span class="mid-dot">·</span> Dados fictícios</p></div></div>
      <div class="home-grid">
        ${saldo(fluxo)}
        <div class="quick-actions" role="group" aria-label="Ações rápidas">
          <a class="quick" href="#gastos?lista=1"><span class="quick-icon">${ic('receipt')}</span>Ver lançamentos</a>
          <a class="quick" href="#aportes?simular=1"><span class="quick-icon">${ic('aporte')}</span>Simular aporte</a>
          <button class="quick" data-dialog="chat"><span class="quick-icon">${ic('chat')}</span>Perguntar à IA</button>
        </div>
        ${aviso(fluxo)}
        <section class="panel ledger">${cabecalho('Últimos lançamentos', link('gastos?lista=1', 'Ver todos'))}
          ${movimentos(m.extrato(4).slice(0, 4), 4)}</section>
        <section class="panel agenda">${cabecalho('Na agenda', link('economia', 'Ver agenda'))}${agendaItem(m.eventos[0])}</section>
        <section class="panel mix">${cabecalho('Carteira por classe', link('carteira', 'Ver carteira'))}
          ${barraClasses(classes)}<div class="legend">${classes.map(c => `<span class="legend-item"><i class="dot" style="background:${c.cor}"></i>${c.nome} <span class="num">${percentual(c.percentual)}</span></span>`).join('')}</div></section>
      </div>`;
  }

  function posicoesFiltradas(classe, busca) {
    const simplificar = s => s.normalize('NFD').replace(/[\u0300-\u036f]/g, '').toLocaleLowerCase('pt-BR');
    const termo = simplificar(busca.trim());
    return m.posicoes.filter(p => (classe === 'todas' || p.classe === classe) &&
      simplificar([p.nome, p.instituicao, p.emissor].join(' ')).includes(termo));
  }
  function listaPosicoes(linhas) {
    if (!linhas.length) return '<p class="empty">Nenhuma posição encontrada. Tente outro nome ou limpe o filtro.</p>';
    return linhas.map(p => `<button class="position" data-dialog="posicao" data-id="${p.id}">
      <span class="row-main"><strong>${p.nome}</strong><span class="sub">${p.instituicao}</span></span>
      <span class="amount-end">${moeda(p.valor)}</span><span class="row-chevron">${ic('forth')}</span></button>`).join('');
  }

  function carteira(estado) {
    const classes = m.composicao();
    const linhas = posicoesFiltradas(estado.classe, estado.busca);
    return titulo('Carteira', 'Posições em reais · 15 de novembro') + `<div class="two-col"><div class="stack">
      <section class="panel"><div class="money-summary"><span class="muted">Patrimônio investido</span>${valorGrande(m.patrimonio)}</div>
        ${detalhe('Histórico do patrimônio', `<dl>${m.historico.map(([mes, valor]) => linha(mes + ' de 2026', moeda(valor))).join('')}</dl>`)}</section>
      <section class="panel"><h2>Distribuição</h2>${barraClasses(classes)}
        ${classes.map(c => `<button class="class-button" data-classe="${c.id}" aria-pressed="${estado.classe === c.id}" aria-controls="lista-posicoes">
          <span class="dot" style="background:${c.cor}"></span><span>${c.nome}</span>
          <span class="class-total"><strong class="num">${moeda(c.valor)}</strong><small>${percentual(c.percentual)}</small></span></button>`).join('')}
      </section></div>
      <section class="panel"><div class="filter-head"><h2>Posições</h2><button class="text-link" data-limpar-filtro${estado.classe === 'todas' && !estado.busca ? ' hidden' : ''}>Limpar filtro</button></div>
        <label class="field field-search"><span>Buscar posição</span><input type="search" id="busca-posicao" placeholder="Nome ou emissor" value="${esc(estado.busca)}"></label>
        <p class="sub" id="contagem-posicoes" role="status">${linhas.length} ${linhas.length === 1 ? 'posição' : 'posições'}</p>
        <div id="lista-posicoes">${listaPosicoes(linhas)}</div></section></div>`;
  }

  function gastos(params) {
    const solicitado = m.meses.findIndex(([id]) => id === params.get('mes'));
    const indice = solicitado >= 2 ? solicitado : 4;
    const categoria = Object.hasOwn(m.categorias, params.get('categoria')) ? params.get('categoria') : 'todas';
    const { total, media, diferenca } = m.resumoGastos(indice, categoria);
    const max = Math.max(total, media, 1);
    const bar = (nome, valor, cor) => `<div><div class="bar-label"><span>${nome}</span><strong class="num">${moeda(valor)}</strong></div>
      <div class="bar-track" aria-hidden="true"><span style="width:${valor / max * 100}%;background:${cor}"></span></div></div>`;
    return titulo('Gastos', 'Comparação até o dia 15 de cada mês') + `<div class="two-col"><div class="stack">
      <section class="panel"><div class="pair-fields">
        <label class="field"><span>Mês</span><select id="filtro-mes">${opcoes(m.meses.slice(2), m.meses[indice][0])}</select></label>
        <label class="field"><span>Categoria</span><select id="filtro-categoria">${opcoes([['todas', 'Todas as categorias'], ...Object.entries(m.categorias)], categoria)}</select></label></div>
        <div class="money-summary section-gap"><span class="muted">${categoria === 'todas' ? 'Gastos do período' : m.categorias[categoria]}</span>${valorGrande(total)}</div>
        <div class="comparison">${bar('Este mês', total, '#a9bfdf')}${bar('Média anterior', media, '#818d9c')}</div>
        <p class="section-gap">${diferenca === 0 ? 'O total está na média.' : `${moeda(Math.abs(diferenca))} ${diferenca > 0 ? 'acima' : 'abaixo'} da média.`}</p>
        ${categoria === 'todas' ? '' : detalhe('Diferença por estabelecimento', `<dl>${m.diferencasPorLoja(indice, categoria)
          .map(([nome, variacao]) => linha(nome, (variacao > 0 ? '+' : variacao < 0 ? '−' : '') + moeda(Math.abs(variacao)))).join('')}</dl>`)}
        ${detalhe('Meses usados na comparação', `<dl>${[indice - 2, indice - 1].map(i => linha('1 a ' + dataMes(15, i), moeda(m.somarGastos(i, categoria)))).join('')}</dl>`)}
      </section></div><section class="panel ledger"><div class="panel-head"><h2 id="titulo-lancamentos" tabindex="-1">Lançamentos</h2>
      <span class="sub">${m.extrato(indice, categoria).length} no período</span></div>
      ${movimentos(m.extrato(indice, categoria), indice)}</section></div>`;
  }

  function distribuicao(valor, disponivel) {
    if (valor === null) return '<p class="empty">Informe um valor para ver a distribuição simulada.</p>';
    if (valor === 0) return '<p class="empty">Com R$ 0, a carteira mantém a composição atual.</p>';
    const partes = m.distribuir(valor);
    return `<p class="sub">${moeda(valor)} distribuídos entre as classes abaixo da meta.</p>
      <div class="comparison">${partes.map(p => `<div><div class="bar-label"><span>${p.nome}</span><strong class="num">${moeda(p.parcela)}</strong></div>
        <div class="bar-track" aria-hidden="true"><span data-parcela="${p.id}" style="width:${p.parcela / valor * 100}%;background:${p.cor}"></span></div>
        <span class="sub">${percentual(p.parcela / valor * 100)} do aporte</span></div>`).join('')}</div>
      <p class="sub section-gap">Patrimônio após a simulação · ${moeda(m.patrimonio + valor)}</p>
      ${disponivel === null || valor > disponivel ? '<p class="sub">O cálculo usa o valor informado para esta simulação.</p>' : ''}`;
  }

  function aportes(estado) {
    const f = m.fluxo(estado.cenario);
    const valor = estado.simulado ?? f.disponivel;
    return titulo('Aportes', 'Contas previstas até a próxima entrada') + `<div class="two-col"><div class="stack">
      <section class="panel"><div class="money-summary"><span class="muted">Disponível para aportar</span>
        ${f.disponivel === null ? '<strong class="big-value">A informar</strong>' : valorGrande(f.disponivel)}</div>
        ${f.falta > 0 ? `<p class="alert-text section-gap">Faltam ${moeda(f.falta)} para cobrir as contas e a reserva.</p>`
          : f.disponivel === null ? '<p class="sub section-gap">Falta informar o total das contas até a próxima entrada.</p>' : ''}
        <dl class="section-gap">${linha('Saldo em conta', moeda(f.saldo))}${linha('Contas previstas', f.contas === null ? 'A informar' : '−' + moeda(f.contas))}
          ${linha('Saldo mínimo', '−' + moeda(f.minimo))}${linha('Próxima entrada', '30 de novembro')}</dl></section>
      <section class="panel"><h2>Simular aporte</h2><form id="form-aporte" novalidate class="section-gap">
        ${campo('aporte', 'Valor da simulação (R$)', `id="valor-aporte" required aria-describedby="erro-aporte" value="${valor === null ? '' : valor.toLocaleString('pt-BR', { useGrouping: false, maximumFractionDigits: 2 })}"`)}
        <p class="field-error" id="erro-aporte" role="alert"></p><div class="button-line"><button class="primary">Recalcular</button>
        <button type="button" class="text-link" data-repor-aporte${estado.simulado === null ? ' hidden' : ''}>Usar valor inicial</button></div></form></section>
      </div><section class="panel" aria-labelledby="titulo-distribuicao"><h2 id="titulo-distribuicao">Distribuição simulada</h2><div class="section-gap" id="distribuicao">${distribuicao(valor, f.disponivel)}</div></section></div>`;
  }

  function economia(params) {
    const e = m.eventos.find(item => item.id === params.get('evento')) || m.eventos[0];
    return voltar('mais', 'Mais') + titulo('Economia', 'Agenda e análises de exemplo') + `<div class="two-col">
      <article class="panel"><p class="date-line">${e.etapa} · ${e.dia} de novembro</p><h2>${e.titulo}</h2>
        <p class="conclusion">${e.conclusao}</p><p class="muted prose">${e.contexto}</p>
        <hr class="section-rule">
        ${detalhe('Debate dos especialistas', `<div class="text-stack">${e.argumentos.map(([nome, texto]) => `<h3>${nome}</h3><p>${texto}</p>`).join('')}
          <h3>Divergência</h3><p>${e.divergencia}</p></div>`)}
        ${detalhe('Fontes do exemplo', `<p>${e.fonte}</p><p class="sub">Referência da prévia · 15 de novembro de 2026</p>`)}
      </article><section class="panel"><h2>Na agenda</h2>${m.eventos.map(agendaItem).join('')}</section></div>`;
  }

  function calculadoras(estado) {
    const modo = estado.modoCalc;
    return voltar('mais', 'Mais') + titulo('Calculadoras') + `<section class="panel narrow"><h2>Simulação financeira</h2>
      <div class="pick-list" role="group" aria-label="Escolher calculadora">${Object.entries(modos).map(([id, nome]) =>
        `<button class="pick-button" data-modo="${id}" aria-pressed="${id === modo}">${nome}</button>`).join('')}</div>
      <form id="form-calc" novalidate><div class="form-grid paired">
        ${campo('inicial', 'Valor inicial (R$, opcional)')}
        ${modo !== 'mensal' ? campo('mensal', 'Aporte mensal (R$, opcional)') : ''}
        ${campo('taxa', 'Taxa de juros (%)', 'required')}
        <label class="field"><span>Período da taxa</span><select name="periodo"><option value="mes">Ao mês</option><option value="ano">Ao ano</option></select></label>
        ${modo !== 'prazo' ? campo('meses', 'Prazo em meses', 'required') : ''}
        ${modo !== 'juros' ? campo('meta', 'Valor da meta (R$)', 'required') : ''}
      </div><p class="field-error" id="erro-calc" role="alert"></p>
      <div class="button-line"><button class="primary">Calcular</button><button type="reset" class="secondary">Limpar</button></div></form>
      <div aria-live="polite" id="resultado-calc"></div></section>`;
  }

  function resultadoCalc(modo, result) {
    let rotulo = 'Valor final estimado';
    let valor = moeda(result.valor ?? 0);
    if (modo === 'prazo') {
      rotulo = 'Tempo até a meta';
      valor = result.meses === 0 ? 'Meta já atingida' : result.meses + (result.meses === 1 ? ' mês' : ' meses');
    } else if (modo === 'mensal') rotulo = 'Aporte mensal necessário';
    return `<div class="result-block"><p class="sub">${rotulo}</p><p class="big-value num">${valor}</p>
      ${modo === 'juros' ? `<dl>${linha('Total aplicado', moeda(result.aplicado))}${linha('Rendimento estimado', moeda(Math.round((result.valor - result.aplicado) * 100) / 100))}</dl>` : ''}
      <p class="sub">Projeção bruta, com aportes no fim de cada mês.</p></div>`;
  }

  function ferramenta(icone, nome, descricao, destino, dialogo = false) {
    const item = dialogo ? 'button' : 'a';
    const attr = dialogo ? `data-dialog="${destino}"` : `href="#${destino}"`;
    return `<${item} class="tool-row" ${attr}>${tile(icone)}<span class="row-main"><strong>${nome}</strong>
      <span class="sub">${descricao}</span></span>${ic('forth')}</${item}>`;
  }

  function mais() {
    return titulo('Mais') + `<div class="two-col"><div class="stack"><section class="panel"><h2>Ferramentas</h2>
      ${ferramenta('globe', 'Economia', 'Agenda, conclusões e debate dos especialistas.', 'economia')}
      ${ferramenta('calc', 'Calculadoras', 'Juros, prazo e aporte mensal.', 'calculadoras')}</section>
      <section class="panel"><h2>Prévia</h2>
      ${ferramenta('sliders', 'Cenário da conta', 'Alternar entre os quatro exemplos.', 'previa', true)}
      ${ferramenta('layers', 'Sistema visual', 'Cores, acabamento, movimento e controles.', 'sistema')}</section></div>
      <section class="panel"><h2>Sua conta</h2>
      ${ferramenta('profile', 'Perfil financeiro', 'Objetivo, reserva e prazo.', 'perfil', true)}
      ${ferramenta('link', 'Conexões e extratos', 'Instituições e origem dos dados.', 'conexoes', true)}
      ${ferramenta('chart', 'Agentes e pesquisas', 'Atividade recente da conta de exemplo.', 'agentes', true)}</section></div>`;
  }

  function sistema() {
    const cores = [['Fundo', '#191C1F'], ['Superfície', '#24282C'], ['Texto', '#EEECE5'], ['Ação', '#A9BFDF'], ['Alerta', '#E8AD91']];
    return voltar('mais', 'Mais') + titulo('Sistema visual', 'Guia da prévia do OpenChris') + `<div class="two-col">
      <div class="stack"><section class="panel"><h2>Paleta</h2><div class="swatches">${cores.map(([nome, cor]) =>
        `<div class="swatch"><span class="swatch-sample" style="background:${cor}"></span><strong>${nome}</strong><span class="sub">${cor}</span></div>`).join('')}</div>
        <div class="material-samples">
          <div class="material-sample titanio"><strong>Titânio escovado</strong><span>Cartão de patrimônio</span></div>
          <div class="material-sample material-carbono"><strong>Fibra de carbono</strong><span class="muted">Fundo das telas</span></div>
        </div></section>
      <section class="panel"><h2>Source Sans 3</h2><div class="type-demo">${valorGrande(m.patrimonio)}<span class="sub">Patrimônio · peso 500</span>
        <h3>Lançamentos</h3><p>O gasto do mês reúne as compras até o dia 15.</p><span class="sub">15 de novembro · Texto de apoio</span></div></section></div>
      <div class="stack"><section class="panel"><h2>Ações e estados</h2><div class="button-line">
        <button class="primary" data-dialog="previa">Alterar cenário</button><button class="secondary" data-exemplo-notificacao>Testar notificação</button>
        <button class="secondary" data-dialog="perfil">Ver perfil</button>
        <button class="secondary" disabled>Indisponível</button></div>
        <div class="section-gap">${aviso(m.fluxo('insuficiente'), true)}</div>
        <p class="empty">Nenhum lançamento neste período.</p>
        <details class="breakdown"><summary>Estado de carregamento${ic('down')}</summary><p class="sub">Buscando lançamentos…</p></details></section>
        <section class="panel"><h2>Campo de valor</h2><form id="form-amostra" class="section-gap" novalidate>
        ${campo('valor', 'Valor em reais', 'required aria-describedby="msg-amostra"')}<div class="button-line"><button class="secondary">Validar campo</button></div>
        <p class="form-feedback" id="msg-amostra" role="status"></p></form></section></div></div>`;
  }

  const perguntas = {
    alimentacao: 'Por que alimentação ficou acima da média?',
    aporte: 'Como meu aporte foi calculado?',
    carteira: 'Como está dividida a minha carteira?'
  };
  function respostaChat(id, cenario) {
    const f = m.fluxo(cenario);
    if (id === 'aporte') {
      if (f.disponivel === null) return 'O saldo em conta é R$ 7.800 e o mínimo reservado é R$ 1.600. Falta o total das contas previstas para calcular o aporte.';
      if (f.falta > 0) return `O saldo é ${moeda(f.saldo)}. As contas e a reserva somam ${moeda(f.contas + f.minimo)}. Faltam ${moeda(f.falta)} e o aporte disponível é R$ 0.`;
      return `Dos ${moeda(f.saldo)} em conta, ${moeda(f.contas)} cobrem as contas previstas e ${moeda(f.minimo)} ficam na reserva. O aporte disponível é ${moeda(f.disponivel)}.`;
    }
    if (id === 'carteira') return `O patrimônio é ${moeda(m.patrimonio)}. A maior classe é renda fixa, com ${moeda(m.composicao()[0].valor)}. Na Carteira você pode tocar em cada classe para conferir as posições.`;
    const resumo = m.resumoGastos(4, 'alimentacao');
    return `Até o dia 15, as compras de alimentação somaram ${moeda(resumo.total)}. A média de setembro e outubro, até o mesmo dia, foi ${moeda(resumo.media)}. A diferença é de ${moeda(resumo.diferenca)}.`;
  }
  function conversa(id, cenario) {
    return `<p class="chat-question">${perguntas[id]}</p><div class="chat-message"><span class="speaker">OpenChris</span><p>${respostaChat(id, cenario)}</p></div>`;
  }

  function painel(tipo, attrs, estado) {
    if (tipo === 'previa') return ['Opções da prévia', `<p class="sheet-copy">O cenário altera os dados fictícios de Hoje e Aportes.</p>
      <fieldset class="scenario-list"><legend>Cenário da conta</legend>${Object.entries(m.cenarios).map(([id, c]) =>
        `<label class="option-row"><input type="radio" name="cenario" value="${id}"${id === estado.cenario ? ' checked' : ''}>
        <span class="row-main"><strong>${c.nome}</strong><span class="sub">${c.detalhe}</span></span></label>`).join('')}</fieldset>
      <div class="button-line scenario-actions"><button class="primary" data-fechar="cenario">Concluir</button>${link('sistema', 'Ver sistema visual')}</div>`];
    if (tipo === 'chat') return ['Perguntar à IA', `<p class="sheet-copy">Conversa de exemplo com os dados fictícios da prévia.</p>
      <div id="conversa" role="log" aria-live="polite">${conversa('alimentacao', estado.cenario)}</div><div class="chat-choices" role="group" aria-label="Perguntas de exemplo">
      ${[['alimentacao', 'Meus gastos'], ['aporte', 'Meu aporte'], ['carteira', 'Minha carteira']].map(([id, nome]) =>
        `<button data-pergunta="${id}" aria-pressed="${id === 'alimentacao'}">${nome}</button>`).join('')}</div>`];
    if (tipo === 'lancamento') {
      const indice = Number(attrs.mes);
      const l = m.extrato(indice).find(item => item.id === attrs.id);
      if (!l) return null;
      return [l.nome, `<p class="big-value num">−${moeda(l.valor)}</p><dl class="section-gap">
        ${linha('Data', dataMes(l.dia, indice) + ' de 2026')}${linha('Categoria', m.categorias[l.categoria])}
        ${linha('Tipo', 'Despesa')}${linha('Origem', 'Extrato de exemplo')}</dl>
        ${link('gastos?mes=' + m.meses[indice][0] + '&categoria=' + l.categoria, 'Ver esta categoria')}`];
    }
    if (tipo === 'posicao') {
      const p = m.posicoes.find(item => item.id === attrs.id);
      if (!p) return null;
      const cls = m.classes.find(c => c.id === p.classe);
      return [p.nome, `<p class="big-value num">${moeda(p.valor)}</p><dl class="section-gap">
        ${linha('Classe', cls.nome)}${linha('Instituição', p.instituicao)}${linha('Emissor', p.emissor)}
        ${p.indexador ? linha('Indexador', p.indexador) : ''}${p.vencimento ? linha('Vencimento', p.vencimento) : ''}
        ${linha('Origem', 'Posição de exemplo')}${linha('Atualização', '15 de novembro de 2026')}</dl>`];
    }
    if (tipo === 'perfil') return ['Perfil financeiro', `<p class="sheet-copy">Perfil da conta de exemplo.</p><dl>
      ${linha('Objetivo', 'Formar patrimônio')}${linha('Prazo', 'Mais de 5 anos')}${linha('Tolerância a perdas', 'Moderada')}
      ${linha('Saldo mínimo em conta', 'R$ 1.600')}</dl>`];
    if (tipo === 'conexoes') return ['Conexões e extratos', `<p class="sheet-copy">Instituições fictícias usadas na carteira e nos lançamentos.</p><dl>
      ${linha('Banco Horizonte', 'Conta e CDB')}${linha('Corretora Aurora', 'Posições de investimento')}${linha('Última atualização', '15 de novembro de 2026')}</dl>`];
    if (tipo === 'agentes') return ['Agentes e pesquisas', `<p class="sheet-copy">Atividades ilustrativas de 15 de novembro.</p><dl>
      ${linha('Classificação de gastos', '13 lançamentos classificados')}${linha('Pesquisa de ativos', '7 posições identificadas')}
      ${linha('Análise econômica', '2 cenários disponíveis')}</dl>${link('economia', 'Abrir análises')}`];
    return null;
  }

  return { nomes, principal, modos, moeda, navegacao, hoje, carteira, listaPosicoes, posicoesFiltradas,
    gastos, aportes, distribuicao, economia, calculadoras, resultadoCalc, mais, sistema, painel, conversa };
})();
