/* Dados da conta de demonstração. Todos os cálculos acontecem no navegador. */
const modelo = (() => {
  'use strict';

  const meses = [
    ['2026-07', 'Julho'], ['2026-08', 'Agosto'], ['2026-09', 'Setembro'],
    ['2026-10', 'Outubro'], ['2026-11', 'Novembro']
  ];
  const categorias = {
    alimentacao: 'Alimentação', moradia: 'Moradia', transporte: 'Transporte',
    lazer: 'Lazer', saude: 'Saúde'
  };
  // Cada coluna de valores corresponde ao mesmo dia de um dos cinco meses.
  const lancamentos = [
    ['mercado', 15, 'Mercado do Bairro', 'alimentacao', 'basket', [207, 226, 232, 245, 286]],
    ['cafe', 14, 'Café da Esquina', 'alimentacao', 'cup', [61, 63, 65, 73, 94]],
    ['livraria', 13, 'Livraria Central', 'lazer', 'book', [78, 86, 95, 104, 118]],
    ['transporte', 12, 'Mobilidade Urbana', 'transporte', 'bus', [70, 72, 74, 79, 82]],
    ['feira', 11, 'Feira da Vila', 'alimentacao', 'basket', [153, 158, 165, 180, 210]],
    ['energia', 10, 'Energia residencial', 'moradia', 'home', [168, 172, 175, 176, 178]],
    ['mercado-semanal', 9, 'Mercado do Bairro', 'alimentacao', 'basket', [378, 391, 397, 417, 490]],
    ['condominio', 8, 'Condomínio', 'moradia', 'home', [352, 352, 352, 352, 352]],
    ['bistro', 7, 'Bistrô da Praça', 'alimentacao', 'cup', [138, 144, 151, 155, 200]],
    ['passe', 6, 'Passe mensal', 'transporte', 'bus', [228, 228, 228, 228, 228]],
    ['cinema', 5, 'Cinema da Rua', 'lazer', 'book', [84, 90, 94, 91, 92]],
    ['aluguel', 4, 'Aluguel', 'moradia', 'home', [1650, 1650, 1650, 1650, 1650]],
    ['farmacia', 3, 'Farmácia da Praça', 'saude', 'health', [170, 185, 192, 195, 200]]
  ].map(([id, dia, nome, categoria, icone, valores]) => ({ id, dia, nome, categoria, icone, valores }));

  const classes = [
    { id: 'fixa', nome: 'Renda fixa', cor: '#a9bfdf', meta: 52 },
    { id: 'acoes', nome: 'Ações', cor: '#88aaa3', meta: 23 },
    { id: 'fundos', nome: 'Fundos imobiliários', cor: '#b9a9c7', meta: 13 },
    { id: 'exterior', nome: 'Exterior', cor: '#cbb593', meta: 12 }
  ];
  const posicoes = [
    ['tesouro', 'Tesouro Selic 2029', 'fixa', 68000, 'Corretora Aurora', 'Tesouro Nacional', 'Selic', '1º de março de 2029'],
    ['cdb', 'CDB Horizonte', 'fixa', 40000, 'Banco Horizonte', 'Banco Horizonte', 'CDI', '20 de agosto de 2028'],
    ['aurora', 'Aurora Energia', 'acoes', 28600, 'Corretora Aurora', 'Aurora Energia'],
    ['ventos', 'Ventos do Sul', 'acoes', 14200, 'Corretora Aurora', 'Ventos do Sul'],
    ['galpoes', 'Fundo Galpões', 'fundos', 13800, 'Banco Horizonte', 'Gestora Horizonte'],
    ['urbano', 'Fundo Renda Urbana', 'fundos', 11000, 'Corretora Aurora', 'Gestora Aurora'],
    ['global', 'Fundo Índice Global', 'exterior', 22000, 'Corretora Aurora', 'Gestora Aurora']
  ].map(([id, nome, classe, valor, instituicao, emissor, indexador, vencimento]) =>
    ({ id, nome, classe, valor, instituicao, emissor, indexador, vencimento }));
  const patrimonio = posicoes.reduce((soma, p) => soma + p.valor, 0);
  const historico = [
    ['Jun', 168900], ['Jul', 176400], ['Ago', 173800],
    ['Set', 183600], ['Out', 190000], ['Nov', patrimonio]
  ];
  const cenarios = {
    completo: { nome: 'Aporte disponível', detalhe: 'Sobram R$ 2.400 após as contas e a reserva.', contas: 3800 },
    pendente: { nome: 'A informar', detalhe: 'Falta informar o total das próximas contas.', contas: null },
    zero: { nome: 'Aporte zerado', detalhe: 'As contas e a reserva consomem todo o saldo.', contas: 6200 },
    insuficiente: { nome: 'Saldo insuficiente', detalhe: 'Faltam R$ 350 para cobrir as contas e a reserva.', contas: 6550 }
  };
  const eventos = [
    {
      id: 'copom', dia: 18, titulo: 'Decisão do Copom', tipo: 'Juros', etapa: 'Antes da decisão',
      conclusao: 'A taxa de juros afeta primeiro os R$ 108.000 da carteira ligados à Selic ou ao CDI.',
      contexto: 'A simulação considera a taxa estável. Uma mudança altera o ritmo de rendimento dos dois títulos pós-fixados.',
      argumentos: [
        ['Renda fixa', 'O Tesouro Selic e o CDB somam R$ 108.000. O rendimento futuro dos dois acompanha seus respectivos indexadores.'],
        ['Risco', 'A data de uso do dinheiro importa mesmo se os juros continuarem estáveis. Os dois títulos têm vencimentos diferentes.']
      ],
      divergencia: 'A análise de renda fixa dá mais peso ao rendimento futuro. A de risco prioriza o dinheiro necessário antes dos vencimentos.',
      fonte: 'A hipótese de juros estáveis e as duas posições da carteira compõem este exemplo.'
    },
    {
      id: 'inflacao', dia: 26, titulo: 'Prévia da inflação', tipo: 'Inflação', etapa: 'Antes da divulgação',
      conclusao: 'A alta de R$ 240 em alimentação merece ser comparada com as compras da própria conta.',
      contexto: 'O índice de preços e os gastos da família medem coisas diferentes. A composição das compras ajuda a explicar essa diferença.',
      argumentos: [
        ['Economia', 'Um índice de preços reúne uma cesta ampla. A mudança nos gastos da família também depende das quantidades compradas.'],
        ['Orçamento', 'Na conta de exemplo, a soma das compras de alimentação até o dia 15 é R$ 1.280. A média comparável é R$ 1.040.']
      ],
      divergencia: 'O efeito dos preços ainda está em aberto. Os lançamentos confirmam apenas o valor total das compras.',
      fonte: 'Lançamentos fictícios de setembro, outubro e novembro, comparados até o mesmo dia.'
    }
  ];

  function fluxo(cenario) {
    const saldo = 7800;
    const minimo = 1600;
    const contas = cenarios[cenario].contas;
    const liquido = contas === null ? null : saldo - contas - minimo;
    return { saldo, minimo, contas, disponivel: liquido === null ? null : Math.max(0, liquido),
      falta: liquido === null ? null : Math.max(0, -liquido) };
  }

  function extrato(indice, categoria = 'todas') {
    return lancamentos.filter(l => categoria === 'todas' || l.categoria === categoria)
      .map(l => ({ ...l, valor: l.valores[indice] }));
  }

  function somarGastos(indice, categoria) {
    return extrato(indice, categoria).reduce((soma, l) => soma + l.valor, 0);
  }

  function resumoGastos(indice, categoria = 'todas') {
    const total = somarGastos(indice, categoria);
    const media = (somarGastos(indice - 1, categoria) + somarGastos(indice - 2, categoria)) / 2;
    return { total, media, diferenca: total - media };
  }

  function diferencasPorLoja(indice, categoria) {
    const lojas = new Map();
    for (const l of extrato(indice, categoria)) {
      const variacao = l.valor - (l.valores[indice - 1] + l.valores[indice - 2]) / 2;
      lojas.set(l.nome, (lojas.get(l.nome) || 0) + variacao);
    }
    return [...lojas].sort((a, b) => Math.abs(b[1]) - Math.abs(a[1]));
  }

  function composicao() {
    return classes.map(classe => {
      const valor = posicoes.filter(p => p.classe === classe.id).reduce((soma, p) => soma + p.valor, 0);
      return { ...classe, valor, percentual: valor / patrimonio * 100 };
    });
  }

  function distribuir(aporte) {
    const centavos = Math.round(aporte * 100);
    const totalFuturo = patrimonio + aporte;
    const linhas = composicao().map(c => ({ ...c, falta: Math.max(0, totalFuturo * c.meta / 100 - c.valor) }));
    const faltaTotal = linhas.reduce((soma, l) => soma + l.falta, 0);
    const partes = linhas.map(l => {
      const exato = faltaTotal ? centavos * l.falta / faltaTotal : 0;
      return { ...l, centavos: Math.floor(exato), resto: exato - Math.floor(exato) };
    });
    const sobras = centavos - partes.reduce((soma, p) => soma + p.centavos, 0);
    const ordem = [...partes].sort((a, b) => b.resto - a.resto);
    for (let i = 0; i < sobras; i++) ordem[i].centavos++;
    return partes.filter(p => p.centavos > 0).map(p => ({ ...p, parcela: p.centavos / 100 }));
  }

  function lerNumero(texto, casas = 2) {
    const s = String(texto ?? '').trim();
    if (!s) return null;
    const agrupado = /^[+-]?\d{1,3}(?:\.\d{3})+(?:,\d+)?$/.test(s);
    if (!agrupado && !/^[+-]?\d+(?:[.,]\d+)?$/.test(s)) return NaN;
    const normal = (agrupado ? s.replaceAll('.', '') : s).replace(',', '.');
    if ((normal.split('.')[1] || '').length > casas) return NaN;
    return Number(normal);
  }

  function simularCalculadora(modo, valores) {
    const limite = 999999999999.99;
    function campo(nome, opcional, minimo, maximo, casas = 2) {
      let n = lerNumero(valores[nome], casas);
      if (n === null && opcional) n = 0;
      if (n === null || !Number.isFinite(n) || n < minimo || n > maximo) {
        const msg = nome === 'meses' ? 'Informe um prazo de 1 a 1.200 meses.'
          : nome === 'taxa' ? 'Informe uma taxa maior que −100% e de até 1.000%.'
          : 'Informe um valor válido em reais.';
        throw Object.assign(new Error(msg), { campo: nome });
      }
      return n;
    }
    const principal = campo('inicial', true, 0, limite);
    const mensal = modo === 'mensal' ? 0 : campo('mensal', true, 0, limite);
    const taxa = campo('taxa', false, -99.99999999, 1000, 8) / 100;
    const r = valores.periodo === 'ano' ? Math.expm1(Math.log1p(taxa) / 12) : taxa;
    const meta = modo === 'juros' ? null : campo('meta', false, 0, limite);
    if (modo === 'prazo') {
      let atual = principal;
      for (let n = 0; n <= 1200; n++) {
        if (Math.round(atual * 100) >= Math.round(meta * 100)) return { meses: n };
        const seguinte = atual * (1 + r) + mensal;
        if (seguinte <= atual) break;
        atual = seguinte;
      }
      throw new Error('A meta não é atingida em até 100 anos com esses valores.');
    }
    const n = campo('meses', false, 1, 1200, 0);
    const fator = Math.exp(n * Math.log1p(r));
    const serie = r === 0 ? n : Math.expm1(n * Math.log1p(r)) / r;
    const total = modo === 'mensal' ? Math.max(0, (meta - principal * fator) / serie)
      : principal * fator + mensal * serie;
    if (!Number.isFinite(total) || total > limite) throw new Error('O resultado ultrapassa o limite desta simulação.');
    return modo === 'mensal' ? { valor: Math.ceil(total * 100 - 1e-7) / 100 }
      : { valor: Math.round(total * 100) / 100, aplicado: principal + mensal * n };
  }

  return { meses, categorias, lancamentos, classes, posicoes, patrimonio, historico,
    cenarios, eventos, fluxo, extrato, somarGastos, resumoGastos, diferencasPorLoja, composicao, distribuir,
    lerNumero, simularCalculadora };
})();
