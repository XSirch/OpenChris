# Instalação em servidor

A demonstração local e a instalação de produção usam bancos e volumes separados. Use a demonstração para experimentar sem contas externas. Para os seus dados, configure uma origem HTTPS e uma senha exclusiva. O PostgreSQL e o runner Codex devem ficar na rede privada do aplicativo.

## Docker Compose

1. Instale Docker com Compose no servidor, clone o repositório e entre na pasta. Copie o arquivo de exemplo:

   ```sh
   cp .env.vps.example .env.vps
   ```

2. Edite `.env.vps`. Defina `PUBLIC_BASE_URL` como a origem HTTPS que vai servir o painel, sem caminho. Gere dois valores diferentes com `openssl rand -hex 32` e use um em `POSTGRES_PASSWORD` e outro em `APP_PASSWORD`. A senha do painel precisa ter pelo menos 20 caracteres. A senha hexadecimal do banco pode ser inserida na URL do Compose sem caracteres que precisem de escape.
3. Pare a demonstração se ela estiver usando a porta 18480. Inicie produção com as flags das integrações em `false`:

   ```sh
   docker compose --env-file .env.vps -f compose.vps.yaml up --build -d --wait
   curl --fail http://127.0.0.1:18480/readyz
   ```

4. Aponte o DNS do seu domínio para o servidor e configure o proxy HTTPS com certificado válido. Encaminhe as requisições para `127.0.0.1:18480` e mantenha o buffering desligado para os streams de eventos. O Compose não publica a porta do banco. [Exemplo de proxy Nginx](../deploy/nginx.conselho.conf.example)
5. Abra seu domínio em HTTPS, entre com a senha definida e confira o painel. O endpoint `/healthz` verifica a conexão com o banco. `/readyz` também verifica o schema. O aplicativo aplica as migrações necessárias na inicialização.

A cada alteração de variáveis, rode novamente o comando `up` correspondente. Para iniciar o Codex e ativar as demais integrações, siga o guia de serviços. [Guia de integrações](integrations.md)

## EasyPanel

1. Crie um serviço **Postgres** com armazenamento persistente dentro do projeto. Crie uma base exclusiva para o OpenChris. Use o endereço interno do banco na configuração do aplicativo.
2. Crie um serviço **App**. Em **Source → GitHub**, informe `XSirch/OpenChris`, branch `main` e caminho `/`. Selecione o builder Dockerfile e o arquivo `Dockerfile` da raiz.
3. Na configuração de ambiente do App, defina `APP_ENV=production`, `APP_PASSWORD`, `DATABASE_URL`, `PUBLIC_BASE_URL` e `LISTEN_ADDR=:8080`. Use a conexão PostgreSQL interna informada pelo serviço Postgres e mantenha os segredos no painel. Em caso de senha com caracteres reservados, use a codificação adequada na URL. As flags das integrações ficam desligadas até sua configuração.
4. Adicione o domínio HTTPS apontando para a porta interna 8080 do App e implante. Confira `/readyz` e o login pelo domínio. Para instalar o runner, use o fluxo privado do guia de integrações.

Referências do painel. [Serviço App](https://easypanel.io/docs/services/app) · [Serviço Postgres](https://easypanel.io/docs/services/postgres) · [Serviço Compose](https://easypanel.io/docs/services/compose)

## Backup, atualização e recuperação

O volume do PostgreSQL retém sessões, lançamentos e relatórios. O volume do runner retém o arquivo cifrado `account.enc`. Faça backup do banco e guarde a chave `CODEX_AUTH_KEY` separadamente da cópia do volume do runner. Trate os arquivos de backup como dados privados.

No EasyPanel, configure o backup do serviço Postgres e confira o resultado de cada execução. No Compose, um backup manual pelo shell de um servidor Linux pode ser feito assim:

```sh
umask 077
docker compose --env-file .env.vps -f compose.vps.yaml exec -T postgres \
  pg_dump -Fc -U conselho -d conselho > "conselho-$(date +%F-%H%M%S).dump"
```

Teste a restauração em uma base descartável com `pg_restore --exit-on-error`. Guarde cópias fora do servidor e confirme que o arquivo é legível antes de atualizar.

Para atualizar, garanta um backup recente, anote o commit ou a imagem anterior, obtenha a revisão desejada e repita o build. Mantenha os volumes e o ambiente. Confira a prontidão, o login e os estados das integrações. Para reverter uma mudança, restaure o código ou imagem compatível preservando o estado atual. Use o backup do banco para recuperação de dados, com validação em outra base antes de qualquer substituição. A remoção dos volumes apaga os dados locais.

## Se a página ou o painel não abrir

| Sintoma | Confira |
| --- | --- |
| O app encerra na inicialização | Veja os logs do App. Confira o endereço do banco, a senha exclusiva e a origem HTTPS. |
| O login não se mantém | Use o domínio definido em `PUBLIC_BASE_URL`. Confira HTTPS, a origem das requisições e os cabeçalhos do proxy. |
| Os dados aparecem após recarregar | Verifique o streaming SSE no proxy. As rotas de eventos precisam ficar abertas e sem buffer. |
| O deploy responde, mas uma integração não funciona | Abra **Conexões** e confira o estado do serviço. Cada provedor tem uma checagem própria. |
