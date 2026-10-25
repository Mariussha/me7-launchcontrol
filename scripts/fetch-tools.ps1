# Windows version of fetch-tools.sh. Run from the repo root:
#   powershell -ExecutionPolicy Bypass -File scripts\fetch-tools.ps1
$ErrorActionPreference = "Stop"
$ME7L_COMMIT = "89d0ff9a3908226efa1995da26e9148f766302f7"
$ME7SUM_VERSION = "v1.1.7"

Set-Location (Join-Path $PSScriptRoot "..")
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
New-Item -ItemType Directory $tmp | Out-Null
try {
    Write-Host "Fetching ME7Info + defs (nyetwurk/ME7L)..."
    Invoke-WebRequest "https://github.com/nyetwurk/ME7L/archive/$ME7L_COMMIT.zip" -OutFile "$tmp\me7l.zip"
    Expand-Archive "$tmp\me7l.zip" $tmp
    $src = "$tmp\ME7L-$ME7L_COMMIT"
    New-Item -ItemType Directory -Force assets\defs | Out-Null
    Copy-Item "$src\bin\ME7Info.exe" assets\ME7Info.exe
    Copy-Item "$src\defs\dist\me7_std.map" assets\defs\me7_std.map
    Copy-Item "$src\defs\dist\me7_alias.map" assets\defs\me7_alias.map

    Write-Host "Fetching ME7Sum $ME7SUM_VERSION..."
    Invoke-WebRequest "https://github.com/nyetwurk/ME7Sum/releases/download/$ME7SUM_VERSION/me7sum-$ME7SUM_VERSION-win.zip" -OutFile "$tmp\me7sum.zip"
    Expand-Archive "$tmp\me7sum.zip" "$tmp\me7sum"
    Copy-Item "$tmp\me7sum\me7sum.exe" assets\me7sum.exe

    Write-Host "Verifying checksums..."
    foreach ($line in Get-Content scripts\tools.sha256) {
        $hash, $file = $line -split '\s+', 2
        $actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLower()
        if ($actual -ne $hash) { throw "Checksum mismatch: $file" }
        Write-Host "  $file OK"
    }
    Write-Host "OK - tools are in assets\"
} finally {
    Remove-Item -Recurse -Force $tmp
}
