# Mini-Tun Asymmetric

**English** · [Русская версия ниже ↓](#mini-tun-asymmetric--русская-версия)

A self-written **asymmetric tunnel** and network-research project, built on the
**BadRouting** protocol. Three roles — **client** (Flutter desktop + Linux CLI),
**master** (entry + internet egress) and **slave** (return-path downlink) — where the
uplink and downlink take *different* network paths (hence "Asymmetric"). It's an
experiment in split-path transport and flow separation — the kind of thing you build
to *understand* how networks look at traffic. Purely academic, of course. 😉

```
                 uplink
   Client ----------------> Master ----> internet
      ^                       |
      |                       |
      +------- Slave <--------+
             downlink
```

Uplink and downlink never share a path: the client sends *to* the master, but
receives *from* a slave. The two directions are separate flows on separate hops —
that's the whole idea.

> [!WARNING]
> **Early alpha — proof of concept.** This is a working proof of concept, published
> to share the *idea and architecture* — not a finished product. It exists for
> research and educational purposes: exploring transport-protocol design, NAT
> traversal, and asymmetric routing. Expect rough edges, breaking changes, and
> unaudited security; don't rely on it for anything real. The question this release
> asks is simply: **does the approach make sense?**
>
> Use it only on networks and systems you own or are authorized to test, and in
> accordance with the laws of your jurisdiction. The author provides it as-is, with
> no warranty, and takes no responsibility for how others use it. What you take away
> from the design is entirely up to you. 🙂

## Highlights

- **Asymmetric routing** — uplink `client → master`, downlink `master → slave → client`
  via UDP NAT hole-punching. The return path leaves from a *different* IP than the
  entry, so the two directions are separate flows rather than one bidirectional
  session — the core research idea of the project.
- **Nearest-node selection** — the master hands the client the full list of live
  slaves; the client RTT-probes each (authenticated ping/pong) and pins the closest
  for its downlink, cutting the latency of the triple-hop path. Falls back to
  round-robin for older clients (versioned handshake, backward-compatible).
- **Pluggable transport carriers** — every UDP packet is framed according to a
  selectable carrier format; the carrier is chosen per session (client-side
  auto-race + fallback, or manual). An experiment in how interchangeable the
  on-wire framing of a transport can be — and, purely coincidentally, in how much
  a packet on the wire ends up looking like a game session, a torrent, or a video
  call. 🙃
  - `cs2` — Source Engine / CS2 game datagram framing
  - `utp` — BitTorrent µTP (BEP-29) framing
  - `webrtc` — STUN + RTP framing
  - `quic` — QUIC / HTTP3 framing on :443 (symmetric variant)
- **Custom ports** — the carrier's native control port is the default, but a profile
  can dial any operator-configured port, with comma-separated fallback. The master
  demuxes the carrier by arrival port.
- **Dynamic firewall (server)** — an nftables-backed subsystem (`common/netfw`,
  falls back to firewalld/ufw) opens each ephemeral per-session data port on demand
  and closes it when the session ends — so the box never exposes a wide port range.
  It owns a tagged table and reconciles its own leftovers on startup.
- **In-tunnel DNS** — a DNS resolver on the gateway plus a client-side DNS
  kill-switch and IPv6 block, so name resolution stays consistent inside the tunnel
  regardless of whatever *creative* answers the local network might otherwise decide
  to hand back. 🙂
- **Authenticated handshake** — the auth token never appears on the wire (HMAC
  over a timestamped nonce); stale/replayed handshakes are rejected silently.
- **Encryption** — ChaCha20-Poly1305 AEAD, per-session key bound to the tunnel IP.
- **gVisor netstack egress** — the master terminates client TCP/UDP in a userspace
  stack and proxies byte streams to the internet, so any TCP works.

## Layout

| Path | What |
|---|---|
| `client-flutter/` | Flutter desktop client (Windows; Android scaffold) — the UI |
| `agent/` + `cmd/mini-tun-asymmetric-agent/` | privileged local sidecar the GUI drives over loopback (owns the VPN engine, self-elevates via UAC) |
| `client-windows/` | the shared client engine `vpncore` (used by agent, CLI, Android) |
| `cmd/mini-tun-asymmetric-cli/` | headless Linux CLI client |
| `master/` | master node — netstack egress, in-tunnel DNS, sessions, control, metrics |
| `slave/` | slave node — downlink relay |
| `mobile/` | gomobile binding of the core for Android |
| `server-tui/` | nmtui-style server manager TUI |
| `common/` | shared packages — transport carriers, crypto, protocol, tun, config, netfw |
| `tools/datapathtest/` | end-to-end data-path smoke test (handshake, RTT probe, full path) |

## Install

**Linux server (.deb / .rpm)** — one package ships both roles; pick master or
slave after install:

```sh
sudo dnf install ./mini-tun-asymmetric-<ver>-1.x86_64.rpm    # RHEL/CentOS/Fedora
sudo apt install ./mini-tun-asymmetric_<ver>_amd64.deb        # Debian/Ubuntu
sudo mta-setup                                                # choose role, generate token, start
```

`mta-setup` → **Quick Setup Wizard** → *Master* or *Slave*. Master generates the
auth token, TLS cert, config, opens the firewall, and starts the service; copy
the token to the slaves and the client. Slave asks for the master host + token.

**Windows client** — portable or installer:

- *Portable*: unzip `mini-tun-asymmetric-client-<ver>-windows-x64-portable.zip`
  and run `mini_tun_asymmetric.exe`. The GUI requests admin (UAC) for the tunnel.
- *Installer*: run `mini-tun-asymmetric-client-<ver>-windows-x64-setup.exe`
  (Start-menu shortcut, optional autostart, uninstaller).

Add a profile with the master's address, transport (e.g. `utp`), and the auth token.

## Build

```sh
make                 # build all server/CLI targets into ./dist
# or individually:
make master slave agent cli mta-setup
```

Server/CLI targets cross-compile from any OS (`CGO_ENABLED=0`). Version is stamped
from the `VERSION` file via `-ldflags`.

Build distributable packages:

```sh
make packages          # .deb + .rpm  (needs nfpm: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest)
make client-portable   # Windows portable zip  (needs Flutter SDK)
make client-installer  # Windows installer .exe (needs Inno Setup 6 + client-portable)
```

The Flutter client builds with the Flutter SDK (`flutter build windows`); the Go
core is exposed to Android via gomobile (`gomobile bind ./mobile`). The Windows
client needs `wintun.dll` (from <https://www.wintun.net/>) — the packaging scripts
bundle it automatically.

## Status

Working prototype, deployed and verified end-to-end on a 3-node setup (1 master,
2 slaves). Done: multi-carrier transport framing, custom ports, nearest-node
selection, nft-backed dynamic firewall, Flutter desktop client.
Roadmap: graceful-shutdown audit, raw (plain) carrier, IPv6 data-path,
Noise/mTLS authentication, Android release.

## Authorship

The **architecture, design decisions, and ideas** are human. **100% of the code**
was written by an AI — Claude Opus 4.8 (Anthropic). This split is deliberate: a
human drove *what* to build and *why*, the model produced *how*. (Naming the tool
is just attribution — it doesn't imply Anthropic endorses or is affiliated with
this project.)

---

# Mini-Tun Asymmetric — русская версия

[English version above ↑](#mini-tun-asymmetric)

Самописный **асимметричный туннель** и сетевой исследовательский проект на базе
протокола **BadRouting**. Три роли — **клиент** (Flutter на десктопе + Linux CLI),
**master** (вход + выход в интернет) и **slave** (обратный канал, downlink) — где
uplink и downlink идут *разными* сетевыми путями (отсюда «Asymmetric»). Это
эксперимент со split-path транспортом и разделением потоков — из тех штук, что
делают, чтобы *понять*, как сети смотрят на трафик. Сугубо академически, разумеется. 😉

```
                 uplink (отдача)
   Клиент ----------------> Master ----> интернет
      ^                       |
      |                       |
      +------- Slave <--------+
             downlink (приём)
```

Uplink и downlink никогда не делят один путь: клиент шлёт *на* master, а получает
*от* slave. Два направления — это отдельные потоки на отдельных хопах, в этом вся идея.

> [!WARNING]
> **Ранняя альфа — proof of concept.** Это рабочий proof of concept, опубликованный
> чтобы поделиться *идеей и архитектурой* — не готовый продукт. Существует для
> исследовательских и образовательных целей: изучение дизайна транспортных
> протоколов, NAT-traversal и асимметричной маршрутизации. Ждите острых углов,
> ломающих изменений и непроверенной безопасности; не полагайтесь на него всерьёз.
> Вопрос, который задаёт этот релиз, прост: **имеет ли подход смысл?**
>
> Используйте только на сетях и системах, которыми владеете или которые вам
> разрешено тестировать, и в рамках законов вашей юрисдикции. Автор предоставляет
> «как есть», без гарантий, и не несёт ответственности за то, как это используют
> другие. Что вы вынесете из этого дизайна — целиком на ваше усмотрение. 🙂

## Возможности

- **Асимметричная маршрутизация** — uplink `клиент → master`, downlink
  `master → slave → клиент` через UDP NAT hole-punching. Обратный путь уходит с
  *другого* IP, чем вход, так что два направления — это отдельные потоки, а не одна
  двусторонняя сессия. Это и есть основная исследовательская идея проекта.
- **Выбор ближайшего узла** — master отдаёт клиенту список живых slave'ов; клиент
  RTT-пробит каждый (аутентифицированный ping/pong) и закрепляет ближайший для
  своего downlink, срезая задержку тройного пути. Для старых клиентов — откат на
  round-robin (версионируемый handshake, обратная совместимость).
- **Сменные транспортные носители** — каждый UDP-пакет оборачивается в выбранный
  формат носителя; носитель выбирается на сессию (авто-гонка с фолбэком на клиенте
  или вручную). Эксперимент над тем, насколько взаимозаменяем on-wire формат
  транспорта — и, чисто по совпадению, насколько пакет на проводе в итоге похож на
  игровую сессию, торрент или видеозвонок. 🙃
  - `cs2` — фрейминг под датаграммы Source Engine / CS2
  - `utp` — фрейминг под BitTorrent µTP (BEP-29)
  - `webrtc` — фрейминг под STUN + RTP
  - `quic` — фрейминг под QUIC / HTTP3 на :443 (симметричный вариант)
- **Кастомные порты** — по умолчанию используется родной control-порт носителя, но
  профиль может использовать любой заданный оператором порт, с фолбэком через
  запятую. Master разбирает носитель по порту прибытия.
- **Динамический фаервол (сервер)** — подсистема на nftables (`common/netfw`, с
  откатом на firewalld/ufw) открывает каждый эфемерный data-порт под сессию по
  требованию и закрывает при её завершении — так что коробка никогда не держит
  открытым широкий диапазон портов. Владеет своей помеченной таблицей и вычищает
  собственные остатки при старте.
- **Внутритуннельный DNS** — DNS-резолвер на шлюзе плюс клиентский DNS
  kill-switch и блок IPv6, так что разрешение имён остаётся консистентным внутри
  туннеля, вне зависимости от того, какие *креативные* ответы решила бы иначе
  подсунуть локальная сеть. 🙂
- **Аутентифицированный handshake** — токен авторизации никогда не появляется на
  проводе (HMAC над временной меткой и nonce); устаревшие/переигранные handshake
  отклоняются молча.
- **Шифрование** — ChaCha20-Poly1305 AEAD, ключ на сессию привязан к IP в туннеле.
- **Egress через gVisor netstack** — master терминирует TCP/UDP клиента в
  userspace-стеке и проксирует байтовые потоки в интернет, так что работает любой TCP.

## Структура

| Путь | Что |
|---|---|
| `client-flutter/` | Flutter-клиент для десктопа (Windows; каркас под Android) — UI |
| `agent/` + `cmd/mini-tun-asymmetric-agent/` | привилегированный локальный сайдкар, которым GUI управляет по loopback (владеет движком VPN, сам поднимает права через UAC) |
| `client-windows/` | общий движок клиента `vpncore` (используется agent, CLI, Android) |
| `cmd/mini-tun-asymmetric-cli/` | headless CLI-клиент для Linux |
| `master/` | master-нода — netstack-egress, внутритуннельный DNS, сессии, control, метрики |
| `slave/` | slave-нода — relay обратного канала |
| `mobile/` | gomobile-биндинг ядра под Android |
| `server-tui/` | TUI-менеджер сервера в стиле nmtui |
| `common/` | общие пакеты — транспортные носители, crypto, протокол, tun, config, netfw |
| `tools/datapathtest/` | сквозной smoke-тест data-path (handshake, RTT-проба, полный путь) |

## Установка

**Linux-сервер (.deb / .rpm)** — один пакет содержит обе роли; master или slave
выбирается после установки:

```sh
sudo dnf install ./mini-tun-asymmetric-<ver>-1.x86_64.rpm    # RHEL/CentOS/Fedora
sudo apt install ./mini-tun-asymmetric_<ver>_amd64.deb        # Debian/Ubuntu
sudo mta-setup                                                # выбрать роль, сгенерить токен, запустить
```

`mta-setup` → **Quick Setup Wizard** → *Master* или *Slave*. Master генерирует
токен, TLS-сертификат, конфиг, открывает firewall и запускает сервис; скопируйте
токен на slave-ноды и клиент. Slave спрашивает адрес master + токен.

**Windows-клиент** — portable или установщик:

- *Portable*: распаковать `mini-tun-asymmetric-client-<ver>-windows-x64-portable.zip`
  и запустить `mini_tun_asymmetric.exe`. GUI запросит права администратора (UAC).
- *Установщик*: запустить `mini-tun-asymmetric-client-<ver>-windows-x64-setup.exe`
  (ярлык в меню Пуск, опциональный автозапуск, деинсталлятор).

Добавьте профиль с адресом master, носителем (напр. `utp`) и токеном.

## Сборка

```sh
make                 # собрать все server/CLI таргеты в ./dist
# или по отдельности:
make master slave agent cli mta-setup
```

Server/CLI таргеты кросс-компилируются с любой ОС (`CGO_ENABLED=0`). Версия
проставляется из файла `VERSION` через `-ldflags`.

Сборка дистрибутивных пакетов:

```sh
make packages          # .deb + .rpm  (нужен nfpm: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest)
make client-portable   # Windows portable zip  (нужен Flutter SDK)
make client-installer  # Windows установщик .exe (нужен Inno Setup 6 + client-portable)
```

Flutter-клиент собирается через Flutter SDK (`flutter build windows`); Go-ядро
пробрасывается в Android через gomobile (`gomobile bind ./mobile`). Windows-клиенту
нужен `wintun.dll` (с <https://www.wintun.net/>) — скрипты упаковки добавляют его
автоматически.

## Статус

Рабочий прототип, развёрнут и проверен end-to-end на связке из 3 нод (1 master,
2 slave). Готово: мульти-носительный транспортный фрейминг, кастомные порты, выбор
ближайшего узла, динамический фаервол на nft, десктопный Flutter-клиент.
Планы: аудит graceful-shutdown, raw (plain) носитель, IPv6 в data-path,
аутентификация Noise/mTLS, релиз под Android.

## Авторство

**Архитектура, проектные решения и идеи** — человек. **100% кода** написано ИИ —
Claude Opus 4.8 (Anthropic). Разделение намеренное: человек задавал *что* строить
и *зачем*, модель выдавала *как*. (Упоминание инструмента — это просто указание
авторства; оно не означает, что Anthropic одобряет проект или связан с ним.)

