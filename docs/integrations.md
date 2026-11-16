# Conectar MeuPluggy, Telegram e Codex

As três integrações são opcionais e exigem produção com HTTPS. No Compose, preencha `.env.vps`. No EasyPanel, use o ambiente privado do serviço, seguindo a separação entre aplicação e runner. Os segredos de webhook e o token do runner precisam ter pelo menos 32 caracteres. Gere cada um com uma execução separada de `openssl rand -hex 32`.

## MeuPluggy

1. Crie ou acesse sua conta no Meu Pluggy. Autorize cada banco na interface oficial e confirme quais dados ele compartilha.
2. Entre no Pluggy Dashboard com a mesma conta. Na aplicação que vai usar, verifique se o conector pessoal **MeuPluggy** aparece no widget. Copie o Client ID e o Client Secret da aplicação.
3. Configure `PLUGGY_CLIENT_ID`, `PLUGGY_CLIENT_SECRET` e seu `PLUGGY_WEBHOOK_SECRET`. O intervalo `PLUGGY_SYNC_INTERVAL` aceita de `1h` a `24h` e começa em `6h`. Defina `PLUGGY_ENABLED=true` e aplique o ambiente.
4. Em **Conexões → Bancos e investimentos**, clique em **Registrar webhook da Pluggy** quando o botão estiver disponível. O app usa `/webhooks/pluggy` no seu domínio e autentica a notificação por segredo.
5. Clique em **Conectar instituição**, selecione **MeuPluggy** no widget e conclua a autorização. A aplicação gera um token de curta duração para o widget. Se você já autorizou uma conexão na mesma aplicação Pluggy, a opção de referência permite recuperar seu `itemId`.
6. Confira as instituições, transações e investimentos em **Consultar dados sincronizados**. Use **Gastos** e **Investimentos** para o acompanhamento diário.

O fluxo pessoal tem limite de cinco conexões do titular. **Sincronizar dados** importa o que já está disponível na API. Se a data da instituição estiver antiga, abra o MeuPluggy e consulte o estado da conexão na origem. Em caso de autorização vencida, renove no MeuPluggy e volte ao painel. O app mantém a última importação válida identificada pela data e pela cobertura. Dados parciais ou antigos podem impedir uma sugestão de aporte.

Fontes oficiais. [MeuPluggy](https://meu.pluggy.ai) · [Pluggy Dashboard](https://dashboard.pluggy.ai) · [Uso pessoal com MeuPluggy](https://docs.pluggy.ai/pt/docs/guides/meu-pluggy-personal-use) · [Webhooks](https://docs.pluggy.ai/en/reference/webhooks)

## Telegram

1. Envie `/newbot` ao BotFather e guarde o token como `TELEGRAM_BOT_TOKEN`. Abra uma conversa privada com o bot criado e envie `/start`.
2. Identifique os campos `message.from.id` e `message.chat.id` de uma mensagem sua ao bot. Use o primeiro em `TELEGRAM_ALLOWED_USER_ID` e o segundo em `TELEGRAM_ALLOWED_CHAT_ID`. Use os IDs do seu usuário e da conversa privada. O ID do bot serve para identificar a conta do bot e não deve ocupar nenhum desses campos.
3. Configure `TELEGRAM_WEBHOOK_SECRET` com um valor exclusivo e defina `TELEGRAM_ENABLED=true`. Depois de aplicar, abra **Conexões → Telegram → Registrar webhook do Telegram**. O app registra `/webhooks/telegram` no seu domínio.
4. Envie `/ajuda` do chat permitido e veja se chega a resposta. O painel mostra o estado do webhook e os horários de recebimento e envio.

Se você ainda não conhece os IDs, consulte `getUpdates` para o bot novo antes de registrar o webhook. Em Bash, com `curl` e `jq`, esta consulta lê o token sem exibi-lo e imprime apenas os dois IDs de mensagens privadas:

```bash
read -rsp 'Token do bot: ' bot_token
printf '\n'
printf 'url = "https://api.telegram.org/bot%s/getUpdates"\n' "$bot_token" |
  curl --silent --show-error --fail --config - |
  jq -r '.result[] | .message? | select(.chat.type == "private") | [.from.id, .chat.id] | @tsv'
unset bot_token
```

O Telegram oferece `/start`, `/ajuda`, `/resumo`, `/gastos`, `/patrimonio` e `/memoria`. O servidor aceita somente o chat privado e o usuário configurados. Com o Codex disponível, o texto livre pode gerar uma análise no dashboard e uma notificação com link para leitura. Com a autonomia ativa, o resumo financeiro fica elegível a partir das 8h de São Paulo e usa a fila de mensagens sem consumir inferência. A fila registra resultados e evita repetição automática se a entrega ficou incerta.

Se o webhook estiver registrado mas o bot não responder, confira os dois IDs, o chat autorizado e a saúde da origem HTTPS. A API `getUpdates` só funciona quando não há webhook ativo. [BotFather](https://t.me/BotFather) · [Telegram Bot API](https://core.telegram.org/bots/api#getupdates)

## Codex

O aplicativo chama um runner privado por HTTP. O runner usa o Codex CLI com uma sessão própria da conta ChatGPT. A disponibilidade da conta, do modelo e a cota do provedor continuam aplicáveis. A aplicação não tem fallback para a API paga.

| Variável | Serviço e finalidade |
| --- | --- |
| `CODEX_RUNNER_TOKEN` | Mesmo valor no app e no runner. Gere com `openssl rand -hex 32`. |
| `CODEX_AUTH_KEY` | Só no runner. Gere uma chave exclusiva com `openssl rand -base64 32`. |
| `CODEX_RUNNER_URL` | No app. O Compose usa `http://conselho-codex:8081`. No EasyPanel, use o alias privado da configuração do runner. |
| `CODEX_ENABLED` | No app. Ative após o login inicial do runner. |
| `CODEX_DAILY_LIMIT` | No app. Total diário entre 1 e 30, padrão 30. |
| `AUTONOMY_ENABLED` | No app. Ativa a rotina automática e requer o Codex habilitado. |
| `AUTONOMY_DAILY_LIMIT` | No app. Limite da rotina entre 1 e 20, padrão 20, dentro do total diário. Deixe espaço para as consultas manuais. |

A imagem do runner usa uma distribuição do CLI para Linux x86-64 e valida o arquivo pelo SHA-256. O volume `/state` guarda a autenticação cifrada. Use a mesma chave após recriar o container e mantenha o serviço sem porta pública.

### No Compose

Com o token e a chave já configurados em `.env.vps`, deixe `CODEX_ENABLED=false` para a primeira inicialização do runner:

```sh
docker compose --env-file .env.vps -f compose.vps.yaml --profile ai up --build -d --wait conselho-codex
docker compose --env-file .env.vps -f compose.vps.yaml --profile ai exec conselho-codex /app/codex-runner login
```

Siga a URL oficial indicada no terminal e informe o código temporário. Se o login por dispositivo estiver desabilitado, verifique as permissões da sua conta ou workspace. Após o login, defina `CODEX_ENABLED=true` e reaplique os serviços:

```sh
docker compose --env-file .env.vps -f compose.vps.yaml --profile ai up -d --wait
```

### No EasyPanel

1. Na máquina do EasyPanel, obtenha a mesma revisão do código e construa a imagem local do runner na raiz do repositório:

   ```sh
   docker build -f deploy/Dockerfile.codex-runner.source -t easypanel/app/conselho-codex:latest .
   ```

2. No mesmo projeto do App, crie um serviço **Compose** com o conteúdo de `deploy/compose.codex-easypanel.yaml`. Habilite a criação do arquivo `.env` desse serviço para interpolar o token e a chave. Preserve o volume do runner e o alias na rede privada. [Configuração do runtime](../deploy/compose.codex-easypanel.yaml)
3. No console desse container, execute `/app/codex-runner login` e conclua o código na URL oficial indicada. No App, configure o mesmo token, `CODEX_RUNNER_URL=http://conselho-codex-internal:8081` e `CODEX_ENABLED=true`. Aplique a configuração e confira **Conexões**.

### Conferir o estado e ativar a rotina

Em **Conexões**, confira se a IA aparece como disponível. **Aguardando login** indica que falta concluir a autenticação no runner. **Ocupado** indica uso de um recurso que aceita uma operação por vez. Um bloqueio por política ou armazenamento exige verificar os logs e o volume antes de executar `/app/codex-runner recover` no console do runner. Se a sessão foi invalidada, faça login novamente. O healthcheck do container verifica o processo. A disponibilidade em **Conexões** inclui uma checagem do runner, e uma análise de teste confirma a execução efetiva.

Para permitir rodadas automáticas, defina `AUTONOMY_ENABLED=true` após conferir a configuração do Codex e aplique o ambiente. Use **Acompanhamento automático** para acompanhar a cota, pausar ou retomar. A pesquisa usa temas e fontes públicas. O contexto privado usa os dados disponíveis para o parecer, com agregados e sem descrições de transações ou identificadores de conta. [Autenticação oficial do Codex](https://learn.chatgpt.com/docs/auth)
