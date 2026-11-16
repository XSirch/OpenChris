# Comparador local Go/Django

Este ensaio compara um contrato HTTP sintético em uma máquina. Django é um comparador pequeno; o aplicativo principal usa Go. Nenhum comando conecta contas, executa operações financeiras ou chama modelos pagos.

As quatro rotas autenticadas são `GET /bench/query`, `POST /bench/webhook`, `GET /bench/events` e `GET /bench/dashboard`. O token padrão de demonstração é `stack-benchmark-local`, enviado como `Authorization: Bearer ...`. `GET /healthz` executa `SELECT 1` sem autenticação.

O webhook aceita um objeto JSON com `event_id` e `text`, ambos strings não vazias, em um corpo de até 64 KiB. A confirmação ocorre após commit no PostgreSQL. O par `(backend,event_id)` impede duplicação; cada stack usa seu próprio backend. NUL e Unicode inválido são recusados. O SSE envia um snapshot com A01 a A23 em `idle` e fecha a conexão. Os dois servidores usam os bytes de `web/benchmark.html`.

## Ambiente e testes

Execute na raiz do projeto com Python 3.13. O ambiente virtual permanece no projeto. `requirements.lock.txt` registra todas as versões instaladas no Windows; `requirements.txt` fixa as dependências diretas.

```powershell
python -m venv bench/.venv
bench/.venv/Scripts/python.exe -m pip install -r bench/requirements.lock.txt
$env:TEST_DATABASE_URL = 'postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_test?sslmode=disable'
bench/.venv/Scripts/python.exe -m unittest discover -s bench/tests -v
```

Use uma base descartável real em `TEST_DATABASE_URL`. Os testes gravam eventos com UUIDs e preservam as tabelas existentes. Sem essa variável, os testes de PostgreSQL são marcados como ignorados; os testes do medidor continuam usando sockets HTTP e métricas reais do processo local.

## Servidor

```powershell
$env:DATABASE_URL = 'postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_demo?sslmode=disable'
bench/.venv/Scripts/python.exe -m bench.django_app
```

O servidor escuta somente em `127.0.0.1:18481`, com um worker Uvicorn e pool PostgreSQL persistente de uma a quatro conexões. Ele cria apenas a tabela `benchmark_events`, se ela não existir. A inicialização falha quando a base está indisponível. O Go usa o mesmo limite de quatro conexões no ensaio.

## Medição

Com os dois servidores disponíveis, informe os PIDs reais de cada processo. No Windows, o executável do ambiente virtual pode criar um processo Python filho: o medidor inclui descendentes.

```powershell
bench/.venv/Scripts/python.exe -m bench.measure `
  --go-url http://127.0.0.1:18480 `
  --django-url http://127.0.0.1:18481 `
  --go-pid 1234 --django-pid 5678 `
  --samples 100 --rounds 3 `
  --output bench/evidence/local-comparison.json
```

Substitua os PIDs de exemplo. O processo retorna código 1 quando há erro HTTP, resposta inválida ou falha de transporte, inclusive no aquecimento. O JSON mantém as tentativas e os erros.

Cada stack recebe 20 requisições de aquecimento por rota. As rodadas alternam a ordem dos backends; cada rota recebe 100 amostras por rodada por padrão, com concorrência 1 e HTTP/1.1 keep-alive. O webhook usa um UUID novo a cada chamada. p50/p95 usam nearest rank sobre todas as tentativas; erros têm contagem separada. Os tamanhos são bytes de corpo recebidos, sem cabeçalhos.

RSS é a soma da árvore de processos, amostrada a cada 20 ms. Memória compartilhada pode aparecer mais de uma vez e picos entre amostras podem passar despercebidos. CPU usa a diferença de tempo de usuário e sistema dividida pelo tempo da janela; o relatório inclui porcentagem por núcleo e normalizada pela quantidade de CPUs lógicas. Janelas curtas podem registrar CPU zero por resolução do sistema operacional. A janela ociosa padrão dura 500 ms. O PostgreSQL e o navegador ficam fora dessas métricas de processo.

Startup fica `null` até receber `--go-startup-ms` e/ou `--django-startup-ms`. Essas opções registram valores medidos externamente; descreva método e condição de cache com `--startup-notes`. O medidor não mede build nem limpa caches. A evidência declara essa limitação e separa o aquecimento das amostras medidas. A entrega não homologa produção, VPS, hardware diferente, carga concorrente, conversas com modelos ou conexões SSE longas.
