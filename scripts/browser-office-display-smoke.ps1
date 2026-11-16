param([string]$BaseUrl='http://127.0.0.1:18481')
$ErrorActionPreference='Stop'
if (([Uri]$BaseUrl).Port -ne 18481 -or ([Uri]$BaseUrl).Host -notin @('127.0.0.1','localhost')) { throw 'Use somente a instância isolada de QA na porta 18481.' }
$session='office-display-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$editor=$session+'-editor'
$evidence=Join-Path $PSScriptRoot '../evidence/office-correction'
[IO.Directory]::CreateDirectory($evidence) | Out-Null
function Browser([string[]]$Arguments,[string]$Target=$session) {
    if($Arguments[0] -eq 'wait'){Write-Host ('Aguardando: '+$Arguments[-1])}
    $result=& agent-browser --session $Target @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Falha no navegador: $($Arguments -join ' ')" }
    return $result
}
function Assert-Page([string]$Expression,[string]$Message,[string]$Target=$session) {
    $result=Browser @('eval',"Boolean($Expression)") $Target
    if ("$result".Trim() -ne 'true') { throw $Message }
}
function Stable-Page([string]$Target=$session) {
    Browser @('eval','(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{})));return true})()') $Target | Out-Null
    Browser @('snapshot','-i') $Target | Out-Null
}
function Login([string]$Target) {
    & agent-browser --session $Target open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao abrir QA.' }
    Browser @('set','viewport','1600','1000') $Target | Out-Null
    Browser @('snapshot','-i') $Target | Out-Null
    Browser @('fill','#password','demo-conselho') $Target | Out-Null
    Browser @('click','button[type=submit]') $Target | Out-Null
    Browser @('wait','--fn','window.ConselhoOffice?.getState().ready && ConselhoOffice.getState().finance?.connection==="live"') $Target | Out-Null
    Stable-Page $Target
}
try {
    $qaEnvironment=& docker inspect openchris-finance-qa --format '{{json .Config.Env}}' | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or -not ($qaEnvironment -contains 'DATABASE_URL=postgres://conselho:synthetic-only@postgres:5432/conselho_ui_test?sslmode=disable')) { throw 'O container de QA deve usar conselho_ui_test.' }
    & agent-browser skills get core | Out-Null
    Login $session
    Login $editor
    $before=Browser @('eval','(async()=>await(await fetch("/api/finance/summary")).json())()') | Out-String | ConvertFrom-Json
    Assert-Page '(ConselhoOffice.getState().camera.scale>0 && document.getElementById("office-zoom").textContent==="100%")' 'O enquadramento inicial não mostra o escritório inteiro.'
    Assert-Page '!ConselhoOffice.getState().agents.some(a=>a.seatId)' 'Cadeiras ocupadas sem atividade.'
    $point=Browser @('eval','(async()=>{const m=await(await fetch("/static/office/manifest.json")).json(),s=m.world.financeScreen,c=ConselhoOffice.getState().camera,r=document.getElementById("office-canvas").getBoundingClientRect();return {x:Math.round(r.x+c.x+(s.x+s.width/2)*c.scale),y:Math.round(r.y+c.y+(s.y+s.height/2+s.width/2*s.shearY)*c.scale)}})()') | Out-String | ConvertFrom-Json
    Browser @('mouse','move',"$($point.x)","$($point.y)") | Out-Null
    Assert-Page 'document.getElementById("office-canvas").dataset.cursor==="pointer" && document.getElementById("office-canvas").title==="Ver painel financeiro"' 'O monitor não sinaliza interação.'
    Browser @('mouse','down') | Out-Null
    Browser @('mouse','up') | Out-Null
    Stable-Page
    Assert-Page '!document.getElementById("office-finance-panel").hidden && document.activeElement.id==="office-finance-title"' 'Clique no monitor não abriu o painel acessível.'
    Browser @('press','Escape') | Out-Null
    Assert-Page 'document.activeElement.id==="office-canvas"' 'Escape não devolveu foco ao escritório.'
    Browser @('click','#expand-office') | Out-Null
    Browser @('click','[data-open-panel=finance]') | Out-Null
    Stable-Page
    Assert-Page 'document.getElementById("office-viewport").dataset.expanded==="false"' 'O painel ficou sobreposto ao modo expandido.'
    Browser @('open',"$BaseUrl/expenses") $editor | Out-Null
    Stable-Page $editor
    Browser @('click','#new-expense>summary') $editor | Out-Null
    Browser @('fill','#new-expense-description','Regressão do painel na parede') $editor | Out-Null
    Browser @('fill','#new-expense-account','Carteira QA isolada') $editor | Out-Null
    Browser @('fill','#new-expense-amount','1,23') $editor | Out-Null
    Browser @('select','#new-expense-status','confirmed') $editor | Out-Null
    Browser @('select','#new-expense-category','10000000-0000-4000-8000-000000000006') $editor | Out-Null
    Browser @('click','#new-expense button[type=submit]') $editor | Out-Null
    Browser @('wait','--fn','document.querySelector(".finance-saved")?.textContent.includes("Gasto adicionado")') $editor | Out-Null
    $oldText=ConvertTo-Json -InputObject $before.expense_text -Compress
    Browser @('wait','--fn',"ConselhoOffice.getState().finance.expenses!==$oldText") | Out-Null
    Assert-Page 'ConselhoOffice.getState().finance.expenses===document.getElementById("office-finance-expenses").textContent && document.getElementById("live-expenses").textContent===document.getElementById("office-finance-expenses").textContent' 'Canvas, painel e resumo divergiram após o cadastro.'
    Assert-Page 'ConselhoOffice.getState().finance.wealth===document.getElementById("office-finance-wealth").textContent && ConselhoOffice.getState().finance.pending===document.getElementById("office-finance-pending").textContent && ConselhoOffice.getState().finance.category===document.getElementById("office-finance-category").textContent && ConselhoOffice.getState().finance.activity===document.getElementById("office-finance-activity").textContent' 'O painel não mostra todos os agregados.'
    Browser @('screenshot',(Join-Path $evidence 'panel-desktop.png')) | Out-Null
    Browser @('click','[data-edit-wealth]') | Out-Null
    Assert-Page 'document.getElementById("office-finance-panel").hidden && document.getElementById("net-worth-editor").open && document.activeElement.id==="net-worth-assets"' 'Atualizar patrimônio não levou ao formulário.'
    Browser @('fill','#net-worth-assets','5150,00') | Out-Null
    Browser @('fill','#net-worth-liabilities','1250,50') | Out-Null
    Browser @('click','#net-worth-editor button[type=submit]') | Out-Null
    Browser @('wait','--fn','ConselhoOffice.getState().finance.wealth==="R$ 3.899,50"') | Out-Null
    Stable-Page
    Browser @('screenshot',(Join-Path $evidence 'office-desktop.png')) | Out-Null
    Browser @('click','#expand-office') | Out-Null
    Stable-Page
    Browser @('screenshot',(Join-Path $evidence 'office-expanded.png')) | Out-Null
    Browser @('click','#expand-office') | Out-Null
    Browser @('set','viewport','390','844') | Out-Null
    Stable-Page
    Assert-Page 'document.documentElement.scrollWidth<=innerWidth' 'O escritório transborda no celular.'
    Browser @('screenshot',(Join-Path $evidence 'office-mobile.png')) | Out-Null
    Browser @('click','[data-open-panel=finance]') | Out-Null
    Stable-Page
    Assert-Page '!document.getElementById("office-finance-panel").hidden && document.documentElement.scrollWidth<=innerWidth' 'O painel móvel não abriu corretamente.'
    Browser @('screenshot',(Join-Path $evidence 'panel-mobile.png')) | Out-Null
    $errors=Browser @('errors') | Out-String
    if ($errors.Trim()) { throw "Erros de JavaScript: $errors" }
    $summary=[ordered]@{base_url=$BaseUrl;database='conselho_ui_test';monitor_click=$true;focus_restore=$true;initial_full_office=$true;seated=0;cross_session_expense_update=$true;wealth_update=$true;desktop=$true;mobile=$true;js_errors=0;qa_only=$true}
    $summary | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $evidence 'verification.json')
    $summary | ConvertTo-Json
} finally {
    & agent-browser --session $session close | Out-Null
    & agent-browser --session $editor close | Out-Null
}
