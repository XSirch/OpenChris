$ErrorActionPreference='Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
    docker compose up --build -d --wait
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível iniciar a demonstração Docker.' }
    Write-Output 'Demonstração em http://127.0.0.1:18480. Senha sintética: demo-conselho.'
} finally { Pop-Location }
