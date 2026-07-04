@echo off
REM Mini-Tun Asymmetric diagnostic — runs locally, needs NO internet. Captures DNS/firewall
REM state while the VPN is connected, into vpndiag-out.txt. Run AS ADMINISTRATOR.
REM
REM HOW TO USE:
REM   1) Start Mini-Tun Asymmetric.exe as admin and CONNECT (leave it connected).
REM   2) Then run THIS file as admin (right-click -> Run as administrator).
REM   3) While it runs (~45s), do one speedtest.net test.
REM   4) Disconnect the VPN. Tell the assistant "готово".
REM
set OUT=%~dp0vpndiag-out.txt
echo ============================================================ > "%OUT%"
echo Mini-Tun Asymmetric DIAG  %DATE% %TIME% >> "%OUT%"
echo ============================================================ >> "%OUT%"

echo. >> "%OUT%"
echo ===== [1] DNS kill-switch firewall rule (expect: rule present) ===== >> "%OUT%"
netsh advfirewall firewall show rule name=MiniTunAsymmetric-DNS-Killswitch >> "%OUT%" 2>&1

echo. >> "%OUT%"
echo ===== [2] DNS servers per adapter (expect tunnel adapter = 10.8.0.1) ===== >> "%OUT%"
netsh interface ip show dnsservers >> "%OUT%" 2>&1

echo. >> "%OUT%"
echo ===== [3] admin check (expect: shows admin groups, no access-denied) ===== >> "%OUT%"
net session >> "%OUT%" 2>&1

echo. >> "%OUT%"
echo ===== SNAPSHOT A ===== >> "%OUT%"
echo --- resolve ip.sb (REAL = good, 198.18.x = LEAK) --- >> "%OUT%"
nslookup ip.sb >> "%OUT%" 2>&1
echo --- resolve 2ip.io --- >> "%OUT%"
nslookup 2ip.io >> "%OUT%" 2>&1
echo --- active 198.18.x (fake) connections (expect NONE) --- >> "%OUT%"
netstat -n | findstr "198.18." >> "%OUT%" 2>&1

ping -n 16 127.0.0.1 >nul

echo. >> "%OUT%"
echo ===== SNAPSHOT B (after ~15s of speedtest) ===== >> "%OUT%"
nslookup ip.sb >> "%OUT%" 2>&1
netstat -n | findstr "198.18." >> "%OUT%" 2>&1

ping -n 16 127.0.0.1 >nul

echo. >> "%OUT%"
echo ===== SNAPSHOT C (after ~30s) ===== >> "%OUT%"
nslookup ip.sb >> "%OUT%" 2>&1
netstat -n | findstr "198.18." >> "%OUT%" 2>&1

echo. >> "%OUT%"
echo ===== [4] CLEAN THROUGHPUT (curl, fresh resolve, NO browser cache) ===== >> "%OUT%"
echo --- download 50MB from Cloudflare via tunnel --- >> "%OUT%"
curl.exe -s -o NUL -w "speed=%%{speed_download} bytes/s  time=%%{time_total}s  http=%%{http_code}\n" --max-time 40 "https://speed.cloudflare.com/__down?bytes=52428800" >> "%OUT%" 2>&1
echo --- second run --- >> "%OUT%"
curl.exe -s -o NUL -w "speed=%%{speed_download} bytes/s  time=%%{time_total}s  http=%%{http_code}\n" --max-time 40 "https://speed.cloudflare.com/__down?bytes=52428800" >> "%OUT%" 2>&1

echo. >> "%OUT%"
echo ===== DONE ===== >> "%OUT%"
echo Diagnostic written to: %OUT%
echo You can disconnect the VPN now and tell the assistant.
pause
