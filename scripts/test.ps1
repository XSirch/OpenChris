$ErrorActionPreference='Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
    $go=Join-Path $PWD '.tools\go\bin\go.exe'
    if (-not (Test-Path -LiteralPath $go)) { $go=(Get-Command go -ErrorAction Stop).Source }
    $env:GOPATH=Join-Path $PWD '.tools\gopath'
    $env:GOCACHE=Join-Path $PWD '.tools\gocache'
    $env:GOMODCACHE=Join-Path $PWD '.tools\gomodcache'
    docker compose up -d --wait postgres
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL de teste indisponível.' }
    foreach($database in @('conselho_test','conselho_http_test','conselho_finance_test','conselho_telegram_test','conselho_pluggy_test','conselho_integrations_test','conselho_council_test','conselho_autonomy_test')) {
        $exists=[string](docker compose exec -T postgres psql -U conselho -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$database'")
        if ($LASTEXITCODE -ne 0) { throw 'Não foi possível inspecionar os bancos de teste.' }
        if ("$exists".Trim() -ne '1') { docker compose exec -T postgres createdb -U conselho $database; if ($LASTEXITCODE -ne 0) {throw 'Não foi possível criar o banco de teste.'} }
    }
    $env:TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_test?sslmode=disable'
    $env:AUTONOMY_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_autonomy_test?sslmode=disable'
    $env:COUNCIL_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_council_test?sslmode=disable'
    $env:HTTP_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_http_test?sslmode=disable'
    $env:FINANCE_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_finance_test?sslmode=disable'
    $env:TELEGRAM_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_telegram_test?sslmode=disable'
    $env:PLUGGY_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_pluggy_test?sslmode=disable'
    $env:INTEGRATIONS_TEST_DATABASE_URL='postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_integrations_test?sslmode=disable'
    & $go test ./... -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'A suíte Go falhou.' }
    & $go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet encontrou problemas.' }
} finally { Pop-Location }
