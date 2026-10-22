# Demonstração do tracker com processos reais, um por nó.
#
#   powershell -ExecutionPolicy Bypass -File scripts\demo.ps1
#
# Roteiro: sobe o tracker (timeout de heartbeat de 6 s), a fonte e 5 peers;
# o n6 entra sem mandar ping e é derrubado por timeout; depois o processo do
# n2 é morto (queda) e os filhos dele são religados; por fim a fonte é morta
# e todos recebem stream_ended. Os logs de cada processo ficam em demo-logs\.

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

$tracker = Start-Node "tracker" "tracker.exe" "-addr 127.0.0.1:8000 -heartbeat-timeout 6s"
Start-Sleep -Milliseconds 800
$src = Start-Node "n1-fonte" "peer-sim.exe" "-role source -port 9001"
Start-Sleep -Milliseconds 500
$n2 = Start-Node "n2" "peer-sim.exe" "-port 9002"
Start-Sleep -Milliseconds 500
$n3 = Start-Node "n3" "peer-sim.exe" "-port 9003"
Start-Sleep -Milliseconds 500
$n4 = Start-Node "n4" "peer-sim.exe" "-port 9004"
Start-Sleep -Milliseconds 500
$n5 = Start-Node "n5" "peer-sim.exe" "-port 9005"
Start-Sleep -Milliseconds 500
$n6 = Start-Node "n6-mudo" "peer-sim.exe" "-port 9006 -heartbeat 0"
Start-Sleep -Seconds 8

Write-Output "matando o n2 (queda sem leave)..."
Stop-Process -Id $n2.Id -Force
Start-Sleep -Seconds 2

Write-Output "matando a fonte..."
Stop-Process -Id $src.Id -Force
Start-Sleep -Seconds 2

foreach ($p in @($tracker, $n3, $n4, $n5, $n6)) { if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force } }
Start-Sleep -Milliseconds 300

foreach ($f in @("tracker", "n1-fonte", "n4")) {
    Write-Output "===================== $f ====================="
    Get-Content (Join-Path $logs "$f.log") -Encoding utf8
}
