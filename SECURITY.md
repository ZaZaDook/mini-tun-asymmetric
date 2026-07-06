# Security Policy

**English** · [Русская версия ниже ↓](#политика-безопасности)

## ⚠️ Project status

Mini-Tun Asymmetric is an **early alpha (v0.1) proof of concept**. The security of
the code is **unaudited**. It is a network-research experiment, **not** a hardened
product:

- Do **not** deploy the server on production or primary hosts — use throwaway
  test machines / VMs.
- Do **not** run the client on your daily-driver machine — it installs a TUN
  adapter and changes routing/DNS.
- Assume the on-wire protocol, crypto, and firewall handling may all have flaws.

Because the project is pre-release and moves fast, treat every version as
potentially vulnerable. There is no guarantee of timely fixes.

## Supported versions

Only the latest commit on the default branch (`main`) receives fixes. Older tags,
releases, and forks are **not** supported.

| Version | Supported |
|---|---|
| latest `main` | ✅ |
| everything else | ❌ |

## Reporting a vulnerability

**Please report security issues privately — do not open a public issue.**

Use **GitHub's private vulnerability reporting**:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Fill in the advisory form with as much detail as you can.

This keeps the report private between you and the maintainer until a fix is
available.

### What to include

- A clear description of the issue and its impact.
- Steps to reproduce (a minimal proof of concept helps a lot).
- Affected component (`master`, `slave`, client, a specific carrier, etc.) and
  commit hash / version.
- Any suggested remediation, if you have one.

### What to expect

This is a hobby / research project maintained on a best-effort basis by a single
author. There is **no SLA** and **no bug bounty**. That said:

- You should get an acknowledgement of your report.
- Confirmed issues will be fixed on `main` when time allows, and credited to the
  reporter unless you ask otherwise.
- Please give a reasonable window for a fix before any public disclosure —
  coordinated disclosure is appreciated.

### Scope

In scope: bugs in this repository's code — protocol, crypto usage, authentication,
the dynamic firewall, session handling, privilege boundaries, memory-safety, etc.

Out of scope: the fundamental design (asymmetric split-path routing and
traffic-shaping carriers are the *point* of the project, not a vulnerability),
issues in third-party dependencies (report those upstream), and anything requiring
you to attack systems you do not own or are not authorized to test.

Use this software only on networks and systems you own or are explicitly
authorized to test, and in accordance with the laws of your jurisdiction.

---

# Политика безопасности

[English version above ↑](#security-policy)

## ⚠️ Статус проекта

Mini-Tun Asymmetric — это **ранняя альфа (v0.1), proof of concept**. Безопасность
кода **не проходила аудита**. Это сетевой исследовательский эксперимент, **не**
закалённый продукт:

- **Не** разворачивайте сервер на боевых или основных хостах — только на
  одноразовых тестовых машинах / VM.
- **Не** запускайте клиент на основной рабочей машине — он ставит TUN-адаптер и
  меняет маршрутизацию/DNS.
- Исходите из того, что on-wire протокол, криптография и работа с фаерволом
  могут содержать ошибки.

Поскольку проект в пред-релизном состоянии и быстро меняется, считайте любую
версию потенциально уязвимой. Своевременные исправления не гарантируются.

## Поддерживаемые версии

Исправления получает только последний коммит в ветке по умолчанию (`main`).
Старые теги, релизы и форки **не** поддерживаются.

| Версия | Поддержка |
|---|---|
| последний `main` | ✅ |
| всё остальное | ❌ |

## Как сообщить об уязвимости

**Пожалуйста, сообщайте об уязвимостях приватно — не открывайте публичный issue.**

Используйте **приватные репорты GitHub (private vulnerability reporting)**:

1. Откройте вкладку **Security** в репозитории.
2. Нажмите **Report a vulnerability**.
3. Заполните форму advisory с максимумом деталей.

Так репорт остаётся приватным между вами и мейнтейнером до появления исправления.

### Что приложить

- Понятное описание проблемы и её последствий.
- Шаги воспроизведения (минимальный proof of concept очень помогает).
- Затронутый компонент (`master`, `slave`, клиент, конкретный носитель и т.д.) и
  хэш коммита / версию.
- Предлагаемое исправление, если оно у вас есть.

### Чего ожидать

Это хобби / исследовательский проект, который поддерживает один автор по мере
сил. **Никакого SLA** и **никакого bug bounty** нет. При этом:

- Вы получите подтверждение получения репорта.
- Подтверждённые проблемы будут исправлены в `main`, когда позволит время, с
  указанием автора репорта, если вы не попросите об обратном.
- Пожалуйста, дайте разумное время на исправление до публичного раскрытия —
  скоординированное раскрытие приветствуется.

### Область (scope)

В области: баги в коде этого репозитория — протокол, использование криптографии,
аутентификация, динамический фаервол, работа с сессиями, границы привилегий,
безопасность памяти и т.д.

Вне области: фундаментальный дизайн (асимметричная split-path маршрутизация и
маскирующие носители — это *смысл* проекта, а не уязвимость), проблемы в сторонних
зависимостях (сообщайте о них upstream), а также всё, что требует атаковать
системы, которыми вы не владеете или не имеете явного разрешения тестировать.

Используйте это ПО только на сетях и системах, которыми владеете или которые вам
явно разрешено тестировать, и в рамках законов вашей юрисдикции.
