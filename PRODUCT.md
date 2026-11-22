# OpenChris

O OpenChris é um site de finanças pessoais para usar pelo navegador do celular. Ao abrir, a pessoa vê o que merece atenção, o motivo e os valores envolvidos. As rotinas de importação, classificação e pesquisa trabalham em segundo plano.

## Experiência no celular

A direção visual aprovada usa fundo escuro com textura de carbono, cartão de patrimônio em titânio, texto claro e navegação inferior. Hoje, Carteira, Gastos e Aportes ficam a um toque. Economia e Calculadoras ficam em Mais. A tela Hoje destaca o patrimônio investido, os gastos do mês e o aporte disponível, seguidos das ações rápidas e do assunto prioritário. As imagens de referência usam dados fictícios.

## Carteira, gastos e aportes

- **Carteira.** Agrupar todos os investimentos por classe e mostrar o valor e a participação de cada uma. Em renda fixa, detalhar emissor, indexador, vencimento e instituição. Registrar a origem de cada posição e a data da última atualização. Usar as conexões bancárias disponíveis e permitir completar a carteira por extrato ou cadastro manual. Conciliar as importações para evitar duplicatas.
- **Gastos.** Classificar os lançamentos por categoria. Comparar o período já transcorrido do mês com o mesmo intervalo dos meses anteriores e mostrar quais compras explicam a variação e seu efeito estimado sobre o aporte. Contar cada parcela na respectiva fatura. Se houver antecipação, registrar o total efetivamente cobrado no mês, ajustar as parcelas futuras e conciliar o pagamento da fatura para evitar dupla contagem.
- **Aportes.** Partir do saldo disponível, descontar os compromissos até a próxima entrada e o saldo mínimo escolhido pela pessoa. Exibir o cálculo e permitir ajustar o valor que será simulado. Quando os compromissos consumirem o saldo, mostrar o valor que falta e um aporte disponível de zero.
- **Distribuição.** Levantar objetivos, prazo, reserva e tolerância a perdas uma vez. Propor metas por classe, explicar a escolha e permitir ajustes. Direcionar os novos aportes às classes abaixo das metas, preservando as posições existentes. Exibir reais e porcentagem de cada parcela sugerida e a composição projetada da carteira.
- **Calculadoras.** Oferecer juros compostos, tempo até a meta e aporte mensal necessário. Os campos começam vazios. A simulação usa os valores informados e termina quando a pessoa sai, sem salvar.
- **Documentos.** Guardar, baixar e excluir PDFs e arquivos de texto. Uma declaração de IR ou um informe pode ficar disponível para o chat, se o titular autorizar. A busca roda no servidor quando chega uma pergunta e seleciona trechos pertinentes. O chat cita a página quando o arquivo tem paginação e avisa quando faltam dados para avaliar o caso.
- **Economia.** Acompanhar grandes eventos antes e depois, inclusive os que tenham pouco impacto na carteira. Distinguir o fato publicado dos cenários e mostrar o efeito sobre os ativos. A conclusão vem primeiro. Um toque abre os argumentos dos especialistas, as divergências e a evidência que sustentou a conclusão. Os avisos podem chegar pelo Telegram e abrir a análise no navegador.

## Agentes com a assinatura do Codex

Para o uso pessoal do titular, o desenho prevê um processo privado no servidor com o Codex CLI autenticado pelo ChatGPT. As tarefas agendadas usam a sessão desse processo e a franquia da assinatura. A autenticação e a execução real na nova instalação ainda precisam ser verificadas. [Modo não interativo](https://learn.chatgpt.com/docs/non-interactive-mode) · [Limites de uso](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan)

1. **Classificar gastos.** Reaproveitar as correções da pessoa e as regras já conhecidas. Enviar ao Codex, em lote, os lançamentos que ainda precisem de interpretação, com os dados mínimos para distinguir compra, transferência, pagamento de fatura e estorno. Manter o lançamento original. Uma descrição ambígua permanece pendente e pode ser corrigida sem bloquear o restante do mês.
2. **Classificar investimentos.** Identificar o instrumento pelo código disponível, pelo emissor e pelos documentos da posição. Usar a informação confirmada na fonte e recorrer ao agente para interpretar nomes abreviados e descrições desconhecidas. Registrar classe e, quando aplicável, indexador, vencimento e emissor. Conservar a origem de cada dado. Uma correspondência duvidosa fica sinalizada na carteira.
3. **Pesquisar ativos.** Ao encontrar um ativo novo ou informação desatualizada, buscar a documentação pertinente ao identificador, priorizando emissor, gestor, B3 ou CVM, conforme o instrumento. Guardar o link, a data de referência, a data da consulta e os campos que a fonte comprova. Reaproveitar a ficha entre importações. Informações incompatíveis ou indisponíveis ficam explícitas.
4. **Analisar.** Os especialistas usam as fichas e os valores conciliados para explicar variações, comparar cenários e discutir o aporte. Os cálculos de saldo, juros, parcelas e alocação são reproduzíveis no programa. A análise guarda a conclusão, a divergência relevante e as fontes.

Se a franquia acabar ou o servidor exigir novo login, preservar as tarefas pendentes e mostrar a última atualização válida. O desenho prevê retomar somente o trabalho que falta e avisar quando for necessária uma ação da pessoa. A troca para cobrança separada por API depende de escolha expressa. A pesquisa pública de um ativo usa o identificador do papel e preserva os dados pessoais da carteira.

## Radar do BTG

Módulo condicionado à confirmação de um canal oficial que permita ler as ofertas e enviar ordens automaticamente para a conta de pessoa física, inclusive com o computador pessoal desligado. O horário desejado para preparar o acesso é 9h50 de Brasília. A visibilidade das ofertas antes da abertura e a autenticação sem intervenção ainda precisam de confirmação.

O objetivo é comparar títulos de renda fixa do mercado secundário disponíveis nas instituições já utilizadas pela pessoa, considerando emissor, risco, prazo, indexador, liquidez e retorno líquido. Para compras automáticas no BTG, a pessoa separa uma verba e define teto por ordem, emissores e títulos autorizados, taxa mínima, prazo máximo e limite total por emissor. Uma ordem incerta mantém a verba reservada até a conciliação. O Telegram informa a oportunidade e, quando houver execução confirmada, o resultado.
