param([string]$BaseUrl='http://127.0.0.1:18480')
$ErrorActionPreference='Stop'
$session='openchris-pixel-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
function Browser([string[]]$Arguments) {
    $result=& agent-browser --session $session @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Falha no navegador: $($Arguments[0])" }
    return $result
}
function Assert-Page([string]$Expression,[string]$Message) {
    $result=Browser @('eval',"Boolean($Expression)")
    if ($result.Trim() -ne 'true') { throw $Message }
}
try {
    & agent-browser skills get core | Out-Null
    & agent-browser --session $session open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao abrir a entrada local.' }
    Browser @('set','viewport','1440','1000') | Out-Null
    Browser @('snapshot','-i') | Out-Null
    Browser @('fill','#password','demo-conselho') | Out-Null
    Browser @('click','button[type=submit]') | Out-Null
    Browser @('wait','--url','**/dashboard') | Out-Null
    Browser @('wait','--fn','window.ConselhoWorkspace && window.ConselhoActivity && window.ConselhoOffice && ConselhoOffice.getState().ready') | Out-Null
    Assert-Page 'ConselhoOffice.getState().count===23 && document.querySelectorAll("#agent-roster [data-agent]").length===23' 'Faltam personagens no escritório.'
    Assert-Page 'document.querySelector("#office-viewport").clientHeight<innerHeight && document.documentElement.scrollWidth<=innerWidth' 'A cena excedeu a janela.'
    Browser @('click','[data-open-panel=agents].rail-item') | Out-Null
    Browser @('snapshot','-i') | Out-Null
    Browser @('click','#agent-roster [data-agent=A23]') | Out-Null
    Assert-Page 'ConselhoOffice.getState().selected==="A23" && document.querySelector("#agent-name").textContent==="Otto"' 'O último agente não abre seu próprio personagem.'
    Browser @('wait','--fn','ConselhoOffice.getState().agents.some(a=>a.roaming && a.path.length>0)') | Out-Null
    Browser @('click','#pause-office') | Out-Null
    Assert-Page 'ConselhoOffice.getState().paused && !ConselhoOffice.getState().renderLoopActive' 'A pausa não suspendeu o motor.'
    Browser @('click','#pause-office') | Out-Null
    Browser @('click','[data-open-panel=conversation].conversation-launcher') | Out-Null
    Browser @('press','Escape') | Out-Null
    Assert-Page 'document.querySelector("#conversation-panel").hidden && !document.querySelector("#agent-panel").hidden' 'Escape fechou a camada errada.'
    Browser @('click','#agent-close') | Out-Null
    Browser @('click','[data-open-panel=scenario].button-primary') | Out-Null
    Browser @('click','#scenario-panel [data-prompt]') | Out-Null
    Browser @('press','Escape') | Out-Null
    Assert-Page 'document.activeElement.getClientRects().length>0 && document.querySelector("#conversation-panel").hidden' 'O foco voltou a um controle oculto.'
    Browser @('click','#view-history') | Out-Null
    Browser @('click','[data-open-panel=memory].rail-item') | Out-Null
    Browser @('click','#tab-episodes') | Out-Null
    Assert-Page 'document.querySelector("#view-history").getAttribute("aria-selected")==="true"' 'As abas de memória alteraram a visão financeira.'
    Browser @('fill','#search','viagem') | Out-Null
    Browser @('click','#search-form button') | Out-Null
    Browser @('press','Escape') | Out-Null
    Browser @('click','[data-open-panel=conversation].conversation-launcher') | Out-Null
    Browser @('check','input[value=telegram]') | Out-Null
    Browser @('fill','#message','Qual é minha reserva?') | Out-Null
    Browser @('click','#send') | Out-Null
    Browser @('wait','--fn','document.querySelector("#feedback").textContent.includes("Registrado.")') | Out-Null
    Assert-Page 'document.querySelector("#messages li:last-child").textContent.includes("preferência ativa de reserva")' 'A consulta entre canais não recuperou a preferência.'
    Browser @('press','Escape') | Out-Null
    Browser @('set','viewport','390','844') | Out-Null
    Assert-Page 'document.documentElement.scrollWidth<=innerWidth' 'Há rolagem horizontal no celular.'
    Browser @('set','media','dark','reduced-motion') | Out-Null
    Browser @('click','[data-open-panel=agents].rail-item') | Out-Null
    Browser @('click','#agent-roster [data-agent=A06]') | Out-Null
    Assert-Page 'ConselhoOffice.getState().reducedMotion && !ConselhoOffice.getState().renderLoopActive && !ConselhoOffice.getState().agents.find(a=>a.id==="A06").path.length' 'Movimento reduzido não preservou o destino sem animação.'
    Write-Output 'PASS: login, 23 personagens, caminhada, pausa, foco, camadas, abas, memória entre canais e celular.'
} finally {
    & agent-browser --session $session close | Out-Null
}
