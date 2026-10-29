@echo off
rem avior launcher (arsip rilis Windows) — menjalankan CLI dari direktori
rem arsipnya sendiri, tanpa perlu PATH. Dipakai: setup.bat <perintah> ...
"%~dp0avior.exe" %*
exit /b %ERRORLEVEL%
