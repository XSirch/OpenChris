param([string]$BaseUrl='http://127.0.0.1:18480')
$ErrorActionPreference='Stop'
$session='openchris-smoke-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
function Invoke-Browser([string[]]$Arguments){
    $result = & agent-browser --session $session @Arguments
    if ($LASTEXITCODE -ne 0) { throw "agent-browser falhou: $($Arguments[0])" }
    return $result
}
try {
    & agent-browser skills get core | Out-Null
    # The first Windows daemon launch must not inherit a captured pipeline.
    & agent-browser --session $session open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível abrir o navegador local.' }
    Invoke-Browser @('snapshot','-i') | Out-Null
    Invoke-Browser @('fill','#password','demo-conselho') | Out-Null
    Invoke-Browser @('click','button[type=submit]') | Out-Null
    $current=Invoke-Browser @('get','url')
    if ($current.Trim() -ne "$BaseUrl/dashboard") { throw 'O login real em navegador não chegou ao dashboard.' }
    Invoke-Browser @('snapshot','-i') | Out-Null
    Write-Output 'PASS: formulário de login, origem, cookie e dashboard autenticado em navegador real.'
} finally {
    & agent-browser --session $session close | Out-Null
}
