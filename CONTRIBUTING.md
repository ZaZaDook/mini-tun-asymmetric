# Contributing

**English** · [Русская версия ниже ↓](#участие-в-проекте)

Thanks for your interest in Mini-Tun Asymmetric. This is an early-alpha
network-research project, so contributions, bug reports, and ideas are all
welcome — with a few caveats below.

## Before you start

- **This is alpha software.** The design and on-wire protocol change without
  notice. Coordinate on a big change (open an issue first) before writing a lot
  of code, so your work doesn't collide with an in-flight redesign.
- **Never test on production.** Use throwaway VMs / test hosts for the server,
  and a spare or virtual machine for the client. See the warnings in the
  [README](README.md).
- **Security issues do not go here.** Report vulnerabilities privately — see
  [SECURITY.md](SECURITY.md). Do not open a public issue or PR for a security
  bug.
- By contributing, you agree your contributions are licensed under the project's
  [AGPL-3.0](LICENSE).

## Ways to contribute

- **Report bugs** — open an issue with the bug template. Include the component
  (`master` / `slave` / client / carrier), version or commit hash, OS, and steps
  to reproduce.
- **Suggest features** — open an issue with the feature template. Say what
  problem it solves, not just the solution.
- **Send code** — fix a bug, improve docs, add a test, or implement something
  from the roadmap in the README.

## Development setup

You need [Go](https://go.dev/) (see `go.mod` for the version) and, for the
client GUI, the [Flutter SDK](https://flutter.dev/).

Server and CLI targets cross-compile from any OS with `CGO_ENABLED=0`:

```sh
make                 # build all server/CLI targets into ./dist
make master slave agent cli mta-setup   # or build individually
```

Other useful targets:

```sh
make tls             # generate self-signed dev TLS certs into ./certs
make packages        # .deb + .rpm (needs nfpm)
make client-portable # Windows portable zip (needs Flutter SDK)
make clean           # remove ./dist
```

The Windows GUI is the Flutter app in `client-flutter/`
(`flutter build windows`); the Go side is the privileged sidecar it drives over
loopback. See the [README](README.md) for the full layout and build details.

## Testing

Please run the test suite and the end-to-end data-path smoke test before opening
a PR:

```sh
go test ./...                    # unit tests
go vet ./...                     # static checks
go run ./tools/datapathtest      # end-to-end smoke test (handshake, RTT probe, full path)
```

If you change behavior, add or update a test. If you can't run something (e.g.
the full 3-node setup), say so in the PR rather than claiming it passed.

## Code style

- Match the surrounding code — naming, comment density, and existing idioms.
- Run `gofmt` (or `go fmt ./...`) on Go code before committing.
- Keep changes focused; unrelated cleanups belong in their own PR.
- Don't introduce a new dependency or library when the project already has one
  that does the job.

## Pull requests

1. Fork the repo and create a branch off `main` (never commit to `main`
   directly).
2. Make your change, with tests, and make sure `go test ./...` and `go vet ./...`
   pass.
3. Write a clear commit message: what changed and why.
4. Open a PR against `main` using the PR template. Describe what you changed,
   what you tested, and anything left unfinished or unverified.
5. Be ready for review feedback — since the protocol is in flux, a maintainer may
   ask for changes to keep things consistent.

Small, self-contained PRs get merged faster than large sweeping ones.

## License

By submitting a contribution, you certify that you have the right to submit it
under the [AGPL-3.0](LICENSE) and agree that it will be distributed under that
license. Copyright and license notices must be preserved.

---

# Участие в проекте

[English version above ↑](#contributing)

Спасибо за интерес к Mini-Tun Asymmetric. Это ранняя альфа и сетевой
исследовательский проект, поэтому контрибьюции, баг-репорты и идеи приветствуются
— с парой оговорок ниже.

## Прежде чем начать

- **Это альфа-софт.** Дизайн и on-wire протокол меняются без предупреждения. По
  крупным изменениям сначала согласуйтесь (откройте issue), прежде чем писать
  много кода, чтобы работа не столкнулась с редизайном, который уже в процессе.
- **Никогда не тестируйте на проде.** Для сервера используйте одноразовые VM /
  тестовые хосты, для клиента — запасную или виртуальную машину. См.
  предупреждения в [README](README.md).
- **Проблемы безопасности — не сюда.** Сообщайте об уязвимостях приватно, см.
  [SECURITY.md](SECURITY.md). Не открывайте публичный issue или PR по
  security-багу.
- Участвуя, вы соглашаетесь, что ваши контрибьюции лицензируются под
  [AGPL-3.0](LICENSE) проекта.

## Как можно помочь

- **Сообщать о багах** — откройте issue по шаблону бага. Укажите компонент
  (`master` / `slave` / клиент / носитель), версию или хэш коммита, ОС и шаги
  воспроизведения.
- **Предлагать фичи** — откройте issue по шаблону фичи. Опишите, какую проблему
  это решает, а не только само решение.
- **Присылать код** — исправить баг, улучшить документацию, добавить тест или
  реализовать что-то из планов (roadmap) в README.

## Настройка окружения

Нужен [Go](https://go.dev/) (версию см. в `go.mod`), а для GUI-клиента —
[Flutter SDK](https://flutter.dev/).

Server- и CLI-таргеты кросс-компилируются с любой ОС при `CGO_ENABLED=0`:

```sh
make                 # собрать все server/CLI таргеты в ./dist
make master slave agent cli mta-setup   # или по отдельности
```

Другие полезные таргеты:

```sh
make tls             # сгенерировать self-signed dev TLS-сертификаты в ./certs
make packages        # .deb + .rpm (нужен nfpm)
make client-portable # Windows portable zip (нужен Flutter SDK)
make clean           # удалить ./dist
```

Windows-GUI — это Flutter-приложение в `client-flutter/`
(`flutter build windows`); Go-сторона — привилегированный сайдкар, которым GUI
управляет по loopback. Полную структуру и детали сборки см. в [README](README.md).

## Тестирование

Пожалуйста, перед открытием PR прогоните тесты и сквозной smoke-тест data-path:

```sh
go test ./...                    # юнит-тесты
go vet ./...                     # статические проверки
go run ./tools/datapathtest      # сквозной smoke-тест (handshake, RTT-проба, полный путь)
```

Если меняете поведение — добавьте или обновите тест. Если что-то не можете
прогнать (например, полную связку из 3 нод) — напишите об этом в PR, а не
утверждайте, что всё прошло.

## Стиль кода

- Подстраивайтесь под окружающий код — именование, плотность комментариев,
  существующие идиомы.
- Прогоняйте `gofmt` (или `go fmt ./...`) по Go-коду перед коммитом.
- Держите изменения сфокусированными; несвязанные чистки — в отдельный PR.
- Не тащите новую зависимость или библиотеку, если в проекте уже есть та, что
  делает то же самое.

## Pull request'ы

1. Форкните репозиторий и создайте ветку от `main` (никогда не коммитьте в `main`
   напрямую).
2. Внесите изменение с тестами и убедитесь, что `go test ./...` и `go vet ./...`
   проходят.
3. Напишите понятное сообщение коммита: что изменилось и зачем.
4. Откройте PR в `main` по шаблону PR. Опишите, что изменили, что тестировали и
   что осталось незаконченным или непроверенным.
5. Будьте готовы к правкам по ревью — поскольку протокол в движении, мейнтейнер
   может попросить изменения ради консистентности.

Небольшие самодостаточные PR мёржатся быстрее, чем большие всеохватные.

## Лицензия

Отправляя контрибьюцию, вы подтверждаете, что имеете право предоставить её под
[AGPL-3.0](LICENSE), и соглашаетесь, что она будет распространяться под этой
лицензией. Уведомления об авторских правах и лицензии должны сохраняться.
