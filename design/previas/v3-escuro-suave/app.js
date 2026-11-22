(() => {
  'use strict';
  const main = document.querySelector('main');
  const nav = document.querySelector('#nav-principal');
  const modal = document.querySelector('#painel');
  const corpoModal = document.querySelector('#painel-corpo');
  const notificacao = document.querySelector('#notificacao');
  const textoNotificacao = document.querySelector('#notificacao-texto');
  const leituraNotificacao = document.querySelector('#notificacao-leitor');
  const movimentoPermitido = matchMedia('(prefers-reduced-motion: no-preference)');
  const estado = { cenario: 'completo', classe: 'todas', busca: '', simulado: null, modoCalc: 'juros' };
  let rotaAtual = '';
  let proximoFoco = '';
  let origemModal;
  let cenarioAoAbrir;
  let origemNotificacao;
  let temporizadorNotificacao;
  let ponteiroNaNotificacao = false;

  function dispensarNotificacao() {
    clearTimeout(temporizadorNotificacao);
    if (notificacao.inert) return;
    const devolverFoco = notificacao.contains(document.activeElement);
    notificacao.inert = true;
    document.removeEventListener('pointermove', acompanharPonteiro);
    ponteiroNaNotificacao = false;
    leituraNotificacao.replaceChildren();
    if (devolverFoco) (origemNotificacao?.isConnected ? origemNotificacao : main).focus({ preventScroll: true });
  }

  function agendarFechamento() {
    clearTimeout(temporizadorNotificacao);
    if (!notificacao.inert && !document.hidden && !ponteiroNaNotificacao && !notificacao.matches(':focus-within')) {
      temporizadorNotificacao = setTimeout(dispensarNotificacao, 6000);
    }
  }

  function acompanharPonteiro(e) {
    if (e.pointerType === 'touch') return;
    const area = notificacao.getBoundingClientRect();
    const dentro = e.clientX >= area.left && e.clientX <= area.right && e.clientY >= area.top && e.clientY <= area.bottom;
    if (dentro === ponteiroNaNotificacao) return;
    ponteiroNaNotificacao = dentro;
    agendarFechamento();
  }

  function notificar(mensagem, origem) {
    origemNotificacao = origem;
    textoNotificacao.textContent = mensagem;
    notificacao.classList.remove('no-topo');
    const area = notificacao.getBoundingClientRect();
    const cobreAcao = Array.from(main.querySelectorAll('form .button-line')).some(grupo => {
      const r = grupo.getBoundingClientRect();
      return r.bottom > area.top - 8 && r.top < area.bottom + 8 && r.right > area.left && r.left < area.right;
    });
    notificacao.classList.toggle('no-topo', cobreAcao);
    notificacao.inert = false;
    leituraNotificacao.replaceChildren(document.createTextNode(mensagem));
    document.addEventListener('pointermove', acompanharPonteiro, { passive: true });
    agendarFechamento();
  }

  notificacao.querySelector('button').addEventListener('click', dispensarNotificacao);
  notificacao.addEventListener('focusin', () => clearTimeout(temporizadorNotificacao));
  notificacao.addEventListener('focusout', () => queueMicrotask(agendarFechamento));
  document.addEventListener('visibilitychange', agendarFechamento);

  function endereco() {
    const [nome, consulta = ''] = location.hash.slice(1).split('?');
    return { nome: Object.hasOwn(telas.nomes, nome) ? nome : 'hoje', params: new URLSearchParams(consulta) };
  }

  function focarDestino(nome, params) {
    let alvo;
    if (nome === 'gastos' && params.get('lista') === '1') alvo = document.querySelector('#titulo-lancamentos');
    if (nome === 'aportes' && params.get('simular') === '1') alvo = document.querySelector('#valor-aporte');
    if (alvo) {
      alvo.focus({ preventScroll: true });
      alvo.scrollIntoView({ block: 'nearest' });
    } else {
      main.querySelector('h1').focus({ preventScroll: true });
      window.scrollTo(0, 0);
    }
  }

  function render(mover = false) {
    const { nome, params } = endereco();
    const mudouDeTela = Boolean(rotaAtual && rotaAtual !== nome);
    if (mudouDeTela) dispensarNotificacao();
    main.classList.toggle('troca-de-tela', mudouDeTela);
    if (rotaAtual === 'calculadoras' && nome !== rotaAtual) estado.modoCalc = 'juros';
    rotaAtual = nome;
    let conteudo;
    switch (nome) {
      case 'carteira': conteudo = telas.carteira(estado); break;
      case 'gastos': conteudo = telas.gastos(params); break;
      case 'aportes': conteudo = telas.aportes(estado); break;
      case 'economia': conteudo = telas.economia(params); break;
      case 'calculadoras': conteudo = telas.calculadoras(estado); break;
      case 'sistema': conteudo = telas.sistema(); break;
      case 'mais': conteudo = telas.mais(); break;
      default: conteudo = telas.hoje(estado);
    }
    main.innerHTML = conteudo;
    const ativa = Object.hasOwn(telas.principal, nome) ? nome : 'mais';
    if (nav.dataset.ativa !== ativa) {
      nav.innerHTML = telas.navegacao(ativa);
      nav.dataset.ativa = ativa;
    }
    const guia = document.querySelector('.review-link');
    if (nome === 'sistema') guia.setAttribute('aria-current', 'page');
    else guia.removeAttribute('aria-current');
    document.title = telas.nomes[nome] + ' · OpenChris';
    if (proximoFoco) {
      document.getElementById(proximoFoco)?.focus({ preventScroll: true });
      proximoFoco = '';
    } else if (mover) focarDestino(nome, params);
  }

  function abrirModal(botao) {
    const painel = telas.painel(botao.dataset.dialog, botao.dataset, estado);
    if (!painel) return;
    dispensarNotificacao();
    cenarioAoAbrir = estado.cenario;
    origemModal = botao;
    document.querySelector('#painel-titulo').textContent = painel[0];
    corpoModal.innerHTML = painel[1];
    modal.showModal();
    corpoModal.scrollTop = 0;
  }

  function filtrarPosicoes() {
    const linhas = telas.posicoesFiltradas(estado.classe, estado.busca);
    main.querySelector('#lista-posicoes').innerHTML = telas.listaPosicoes(linhas);
    main.querySelector('#contagem-posicoes').textContent = linhas.length + (linhas.length === 1 ? ' posição' : ' posições');
    main.querySelector('[data-limpar-filtro]').hidden = estado.classe === 'todas' && !estado.busca;
    main.querySelectorAll('[data-classe]').forEach(b => b.setAttribute('aria-pressed', String(estado.classe === b.dataset.classe)));
  }

  function recalcularAporte(valor) {
    const disponivel = modelo.fluxo(estado.cenario).disponivel;
    const distribuicao = main.querySelector('#distribuicao');
    const proporcoes = new Map();
    if (movimentoPermitido.matches) {
      for (const barra of distribuicao.querySelectorAll('[data-parcela]')) {
        const trilho = barra.parentElement.getBoundingClientRect().width;
        if (trilho > 0) proporcoes.set(barra.dataset.parcela, barra.getBoundingClientRect().width / trilho);
      }
    }
    estado.simulado = valor;
    distribuicao.innerHTML = telas.distribuicao(valor ?? disponivel, disponivel);
    if (movimentoPermitido.matches) {
      for (const barra of distribuicao.querySelectorAll('[data-parcela]')) {
        const nova = parseFloat(barra.style.width) / 100;
        const anterior = proporcoes.get(barra.dataset.parcela) ?? 0;
        if (nova > 0) {
          barra.style.setProperty('--escala-anterior', anterior / nova);
          barra.classList.add('parcela-atualizada');
        }
      }
    }
    main.querySelector('[data-repor-aporte]').hidden = valor === null;
  }

  document.addEventListener('click', e => {
    const el = e.target.closest('a, button');
    if (!el) return;
    if (el.matches('.skip')) {
      e.preventDefault();
      main.focus();
      return;
    }
    if (el.matches('a[href^="#"]')) {
      if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      if (modal.open) modal.close();
      if (el.hash === location.hash || (!location.hash && el.hash === '#hoje')) {
        e.preventDefault();
        const { nome, params } = endereco();
        focarDestino(nome, params);
      }
      return;
    }
    if (el.hasAttribute('data-fechar')) {
      modal.close();
      if (el.dataset.fechar === 'cenario' && cenarioAoAbrir !== estado.cenario) {
        notificar('Cenário aplicado · ' + modelo.cenarios[estado.cenario].nome, origemModal);
      }
    } else if (el.hasAttribute('data-exemplo-notificacao')) {
      notificar('Notificação de exemplo.', el);
    } else if (el.dataset.dialog) {
      abrirModal(el);
    } else if (el.hasAttribute('data-limpar-filtro')) {
      estado.busca = '';
      estado.classe = 'todas';
      const busca = main.querySelector('#busca-posicao');
      busca.value = '';
      filtrarPosicoes();
      busca.focus({ preventScroll: true });
    } else if (el.dataset.classe) {
      estado.classe = estado.classe === el.dataset.classe ? 'todas' : el.dataset.classe;
      filtrarPosicoes();
    } else if (el.dataset.pergunta) {
      corpoModal.querySelector('#conversa').innerHTML = telas.conversa(el.dataset.pergunta, estado.cenario);
      corpoModal.querySelectorAll('[data-pergunta]').forEach(b => b.setAttribute('aria-pressed', String(b === el)));
    } else if (el.dataset.modo && el.dataset.modo !== estado.modoCalc) {
      estado.modoCalc = el.dataset.modo;
      render();
      main.querySelector('[data-modo="' + estado.modoCalc + '"]').focus({ preventScroll: true });
    } else if (el.hasAttribute('data-repor-aporte')) {
      recalcularAporte(null);
      const atual = modelo.fluxo(estado.cenario).disponivel;
      const entrada = main.querySelector('#valor-aporte');
      entrada.value = atual === null ? '' : String(atual).replace('.', ',');
      entrada.removeAttribute('aria-invalid');
      main.querySelector('#erro-aporte').textContent = '';
      entrada.focus({ preventScroll: true });
      notificar('Valor inicial restaurado.', entrada);
    }
  });

  modal.addEventListener('click', e => {
    if (e.target !== modal) return;
    const r = modal.getBoundingClientRect();
    if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) modal.close();
  });
  modal.addEventListener('close', () => {
    if (origemModal && !origemModal.isConnected && document.activeElement === document.body) {
      const seletor = '[data-dialog="' + origemModal.dataset.dialog + '"]';
      (document.querySelector(seletor) || main.querySelector('h1'))?.focus({ preventScroll: true });
    }
    origemModal = null;
  });

  document.addEventListener('change', e => {
    const el = e.target;
    if (el.name === 'cenario' && Object.hasOwn(modelo.cenarios, el.value)) {
      estado.cenario = el.value;
      estado.simulado = null;
      if (rotaAtual === 'hoje' || rotaAtual === 'aportes') render();
      return;
    }
    if (!el.matches('#filtro-mes, #filtro-categoria')) return;
    const params = new URLSearchParams();
    const mes = main.querySelector('#filtro-mes').value;
    const categoria = main.querySelector('#filtro-categoria').value;
    if (mes !== '2026-11') params.set('mes', mes);
    if (categoria !== 'todas') params.set('categoria', categoria);
    proximoFoco = el.id;
    location.hash = 'gastos' + (params.size ? '?' + params : '');
  });

  function limparResultadoCalc() {
    main.querySelector('#resultado-calc').replaceChildren();
    main.querySelector('#erro-calc').textContent = '';
    main.querySelectorAll('#form-calc [aria-invalid]').forEach(el => el.removeAttribute('aria-invalid'));
  }
  document.addEventListener('input', e => {
    const el = e.target;
    if (el.id === 'busca-posicao') {
      estado.busca = el.value;
      filtrarPosicoes();
    } else if (el.id === 'valor-aporte') {
      dispensarNotificacao();
      el.removeAttribute('aria-invalid');
      main.querySelector('#erro-aporte').textContent = '';
    } else if (el.closest('#form-calc')) {
      limparResultadoCalc();
    } else if (el.closest('#form-amostra')) {
      el.removeAttribute('aria-invalid');
      main.querySelector('#msg-amostra').textContent = '';
    }
  });
  document.addEventListener('reset', e => {
    if (e.target.id === 'form-calc') limparResultadoCalc();
  });
  document.addEventListener('submit', e => {
    e.preventDefault();
    const form = e.target;
    const valores = Object.fromEntries(new FormData(form));
    if (form.id === 'form-aporte') {
      const valor = modelo.lerNumero(valores.aporte);
      const entrada = form.elements.aporte;
      if (valor === null || !Number.isFinite(valor) || valor < 0 || valor > 999999999999.99) {
        dispensarNotificacao();
        entrada.setAttribute('aria-invalid', 'true');
        form.querySelector('#erro-aporte').textContent = 'Informe um valor válido de R$ 0 até R$ 999.999.999.999,99.';
        entrada.focus();
        return;
      }
      entrada.removeAttribute('aria-invalid');
      form.querySelector('#erro-aporte').textContent = '';
      recalcularAporte(valor);
      notificar('Simulação de ' + telas.moeda(valor) + ' recalculada.', e.submitter || entrada);
    } else if (form.id === 'form-calc') {
      limparResultadoCalc();
      try {
        const resultado = modelo.simularCalculadora(estado.modoCalc, valores);
        main.querySelector('#resultado-calc').innerHTML = telas.resultadoCalc(estado.modoCalc, resultado);
      } catch (erro) {
        form.querySelector('#erro-calc').textContent = erro.message;
        const campo = form.elements.namedItem(erro.campo || '');
        if (campo) {
          campo.setAttribute('aria-invalid', 'true');
          campo.setAttribute('aria-describedby', 'erro-calc');
          campo.focus();
        }
      }
    } else if (form.id === 'form-amostra') {
      const valor = modelo.lerNumero(valores.valor);
      const valido = valor !== null && Number.isFinite(valor) && valor >= 0;
      const msg = form.querySelector('#msg-amostra');
      msg.classList.toggle('alert-text', !valido);
      msg.textContent = valido ? 'Valor válido · ' + telas.moeda(valor) : 'Informe um valor em reais, por exemplo 120,50.';
      form.elements.valor.setAttribute('aria-invalid', String(!valido));
      if (!valido) form.elements.valor.focus();
    }
  });

  window.addEventListener('hashchange', () => {
    if (modal.open) modal.close();
    render(true);
  });
  render(Boolean(endereco().params.size));
})();
