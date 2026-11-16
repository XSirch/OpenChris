param([string]$BaseUrl='http://127.0.0.1:18481')
$ErrorActionPreference='Stop'
if (([Uri]$BaseUrl).Port -ne 18481 -or ([Uri]$BaseUrl).Host -notin @('127.0.0.1','localhost')) { throw 'Use a instância de QA isolada na porta 18481; este roteiro cadastra dados de teste.' }
$session='openchris-manual-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$watch=$session+'-watch'
$evidence=Join-Path $PSScriptRoot '../evidence/manual-finance'
[IO.Directory]::CreateDirectory($evidence) | Out-Null
$suffix=[Guid]::NewGuid().ToString('N').Substring(0,6)
$categoryName='Mercado QA '+$suffix
$renamed='Casa QA '+$suffix
function Browser([string[]]$Arguments,[string]$Target=$session) {
    $result=& agent-browser --session $Target @Arguments
    if ($LASTEXITCODE -ne 0) { if($Arguments[0] -eq 'wait'){throw "Falha aguardando: $($Arguments[-1])"}; throw "Falha no navegador: $($Arguments[0])" }
    return $result
}
function Assert-Page([string]$Expression,[string]$Message,[string]$Target=$session) {
    $result=Browser @('eval',"(async()=>Boolean(await ($Expression)))()") $Target
    if ($result.Trim() -ne 'true') { throw $Message }
}
function Stable-Page([string]$Target=$session) {
    Browser @('eval','(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{})));return true})()') $Target | Out-Null
    Browser @('snapshot','-i') $Target | Out-Null
}
function Login([string]$Target) {
    & agent-browser --session $Target open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao abrir login de QA.' }
    Browser @('set','viewport','1440','1000') $Target | Out-Null
    Browser @('snapshot','-i') $Target | Out-Null
    Browser @('fill','#password','demo-conselho') $Target | Out-Null
    Browser @('click','button[type=submit]') $Target | Out-Null
    Browser @('wait','--fn','window.ConselhoOffice && ConselhoOffice.getState().ready && document.getElementById("finance-live-state").textContent==="Ao vivo"') $Target | Out-Null
    Stable-Page $Target
}
function Add-Expense([string]$Amount,[string]$State,[string]$Description,[string]$CategoryId) {
    Browser @('click','#new-expense>summary') | Out-Null
    Browser @('fill','#new-expense-description',$Description) | Out-Null
    Browser @('fill','#new-expense-amount',$Amount) | Out-Null
    Browser @('select','#new-expense-category',$CategoryId) | Out-Null
    Browser @('select','#new-expense-status',$State) | Out-Null
    Browser @('fill','#new-expense-account','Carteira QA isolada') | Out-Null
    Browser @('click','#new-expense button[type=submit]') | Out-Null
    Browser @('wait','--fn','document.querySelector(".finance-saved")?.textContent.includes("Gasto adicionado")') | Out-Null
    Stable-Page
}
try {
    $qaEnvironment=& docker inspect openchris-finance-qa --format '{{json .Config.Env}}' | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or -not ($qaEnvironment -contains 'DATABASE_URL=postgres://conselho:synthetic-only@postgres:5432/conselho_ui_test?sslmode=disable')) { throw 'O container de QA não está configurado para conselho_ui_test.' }
    & docker compose exec -T postgres psql -U conselho -d conselho_ui_test -v ON_ERROR_STOP=1 -c 'TRUNCATE finance_audit,finance_transactions,finance_net_worth,finance_categories RESTART IDENTITY CASCADE' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao preparar o banco isolado de QA.' }
    Get-Content (Join-Path $PSScriptRoot '../migrations/002_finance.sql') -Raw | docker compose exec -T postgres psql -U conselho -d conselho_ui_test -v ON_ERROR_STOP=1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao preparar as categorias de QA.' }
    & agent-browser skills get core | Out-Null
    Login $session
    Login $watch
    Assert-Page 'document.querySelector("#live-net-worth").textContent==="Não informado"' 'A conta de QA recebeu patrimônio fictício.' $watch
    $before=Browser @('eval','(async()=>JSON.stringify((await(await fetch("/api/state")).json()).snapshot))()') | Out-String
    Browser @('click','a[href="/expenses"]') | Out-Null
    Stable-Page
    Browser @('click','#category-manager>summary') | Out-Null
    Browser @('fill','#new-category-name',$categoryName) | Out-Null
    Browser @('click','form[aria-label="Criar categoria"] button[type=submit]') | Out-Null
    Browser @('wait','--fn','document.querySelector(".finance-saved")?.textContent.includes("Categoria salva")') | Out-Null
    Stable-Page
    $categoryId=Browser @('eval',"[...document.querySelectorAll('#new-expense-category option')].find(o=>o.textContent==='$categoryName').value") | Out-String | ConvertFrom-Json
    Add-Expense '100,50' 'confirmed' 'Mercado de teste' $categoryId
    Browser @('wait','--fn','document.getElementById("live-expenses").textContent==="R$ 100,50"') $watch | Out-Null
    Assert-Page "document.getElementById('live-top-category').textContent==='$categoryName'" 'Maior categoria não atualizou na outra sessão.' $watch
    Add-Expense '40,25' 'pending' 'Compra pendente de teste' $categoryId
    Browser @('wait','--fn','document.getElementById("live-pending").textContent==="R$ 40,25"') $watch | Out-Null
    Assert-Page 'document.getElementById("live-expenses").textContent==="R$ 100,50"' 'Pendência alterou o gasto confirmado.' $watch
    Browser @('click','#category-manager>summary') | Out-Null
    Browser @('fill',"#category-name-$categoryId",$renamed) | Out-Null
    Browser @('eval',"document.getElementById('category-color-$categoryId').value='#efbd72'") | Out-Null
    Browser @('click',"form:has(#category-name-$categoryId) button[type=submit]") | Out-Null
    Browser @('wait','--fn','document.querySelector(".finance-saved")?.textContent.includes("Categoria salva")') | Out-Null
    Stable-Page
    Browser @('wait','--fn',"document.getElementById('live-top-category').textContent==='$renamed'") $watch | Out-Null
    Assert-Page '(async()=>{const r=await(await fetch("/api/expenses")).json();return r.result_count===2 && r.total_cents===10050 && r.transactions.every(t=>t.category_name.startsWith("Casa QA"))})()' 'Renomear perdeu o vínculo dos gastos.'
    Browser @('click','#net-worth-editor>summary') $watch | Out-Null
    Browser @('fill','#net-worth-assets','5000,00') $watch | Out-Null
    Browser @('fill','#net-worth-liabilities','1250,50') $watch | Out-Null
    Browser @('click','#net-worth-editor button[type=submit]') $watch | Out-Null
    Browser @('wait','--fn','document.getElementById("live-net-worth").textContent==="R$ 3.749,50"') $watch | Out-Null
    Stable-Page $watch
    Browser @('screenshot',(Join-Path $evidence 'office-desktop.png')) $watch | Out-Null
    Assert-Page '!ConselhoOffice.getState().agents.some(a=>a.seatId)' 'Cadeiras ocupadas sem atividade.' $watch
    Browser @('set','media','dark','reduced-motion') $watch | Out-Null
    Assert-Page '!ConselhoOffice.getState().agents.some(a=>a.roaming)' 'O passeio continuou em movimento reduzido.' $watch
    Browser @('click','.finance-source-switch a[href="/expenses?source=demo"]') | Out-Null
    Stable-Page
    Assert-Page 'document.querySelector(".expense-total>p").textContent==="R$ 5.524,50" && !document.querySelector("#new-expense")' 'Dados fictícios foram misturados aos cadastros pessoais.'
    Browser @('click','.finance-source-switch a[href="/expenses?source=manual"]') | Out-Null
    Stable-Page
    Browser @('set','viewport','390','844') | Out-Null
    Browser @('click','#new-expense>summary') | Out-Null
    Browser @('screenshot',(Join-Path $evidence 'expense-mobile.png')) | Out-Null
    Assert-Page 'document.documentElement.scrollWidth<=innerWidth && [...document.querySelectorAll("#new-expense input:not([type=hidden]),#new-expense select")].every(e=>e.getBoundingClientRect().right<=innerWidth)' 'Formulário de gasto excedeu a tela móvel.'
    Browser @('a11y','--json') | Set-Content -Encoding utf8 (Join-Path $evidence 'a11y-expenses.json')
    Browser @('set','viewport','390','844') $watch | Out-Null
    Browser @('screenshot',(Join-Path $evidence 'office-mobile.png')) $watch | Out-Null
    $after=Browser @('eval','(async()=>JSON.stringify((await(await fetch("/api/state")).json()).snapshot))()') | Out-String
    if($before.Trim() -ne $after.Trim()){throw 'Cadastro financeiro alterou memória ou fila.'}
    Browser @('errors') | Write-Output
    Browser @('errors') $watch | Write-Output
    Write-Output 'PASS: cadastro, categorias, vínculos, dois navegadores ao vivo, patrimônio, cadeira/cursor, rotina, fontes isoladas e celular.'
} finally {
    & agent-browser --session $session close | Out-Null
    & agent-browser --session $watch close | Out-Null
}
