# OpenChris · CONSELHO

OpenChris é um aplicativo de finanças pessoais para instalar no seu próprio servidor. Ele reúne gastos, patrimônio, investimentos e objetivos em um painel privado. Com o MeuPluggy, importa dados bancários autorizados. Com o Codex, produz pareceres que você pode consultar e comparar. O Telegram leva resumos e links para o painel. Você também pode usar o aplicativo só para registrar seus gastos e patrimônio.

A instalação é individual. A integração bancária consulta os dados autorizados pelo titular. O aplicativo não executa PIX, pagamentos, transferências, compras, vendas ou resgates.

## Sumário

- [O que você pode fazer](#o-que-você-pode-fazer)
- [Experimentar no computador](#experimentar-no-computador)
- [Instalar no servidor](#instalar-no-servidor)
- [Conectar os serviços](#conectar-os-serviços)
- [Primeiros passos](#primeiros-passos)
- [Como o conselho trabalha](#como-o-conselho-trabalha)
- [Arquitetura e testes](#arquitetura-e-testes)
- [Licença](#licença)

## O que você pode fazer

| Área | Recursos |
| --- | --- |
| Gastos | Registrar despesas e categorias, filtrar lançamentos por mês, categoria ou situação e exportar CSV. Com o Pluggy, acompanhar gastos importados, revisar classificações ambíguas e reaplicar uma correção a descrições iguais. |
| Patrimônio e investimentos | Informar ativos e dívidas manualmente e consultar investimentos bancários por instituição e moeda. Cada fonte tem cobertura e data próprias. |
| Perfil e objetivos | Registrar metas, reserva, compromissos para os próximos 30 dias, mínimo em conta, valores protegidos, tolerância a risco, experiência e necessidade de liquidez. O painel indica o que falta preencher ou revisar. |
| Saldo e sugestões | Estimar o que sobra em contas correntes em reais após os compromissos e valores protegidos. Uma análise pessoal pode citar produtos e valores de aporte quando houver perfil confirmado, dados bancários recentes e fontes suficientes. |
| Relatórios e conversa | Enviar perguntas a um especialista com o Codex ativo. Consultar respostas, fontes, hipóteses, riscos, limitações e o histórico de rodadas. No celular, o resumo aparece antes do arquivo de pareceres. |
| Memória e atividade | Buscar episódios, consultar preferências registradas e ver o histórico de atividade. A reprodução visual revisita etapas que já aconteceram. |
| Rotina automática | Coletar fontes públicas, avaliar finanças pessoais, acompanhar estratégias e enviar avisos pelo Telegram. Consulte o horário da próxima rodada, a cota e o botão **Pausar** em **Acompanhamento automático**. |
| Escritório | Explorar a cena em pixel art, consultar perfis e acompanhar o trabalho dos personagens a partir das tarefas registradas. |

Os lançamentos manuais ficam separados dos importados. A classificação aproveita categorias do banco e regras do aplicativo. Transferências próprias, pagamentos de fatura, aplicações e resgates ficam fora do total de gastos quando identificados. O painel sinaliza totais parciais e valores desconhecidos para evitar comparações enganosas.

## Experimentar no computador

Instale Git e Docker com o plugin Docker Compose. No Windows e no macOS, você pode usar o Docker Desktop com containers Linux. Na raiz do repositório, execute:

```sh
git clone https://github.com/XSirch/OpenChris.git
cd OpenChris
docker compose up --build -d --wait
```

Abra [a demonstração local](http://127.0.0.1:18480) e entre com a senha `demo-conselho`. Por padrão, o aplicativo escuta na porta 18480 e o PostgreSQL na 18432, ambos restritos ao computador local.

Experimente **Gastos** e alterne entre **Meus lançamentos** e **Demonstração**. Para testar a memória entre canais, envie “Minha reserva é de 6 meses” em **Telegram simulado** e pergunte “Qual é minha reserva?” no **Dashboard**. O simulador funciona sem contas externas e usa respostas determinísticas.

Use `docker compose stop` para parar e `docker compose up -d` para voltar. O volume `memory_data` guarda os registros entre execuções. Reserve a senha de demonstração para o ambiente local.

## Instalar no servidor

Você precisa de um servidor com Docker Compose, uma origem HTTPS e um PostgreSQL em rede privada. O servidor exige uma senha própria para o painel e inicializa o schema automaticamente. A configuração de exemplo mantém as integrações desligadas, de modo que você pode começar pelos lançamentos manuais.

O guia de instalação cobre os comandos do Compose, a alternativa pelo EasyPanel, os volumes, a checagem de prontidão e o procedimento para backup e atualização. [Instalação em servidor](docs/installation.md)

## Conectar os serviços

| Serviço | O que fazer |
| --- | --- |
| MeuPluggy | Autorize os bancos no MeuPluggy, habilite o conector pessoal no Pluggy Dashboard e configure o Client ID e o Client Secret no servidor. No painel, abra **Conexões → Bancos e investimentos → Conectar instituição** para concluir pelo widget. |
| Telegram | Crie um bot pelo BotFather. Configure o token e os IDs numéricos da sua conta e do chat privado com o bot. Em **Conexões**, use **Registrar webhook do Telegram**, depois envie `/ajuda` no chat. |
| Codex | Suba o runner privado com seu volume de autenticação, configure uma chave de criptografia e um segredo compartilhado com o app, e conclua o login de dispositivo no runner. Em **Conexões**, confira a disponibilidade da IA. |

Configure as credenciais só no ambiente privado do servidor. O guia traz os nomes das variáveis, comandos de login, endereços de webhook e formas de diagnosticar os estados exibidos. [Guia de integrações](docs/integrations.md)

## Primeiros passos

1. Abra **Conexões** e confira a data de atualização na instituição e a última importação pelo aplicativo. Em **Investimentos**, filtre por instituição e estado para entender o que o banco compartilhou.
2. Em **Gastos**, escolha a origem **Pluggy** para ver lançamentos importados ou **Meus lançamentos** para os manuais. Use **Revisar classificação** onde a natureza do movimento estiver incorreta ou indefinida. O CSV usa a origem e os filtros da página.
3. Abra **Meu perfil**. No celular, o acesso fica no menu **Mais**. Preencha objetivos, reserva, horizonte, experiência, liquidez e os valores que você quer manter protegidos. Use zero quando tiver conferido que o valor é zero. Um campo vazio continua desconhecido. Confirme os dados após preenchê-los e revise a confirmação após mudanças ou 30 dias.
4. Consulte **Painel financeiro** e **Relatórios** para ver o saldo estimado e os links para cada pendência. A reserva já aplicada orienta a proposta e fica separada do cálculo do saldo em conta.
5. Com o Codex disponível, escreva na conversa, por exemplo, “Davi, quais compromissos pesam mais sobre o saldo disponível?”. A resposta fica no histórico. Para acompanhar uma rodada automática, abra **Relatórios** e selecione um item em **Histórico de análises**. Leia o resumo, as condições e os pareceres antes de avaliar uma sugestão.
6. Em **Acompanhamento automático**, consulte a agenda, solicite uma rodada ou pause as próximas etapas e avisos. A pausa visual do escritório é um controle separado.

## Como o conselho trabalha

O escritório tem 23 personagens. Você pode selecioná-los na cena ou na lista acessível pelo teclado. Em repouso, circulam pelo espaço e fazem pausas. Durante trabalho individual registrado, o responsável usa um computador. Na revisão conjunta, os autores envolvidos usam a mesa e o balão distingue quem está trabalhando de quem tem um parecer em revisão. Os balões mostram etapas e estados do sistema. Com a preferência por movimento reduzido ativa, a interface preserva a seleção, a posição e o texto. O histórico visual permite voltar a uma rodada sem executá-la novamente.

Atlas atende à conversa por padrão. Começar uma pergunta pelo nome ou ID de um especialista direciona o pedido para aquele perfil. Mnema e Vigia representam funções de memória e segurança aplicadas pelo servidor em produção. Na demonstração, Atlas e Mnema participam do fluxo local de memória.

| Personagens | Responsabilidades |
| --- | --- |
| Atlas, Mnema, Nina e Vigia | Coordenação, memória, qualidade dos dados e segurança. |
| Cora, Maia, Élio, Davi e Helena | Gastos, objetivos, planejamento, liquidez e patrimônio. |
| Teseu, Bento, Íris, Lira e Orion | Estratégias, longo prazo, cenários táticos, renda fixa e macroeconomia. |
| Sol, Kira, Dante e Vera | Exposição internacional, criptoativos, risco e questões jurídicas ou tributárias. |
| Lia, Sofia, Caio, Prisma e Otto | Comportamento, prioridades pessoais, crítica, relatórios e avaliação dos resultados. |

Na rotina automática, um especialista pesquisa o tema, outro formula a estratégia e Dante avalia o risco. Caio examina a proposta separadamente e Atlas consolida os pareceres. Na rodada pessoal, Lira pesquisa renda fixa e Davi formula a proposta. As etapas registram fontes, premissas e lacunas. Uma rodada concluída ainda pode indicar que faltam dados para recomendar um aporte.

O servidor roda as tarefas com o navegador fechado. As finanças pessoais têm prioridade no ciclo da manhã, a partir das 7h em São Paulo, e após mudanças importantes nos dados. As rodadas gerais usam a cota restante. O orçamento padrão é de 30 tentativas totais por dia, com até 20 automáticas. Se a rotina e o Telegram estiverem ativos, o resumo financeiro pode ser enviado a partir das 8h, horário de São Paulo. A fila respeita as condições de envio, a pausa e a autorização do chat.

O catálogo descreve áreas de atuação. A versão atual cobre a conversa por especialista e o fluxo de cinco etapas. Gestão geral de documentos e memória semântica, múltiplos usuários, corretagem e execução de ordens estão fora da versão publicada.

## Arquitetura e testes

O navegador recebe páginas HTML do servidor Go, com CSS e JavaScript sem etapa de compilação do frontend. O escritório usa Canvas 2D. Eventos do servidor (SSE) atualizam a cena e os agregados financeiros. O PostgreSQL guarda sessões, lançamentos, importações, memória, filas e pareceres. Os conectores usam as APIs oficiais. O Codex CLI roda em outro container, em rede privada, com um volume exclusivo para o login cifrado.

| Caminho | Conteúdo |
| --- | --- |
| [cmd](cmd/) e [internal](internal/) | Servidor, runner, finanças, memória, integrações e conselho. |
| [web](web/) e [assets](assets/) | Interface, fontes, manifestos e arte do escritório. |
| [migrations](migrations/) e [schemas](schemas/) | Migrações PostgreSQL e contratos do projeto. |
| [deploy](deploy/) | Configurações de servidor, proxy e runner. |
| [scripts](scripts/) | Verificação de encoding, testes JavaScript e roteiros locais. |
| [bench](bench/) | Comparador local Go/Django em Python, separado do aplicativo. |

O build usa a versão Go fixada no Dockerfile. Para validar com as mesmas dependências da CI, instale Go 1.27.3, Node.js 24, Python 3.13 e PostgreSQL 17. Sem bancos de teste configurados, parte dos testes de integração Go fica pendente.

```sh
go test ./...
go vet ./...
node --test 'scripts/test-*.cjs'
python scripts/check_encoding.py
```

Com PowerShell 7, `./scripts/test.ps1` prepara oito bancos sintéticos e executa os testes Go. Rode o roteiro em um ambiente local de teste. A configuração de CI também prepara esses bancos e executa os testes do comparador. Para conferir o login em navegador, inicie a demonstração, instale o agent-browser e execute `./scripts/browser-smoke.ps1`. [Configuração da CI](.github/workflows/ci.yml) · [Comparador](bench/README.md)

## Licença

O código do OpenChris é distribuído sob a licença MIT. As fontes Manrope e Instrument Serif mantêm as respectivas licenças SIL Open Font License. O runner baixa o Codex CLI de uma versão publicada pela OpenAI. [Licença MIT](LICENSE) · [Manrope](web/fonts/manrope-OFL.txt) · [Instrument Serif](web/fonts/instrument-serif-OFL.txt)
