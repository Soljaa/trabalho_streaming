# Demonstração do parent_unreachable: um pai vivo que os filhos não alcançam.
#
#   powershell -ExecutionPolicy Bypass -File scripts\demo-firewall.ps1
#
# Roteiro: sobe o tracker, a fonte (n1) e o n2 com -firewall (ele manda ping
# normalmente, mas não abre a porta para os filhos). Depois entram n3, n4, n5
# e n6, um a cada 3 s. Quem cai debaixo do n2 tenta se conectar 3 vezes, manda
# parent_unreachable e o tracker o muda de lugar. Os logs ficam em demo-logs\.

$ErrorActionPreference = "Stop"
$proj = Split-Path $PSScriptRoot -Parent
$logs = Join-Path $proj "demo-logs"
New-Item -ItemType Directory -Force $logs | Out-Null
Get-ChildItem $logs | Remove-Item -Force

Set-Location $proj
go build -o bin/ ./cmd/...

function Start-Node($name, $exe, $argList) {
    Start-Process -FilePath (Join-Path $proj "bin\$exe") -ArgumentList $argList -NoNewWindow -PassThru `
        -RedirectStandardError (Join-Path $logs "$name.log") -RedirectStandardOutput (Join-Path $logs "$name.out")
}

$procs = @()
$procs += Start-Node "tracker" "tracker.exe" "-addr 127.0.0.1:8000"
Start-Sleep -Milliseconds 800
$procs += Start-Node "n1-fonte" "peer-sim.exe" "-role source -port 9001"
Start-Sleep -Milliseconds 500
$procs += Start-Node "n2-firewall" "peer-sim.exe" "-port 9002 -firewall"
Start-Sleep -Milliseconds 500
$procs += Start-Node "n3" "peer-sim.exe" "-port 9003"
foreach ($i in 4..6) {
    Start-Sleep -Milliseconds 500
    $procs += Start-Node "n$i" "peer-sim.exe" "-port 900$i"
    Start-Sleep -Seconds 3   # tempo para as 3 tentativas e o aviso
}

foreach ($p in $procs) { if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force } }
Start-Sleep -Milliseconds 300

foreach ($f in @("tracker", "n4", "n6")) {
    Write-Output "===================== $f ====================="
    Get-Content (Join-Path $logs "$f.log") -Encoding utf8
}
