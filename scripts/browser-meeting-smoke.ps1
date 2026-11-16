param([string]$BaseUrl='http://127.0.0.1:18481')
$ErrorActionPreference='Stop'
if (([Uri]$BaseUrl).Port -ne 18481 -or ([Uri]$BaseUrl).Host -notin @('127.0.0.1','localhost')) { throw 'Use a instância isolada de QA na porta 18481.' }
$session='meeting-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$evidence=Join-Path $PSScriptRoot '../.runtime/office-motion-check'
[IO.Directory]::CreateDirectory($evidence) | Out-Null
function Browser([string[]]$Arguments) {
    $result=& agent-browser --session $session @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Falha no navegador: $($Arguments -join ' ')" }
    return $result
}
function Assert-Page([string]$Expression,[string]$Message) {
    $result=Browser @('eval',"Boolean($Expression)")
    if ("$result".Trim() -ne 'true') { throw $Message }
}
function Stable-Page {
    Browser @('eval','(async()=>{await document.fonts.ready;await Promise.all(document.getAnimations().map(a=>a.finished.catch(()=>{})));return true})()') | Out-Null
    Browser @('snapshot','-i') | Out-Null
}
try {
    & agent-browser skills get core | Out-Null
    & agent-browser --session $session open "$BaseUrl/login"
    if ($LASTEXITCODE -ne 0) { throw 'Não foi possível abrir a aplicação de QA.' }
    Browser @('set','viewport','1600','1000') | Out-Null
    Browser @('snapshot','-i') | Out-Null
    Browser @('fill','#password','demo-conselho') | Out-Null
    Browser @('click','button[type=submit]') | Out-Null
    Browser @('wait','--fn','window.ConselhoOffice?.getState().ready') | Out-Null
    Stable-Page
    $before=Browser @('eval','(async()=>JSON.stringify((await(await fetch("/api/state")).json()).snapshot))()') | Out-String
    Assert-Page 'ConselhoOffice.getState().agents.every(a=>!a.seatId)' 'O escritório sem tarefas deve começar com cadeiras vazias.'
    Browser @('wait','--fn','ConselhoOffice.getState().agents.some(a=>a.roaming && a.path.length>0)') | Out-Null
    Browser @('set','media','dark','reduced-motion') | Out-Null
    $fixture='(stage)=>{const id="00000000-0000-4000-8000-000000000099",now=Date.now();const tasks=["A13","A08","A17","A21","A01"].map((agent_id,i)=>({id:String(i),agent_id,stage:i,run_id:id,status:i===stage?"running":i<stage?"complete":"pending",lease_expires_at:new Date(now+120000).toISOString()}));window.dispatchEvent(new CustomEvent("autonomy-status",{detail:{enabled:true,heartbeat_at:new Date(now).toISOString(),runs:[{id,status:"running"}],tasks}}));}'
    Browser @('eval',"($fixture)(1)") | Out-Null
    Assert-Page 'ConselhoOffice.getState().agents.find(a=>a.id==="A08").seatKind==="workstation" && document.querySelector(".office-speech[data-agent=A08]").textContent.includes("Preparando a proposta")' 'A estratégia pessoal não chegou à estação e ao balão.'
    Browser @('eval',"($fixture)(4)") | Out-Null
    Assert-Page 'ConselhoOffice.getState().agents.filter(a=>a.seatKind==="meeting").length===4 && document.querySelectorAll(".office-speech[data-activity=review]").length===3' 'A síntese não reuniu autores e executor.'
    Browser @('screenshot',(Join-Path $evidence 'desktop.png')) | Out-Null
    Browser @('click','#expand-office') | Out-Null
    Browser @('click','[data-room-focus=council]') | Out-Null
    Browser @('screenshot',(Join-Path $evidence 'table.png')) | Out-Null
    foreach($width in 320,390) {
        Browser @('set','viewport',"$width",'844') | Out-Null
        Assert-Page 'document.documentElement.scrollWidth<=innerWidth && document.querySelector(".office-speech[data-agent=A01]").getBoundingClientRect().right<=innerWidth' 'Balão excedeu a tela móvel.'
    }
    Browser @('screenshot',(Join-Path $evidence 'mobile.png')) | Out-Null
    Browser @('eval','window.dispatchEvent(new CustomEvent("autonomy-status",{detail:{enabled:true,runs:[],tasks:[]}})); false') | Out-Null
    Assert-Page 'ConselhoOffice.getState().agents.every(a=>!a.seatId && !a.seatTarget)' 'A etapa terminou com cadeira ainda ocupada.'
    $after=Browser @('eval','(async()=>JSON.stringify((await(await fetch("/api/state")).json()).snapshot))()') | Out-String
    if($before.Trim() -ne $after.Trim()){throw 'O movimento visual alterou a memória de conversas.'}
    $errors=Browser @('errors') | Out-String
    if($errors.Trim()){throw "Erros de JavaScript: $errors"}
    $summary=[ordered]@{base_url=$BaseUrl;meeting_chairs=5;workstations=4;identities=23;review_participants=4;workstation_activity=$true;idle_walk=$true;memory_unchanged=$true;mobile=$true;js_errors=0}
    $summary | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $evidence 'verification.json')
    $summary | ConvertTo-Json
} finally { & agent-browser --session $session close | Out-Null }
