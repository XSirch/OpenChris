# OpenChris

Aplicação pessoal de finanças em Python com Flask, SQLAlchemy e PostgreSQL. A interface usa HTML, CSS e JavaScript e foi desenhada para o navegador do celular. O escopo está em [PRODUCT.md](PRODUCT.md) e o design aprovado em [design/previas/v3-escuro-suave](design/previas/v3-escuro-suave).

## EasyPanel

Crie **um serviço do tipo Compose**, escolha **Inline** e cole o conteúdo de [dist/openchris/compose.yaml](dist/openchris/compose.yaml). Esse arquivo já contém a aplicação. Um deploy sobe o site, o PostgreSQL e o processo que sincroniza dados e executa os agentes. No domínio HTTPS, selecione o destino interno **web** na porta **8000**. Só o site recebe tráfego público. [Guia de Compose do EasyPanel](https://easypanel.io/docs/services/compose)

No editor de ambiente, ative **Create .env file**. Defina `OPENCHRIS_PASSWORD` com pelo menos 12 caracteres e uma senha diferente em `POSTGRES_PASSWORD`. Configure `PUBLIC_BASE_URL` com o endereço HTTPS do site para os links do Telegram e o retorno do banco. Use o domínio também na lista de redirecionamentos autorizados da Pluggy. O arquivo `.env.example` traz as demais opções. O primeiro deploy começa com uma carteira vazia.

Os volumes do Compose guardam o PostgreSQL, a chave da sessão do site e o login do Codex. Mantenha o mesmo nome de serviço nas atualizações e inclua o banco na política de backup do servidor. O banco da instalação anterior pode permanecer separado. A senha do PostgreSQL é usada para inicializar o volume na primeira execução. Para trocá-la depois, altere a senha da conta no banco e o valor configurado no painel.

Para atualizar a versão Inline após uma alteração no código, execute `python tools/export_compose.py`, substitua o conteúdo no mesmo serviço e faça o deploy. Se o projeto estiver hospedado em Git, a origem **Git** também aceita o `compose.yaml` da raiz, com pasta de build `/`.

## Conexões e importações

Com `PLUGGY_CLIENT_ID` e `PLUGGY_CLIENT_SECRET` configurados, abra **Mais → Conexões bancárias** para autorizar uma instituição. Para reaproveitar um consentimento existente da mesma conta Pluggy, informe seu identificador em `PLUGGY_ITEM_IDS`. O worker consulta essas conexões ao iniciar e volta a consultá-las a cada hora. A atualização depende dos dados e dos produtos que a instituição entrega. Os saldos em outra moeda aparecem separados do total em reais.

Na Carteira ou em Gastos, **Importar extrato** oferece o cabeçalho CSV para download. Use UTF-8 e datas no formato `AAAA-MM-DD`. O importador aceita vírgula ou ponto e vírgula como separador. Para inserir valores como `1.234,56`, use ponto e vírgula. A importação é transacional: uma linha inválida cancela o arquivo inteiro e indica a linha que precisa de correção.

Use um `external_id` estável em cada linha, especialmente se houver duas compras iguais no mesmo dia. O código original da Pluggy e o código com prefixo `pluggy:` identificam o mesmo registro. O sistema também procura correspondências entre o banco e o CSV pela posição ou pelos dados da compra. Quando a correspondência for ambígua, indique o identificador da linha correta ou use `separate=sim` para uma operação realmente distinta.

## Documentos

Em **Mais → Documentos**, guarde PDFs ou arquivos TXT em UTF-8 de até 5 MB. Declare o tipo e, se souber, o ano dos dados. Um PDF digitalizado ou protegido por senha pode ser guardado e baixado, mas fica indisponível para consulta até que você envie uma versão com texto legível.

Ative **Permitir no chat** para que perguntas relevantes possam consultar trechos do arquivo. Você também pode escolher um documento específico ao perguntar. O servidor busca o texto localmente e passa ao Codex os trechos selecionados. As respostas e suas referências ficam no histórico.

Configure uma chave [Fernet](https://cryptography.io/en/latest/fernet/) em `OPENCHRIS_DOCUMENT_KEY` no ambiente do Compose. Para gerar uma, use `python -c "from cryptography.fernet import Fernet; print(Fernet.generate_key().decode())"`. O site e o worker usam a mesma chave para abrir os arquivos e seus índices criptografados no banco. Guarde a chave separadamente do backup do PostgreSQL e mantenha seu valor nas atualizações.

## Agentes e alertas

No console do contêiner **worker**, execute `codex login --device-auth` e conclua a autorização no site oficial da OpenAI. Confira com `codex login status`. A autenticação fica no volume privado do worker. O chat financeiro e as rotinas automáticas usam a franquia do Codex da conta ChatGPT do titular. A fila aguarda quando a sessão ou a franquia exigem uma pausa. `ASSET_SOURCE_HOSTS` aceita domínios exatos de emissores ou gestores a incluir na pesquisa. [Autenticação do Codex](https://developers.openai.com/codex/auth)

Em **Mais → Agentes e pesquisas**, toque no agente para escolher seu modelo e esforço de raciocínio. A lista vem do Codex instalado no servidor e mostra os níveis aceitos por cada modelo. A escolha fica no banco e vale a partir da próxima tarefa. Se preferir que o Codex decida, selecione **Padrão do Codex**.

Na primeira instalação, gastos usam `gpt-6-luna` com esforço baixo e a classificação de investimentos usa o mesmo modelo com esforço médio. Pesquisa de ativos, economia e síntese usam `gpt-6.1-sol` com esforço médio. A revisão de risco usa `gpt-6-astra` com esforço alto. Se uma combinação estiver ausente no primeiro catálogo, aquele agente começa no padrão do Codex.

A agenda usa divulgações do IBGE e comunicados do Banco Central. Os especialistas analisam os dados em sequência e a conclusão conserva as evidências e o debate. O calendário antecipado do Copom está cadastrado até dezembro de 2027. Para receber avisos, configure `TELEGRAM_ENABLED=1`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID` e `PUBLIC_BASE_URL`. A compra automática de renda fixa no BTG depende da confirmação de um canal oficial para a conta PF.

## Validação local

Instale as dependências em um ambiente virtual com `python -m pip install -r requirements.txt pytest` e execute `python -m pytest -q tests`. Os testes usam dados sintéticos. Para validar o pacote, preencha uma cópia de `.env.example` em `.env` e rode `docker compose config --quiet`, depois `docker compose up --build -d`. A aplicação responde em `/readyz` na porta interna 8000. No EasyPanel, o domínio faz o encaminhamento para essa porta.
