# avior launcher (arsip rilis Windows) — menjalankan CLI dari direktori
# arsipnya sendiri, tanpa perlu PATH. Dipakai: .\setup.ps1 <perintah> ...
$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
& (Join-Path $dir 'avior.exe') @args
exit $LASTEXITCODE
