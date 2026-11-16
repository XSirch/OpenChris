param([string]$BaseUrl='http://127.0.0.1:18480')
$ErrorActionPreference='Stop'
$session='openchris-replay-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
function Browser([string[]]$Arguments) {
    $result=& agent-browser --session $session @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Falha no navegador: $($Arguments[0])" }
    return $result
}
function Assert-Page([string]$Expression,[string]$Message) {
    $result=Browser @('eval',"(async()=>Boolean(await ($Expression)))()")
    if ($result.Trim() -ne 'true') { throw $Message }
}
try {
    & agent-browser skills get core | Out-Null
    & agent-browser --session $session open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Falha ao abrir a entrada.' }
    Browser @('set','viewport','1440','1000') | Out-Null
    Browser @('fill','#password','demo-conselho') | Out-Null
    Browser @('click','button[type=submit]') | Out-Null
    Browser @('wait','--url','**/dashboard') | Out-Null
    Browser @('wait','--fn','window.ConselhoWorkspace && window.ConselhoActivity && ConselhoOffice.getState().ready') | Out-Null
    Browser @('click','.conversation-launcher') | Out-Null
    Browser @('wait','--fn','document.body.dataset.drawerOpen === "conversation"') | Out-Null
    Browser @('snapshot','-i') | Out-Null
    Browser @('fill','#message','Qual é minha reserva?') | Out-Null
    Browser @('click','#send') | Out-Null
    Browser @('wait','--fn','document.querySelector("#feedback").textContent.includes("Registrado.")') | Out-Null
    Browser @('press','Escape') | Out-Null
    Browser @('eval','(async()=>{const d=await (await fetch("/api/state")).json();window.__beforeReplay=JSON.stringify(d.snapshot);window.__replayMethods=[];const old=window.fetch;window.fetch=function(url,options){window.__replayMethods.push(options?.method||"GET");return old.call(this,url,options)}})()') | Out-Null
    Browser @('click','[data-open-panel=activity]') | Out-Null
    Browser @('select','#activity-agent','A23') | Out-Null
    Browser @('wait','--fn','!document.querySelector("#activity-empty").hidden') | Out-Null
    Assert-Page 'document.querySelector("#activity-empty-title").textContent.includes("Otto")' 'A ausência de atividade foi atribuída ao personagem errado.'
    Browser @('select','#activity-agent','A02') | Out-Null
    Browser @('wait','--fn','document.querySelectorAll("#activity-runs button").length>0') | Out-Null
    Browser @('click','#activity-runs li:first-child button') | Out-Null
    Browser @('wait','--fn','ConselhoActivity.getState().eventCount>0') | Out-Null
    Browser @('click','#activity-next') | Out-Null
    Browser @('click','#activity-next') | Out-Null
    Assert-Page 'ConselhoOffice.getState().replay?.active && ConselhoOffice.getState().agents.find(a=>a.id==="A02").state==="running"' 'A etapa histórica de início não apareceu no personagem.'
    Assert-Page 'document.querySelector("#tour-office").disabled && !ConselhoOffice.getState().agents.some(a=>a.path.length)' 'O replay permitiu deslocamentos sem registro.'
    Browser @('click','#activity-prev') | Out-Null
    Assert-Page 'ConselhoOffice.getState().agents.find(a=>a.id==="A02").state==="unobserved"' 'Retroceder conservou um estado de uma etapa futura.'
    Browser @('click','#activity-play') | Out-Null
    Browser @('wait','--fn','ConselhoActivity.getState().position===ConselhoActivity.getState().eventCount-1 && !ConselhoActivity.getState().playing') | Out-Null
    Browser @('click','#activity-live') | Out-Null
    Assert-Page 'ConselhoOffice.getState().replay===null && document.querySelector("#office-replay-banner").hidden' 'O retorno ao estado atual falhou.'
    Assert-Page 'ConselhoOffice.getState().agents.find(a=>a.id==="A02").state==="available"' 'O replay deixou o personagem em processamento.'
    Assert-Page '!window.__replayMethods.some(method=>method!=="GET")' 'A reprodução enviou uma operação de escrita.'
    Assert-Page '(async()=>JSON.stringify((await (await fetch("/api/state")).json()).snapshot)===window.__beforeReplay)()' 'A reprodução alterou memória, mensagens, fila ou cursor.'
    Browser @('press','Escape') | Out-Null
    Browser @('set','viewport','390','844') | Out-Null
    Browser @('click','[data-open-panel=activity]') | Out-Null
    Browser @('wait','--fn','document.querySelectorAll("#activity-runs button").length>0') | Out-Null
    Browser @('click','#activity-runs li:first-child button') | Out-Null
    Browser @('wait','--fn','ConselhoActivity.getState().eventCount>0') | Out-Null
    Browser @('click','#activity-play') | Out-Null
    Browser @('press','Escape') | Out-Null
    Assert-Page '!ConselhoActivity.getState().playing && document.querySelector("#activity-panel").hidden' 'Fechar o painel não pausou a reprodução.'
    Assert-Page 'document.documentElement.scrollWidth<=innerWidth && !document.querySelector("#office-replay-banner").hidden' 'O replay perdeu seu indicador ou causou rolagem horizontal.'
    Browser @('click','#exit-office-replay') | Out-Null
    Assert-Page 'ConselhoOffice.getState().replay===null' 'A saída do replay no celular falhou.'
    Write-Output 'PASS: filtro por personagem, etapas, avanço/retrocesso, reprodução, ausência de escrita, retorno ao vivo e celular.'
} finally {
    & agent-browser --session $session close | Out-Null
}
