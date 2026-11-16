param([string]$BaseUrl='http://127.0.0.1:18480')
$ErrorActionPreference='Stop'
$session='openchris-expenses-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$evidence=Join-Path $PSScriptRoot '../evidence/expenses'
[IO.Directory]::CreateDirectory($evidence) | Out-Null
function Browser([string[]]$Arguments) {
    $result=& agent-browser --session $session @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Falha no navegador: $($Arguments[0])" }
    return $result
}
function Assert-Page([string]$Expression,[string]$Message) {
    $result=Browser @('eval',"(async()=>Boolean(await ($Expression)))()")
    if ($result.Trim() -ne 'true') { throw $Message }
}
function Stable-Page {
    Browser @('eval','(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{})));return true})()') | Out-Null
    Browser @('snapshot','-i') | Out-Null
}
try {
    & agent-browser skills get core | Out-Null
    & agent-browser --session $session open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao abrir a entrada.' }
    Browser @('set','viewport','1440','1000') | Out-Null
    Browser @('snapshot','-i') | Out-Null
    Browser @('fill','#password','demo-conselho') | Out-Null
    Browser @('click','button[type=submit]') | Out-Null
    Browser @('wait','--fn','window.ConselhoWorkspace && window.ConselhoActivity && ConselhoOffice.getState().ready') | Out-Null
    Stable-Page
    $before=Browser @('eval','(async()=>JSON.stringify((await(await fetch("/api/state")).json()).snapshot))()') | Out-String
    Browser @('click','a[href="/expenses"]') | Out-Null
    Browser @('wait','--url','**/expenses') | Out-Null
    Stable-Page
    Browser @('click','.finance-source-switch a[href="/expenses?source=demo"]') | Out-Null
    Stable-Page
    Assert-Page 'document.querySelector(".expense-total>p").textContent==="R$ 5.524,50" && document.querySelectorAll(".expense-table tbody tr").length===12' 'Resumo ou histórico mensal incorreto.'
    Assert-Page 'document.querySelector(".expense-filters").method==="get"' 'A consulta deixou de funcionar como HTML sem scripts.'
    if (-not (Test-Path (Join-Path $evidence 'desktop.png'))) { Browser @('screenshot',(Join-Path $evidence 'desktop.png')) | Out-Null }
    Browser @('click','a[data-category=alimentacao]') | Out-Null
    Stable-Page
    Assert-Page 'document.querySelectorAll(".expense-table tbody tr").length===2 && document.querySelector(".expense-table tfoot td").textContent==="R$ 971,20" && document.querySelector(".expense-total>p").textContent==="R$ 5.524,50"' 'Filtro de categoria alterou o total do mês ou selecionou registros errados.'
    Browser @('select','#expense-category','saude') | Out-Null
    Browser @('select','#expense-status','pending') | Out-Null
    Browser @('fill','#expense-query','FARMACIA') | Out-Null
    Browser @('click','button[type=submit]') | Out-Null
    Stable-Page
    Assert-Page 'document.querySelectorAll(".expense-table tbody tr").length===1 && document.querySelector(".expense-description").textContent.includes("Farmácia") && document.querySelector(".expense-amount").textContent==="R$ 87,50"' 'Filtros combinados ou busca sem acento falharam.'
    $download=Join-Path $evidence 'gastos-filtrados.csv'
    # The local Chromium download writer also cancels a static-file control.
    # Validate the authenticated browser response; native disk saving is a separate QA limitation.
    $csv=Browser @('eval','(async()=>{const r=await fetch(document.querySelector(".expense-export").href);if(!r.ok || !r.headers.get("content-disposition").includes("attachment"))throw Error("CSV indisponível");return await r.text()})()') | Out-String | ConvertFrom-Json
    [IO.File]::WriteAllText($download,$csv,[Text.UTF8Encoding]::new($false))
    if (-not $csv.Contains('Farmácia do bairro') -or -not $csv.Contains('87,50') -or $csv.Contains('Aluguel')) { throw 'CSV não corresponde aos filtros da tela.' }
    Browser @('fill','#expense-query','inexistente') | Out-Null
    Browser @('click','button[type=submit]') | Out-Null
    Stable-Page
    Assert-Page 'document.querySelector(".expense-empty h3").textContent.includes("Nenhum lançamento")' 'Busca vazia sem orientação.'
    Browser @('click','.expense-clear') | Out-Null
    Stable-Page
    Browser @('focus','#expense-month') | Out-Null
    1..3 | ForEach-Object { Browser @('press','ArrowDown') | Out-Null }
    Browser @('click','button[type=submit]') | Out-Null
    Stable-Page
    Assert-Page 'document.querySelector(".expense-total>p").textContent==="Sem dados" && document.querySelector(".expense-empty h3").textContent==="Sem dados para este mês"' 'Mês ausente foi apresentado como gasto zero.'
    Browser @('click','.expense-empty a') | Out-Null
    Stable-Page
    Browser @('click','a[data-open-panel=activity]') | Out-Null
    Browser @('wait','--fn','window.ConselhoActivity && window.ConselhoWorkspace') | Out-Null
    Browser @('wait','--fn','!document.querySelector("#activity-loading").hidden || document.querySelectorAll("#activity-runs button").length>0 || !document.querySelector("#activity-empty").hidden') | Out-Null
    Browser @('wait','--fn','document.querySelector("#activity-loading").hidden') | Out-Null
    Assert-Page '(async()=>{const h=await(await fetch("/api/office/history")).json();return document.body.dataset.drawerOpen==="activity" && (h.runs.length===0?!document.querySelector("#activity-empty").hidden:document.querySelectorAll("#activity-runs button").length>0)})()' 'Retorno de Gastos abriu histórico sem carregar seus registros.'
    Browser @('press','Escape') | Out-Null
    Browser @('snapshot','-i') | Out-Null
    Browser @('click','a[data-open-panel=memory]') | Out-Null
    Assert-Page 'document.body.dataset.drawerOpen==="memory"' 'Menu do dashboard perdeu a abertura da memória.'
    Browser @('press','Escape') | Out-Null
    Browser @('click','a[href="/expenses"]') | Out-Null
    Stable-Page
    Browser @('click','.finance-source-switch a[href="/expenses?source=demo"]') | Out-Null
    Stable-Page
    Browser @('set','viewport','390','844') | Out-Null
    Stable-Page
    Browser @('screenshot',(Join-Path $evidence 'mobile.png')) | Out-Null
    Assert-Page 'document.documentElement.scrollWidth<=innerWidth && [...document.querySelectorAll(".expense-filters input,.expense-filters select")].every(e=>e.getBoundingClientRect().right<=innerWidth)' 'Filtros causaram overflow no celular.'
    Browser @('click','a[data-category=alimentacao]') | Out-Null
    Stable-Page
    Browser @('eval','document.querySelector(".expense-history").scrollIntoView({behavior:"instant",block:"start"})') | Out-Null
    Browser @('screenshot',(Join-Path $evidence 'mobile-history.png')) | Out-Null
    Assert-Page 'document.querySelectorAll(".expense-table tbody tr").length===2 && [...document.querySelectorAll(".expense-amount")].every(e=>e.getBoundingClientRect().right<=innerWidth)' 'Histórico ilegível no celular.'
    Browser @('set','viewport','320','740') | Out-Null
    Browser @('scrollintoview','a[data-open-panel=connections]') | Out-Null
    Assert-Page '(()=>{const r=document.querySelector("a[data-open-panel=connections]").getBoundingClientRect();return r.left>=0 && r.right<=innerWidth && r.width>=44 && document.documentElement.scrollWidth<=innerWidth})()' 'Último item do menu ficou inacessível em 320px.'
    Browser @('click','a[data-open-panel=connections]') | Out-Null
    Browser @('wait','--fn','window.ConselhoWorkspace && document.body.dataset.drawerOpen==="connections"') | Out-Null
    Stable-Page
    $after=Browser @('eval','(async()=>JSON.stringify((await(await fetch("/api/state")).json()).snapshot))()') | Out-String
    if ($before.Trim() -ne $after.Trim()) { throw 'Consultar gastos alterou dados persistidos da memória.' }
    Browser @('errors') | Write-Output
    Write-Output 'PASS: gastos, categorias, filtros combinados, CSV, vazios, navegação, histórico de atividades, 390px/320px e memória preservada.'
} finally {
    & agent-browser --session $session close | Out-Null
}
