// Mini-Tun Asymmetric — Flutter desktop/mobile client.
// Phase A1: Windows UI at parity with the WebView client, driven by a mock
// engine (real Go bridge lands in A1b). Frameless window via window_manager.
import 'dart:io' show File, Platform, Process, ProcessStartMode, pid;
import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'theme.dart';
import 'i18n.dart';
import 'engine.dart';
import 'http_engine.dart';
import 'widgets/dial.dart';
import 'widgets/logo.dart';
import 'widgets/dither_bg.dart';
import 'widgets/profile_tile.dart';
import 'widgets/profile_modal.dart';
import 'widgets/settings_sheet.dart';
import 'models.dart';

final appTheme = AppTheme();
final i18n = I18n();

/// The active engine. Defaults to a mock so the UI runs standalone; main() swaps
/// in the real HttpEngine (driving the Go sidecar) when its endpoint is found.
Engine engine = MockEngine();

/// Bumped whenever `engine` is replaced, so the UI reloads against the new one.
final engineReady = ValueNotifier<int>(0);

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await appTheme.load();
  await i18n.load();
  activeLangGetter = () => i18n.lang; // let the profile modal read carrier labels

  final isDesktop = Platform.isWindows || Platform.isLinux || Platform.isMacOS;
  if (isDesktop) {
    await windowManager.ensureInitialized();
    const opts = WindowOptions(
      size: Size(440, 760),
      center: true,
      titleBarStyle: TitleBarStyle.hidden, // frameless: our own title bar
      title: 'Mini-Tun Asymmetric',
    );
    windowManager.waitUntilReadyToShow(opts, () async {
      await windowManager.show();
      await windowManager.focus();
    });
  }

  runApp(const App());

  // Connect the real sidecar engine in the background; UI starts on the mock and
  // switches over (engineReady) once the agent publishes its endpoint.
  initRealEngine();
}

/// Try to connect the real sidecar engine. If its endpoint file isn't present,
/// spawn the agent (which self-elevates), then poll briefly for the endpoint.
/// Falls back to the mock engine if the sidecar never appears (dev/standalone).
Future<void> initRealEngine() async {
  final http = HttpEngine();
  if (await http.init()) {
    engine = http;
    engineReady.value++;
    return;
  }
  // No endpoint yet — try to launch the bundled agent next to this exe.
  if (Platform.isWindows) {
    try {
      final dir = File(Platform.resolvedExecutable).parent.path;
      final agentPath = '$dir${Platform.pathSeparator}mini-tun-asymmetric-agent.exe';
      if (await File(agentPath).exists()) {
        await Process.start(agentPath, ['--owner-pid', '$pid'],
            mode: ProcessStartMode.detached);
      }
    } catch (_) {}
    // Poll up to ~15s for the agent (UAC prompt + bind) to publish its endpoint.
    for (var i = 0; i < 30; i++) {
      await Future.delayed(const Duration(milliseconds: 500));
      if (await http.init()) {
        engine = http;
        engineReady.value++;
        return;
      }
    }
  }
  // Fallback: keep the mock engine so the UI still runs.
}

class App extends StatelessWidget {
  const App({super.key});
  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: Listenable.merge([appTheme, i18n]),
      builder: (context, _) {
        final pal = appTheme.palette;
        return MaterialApp(
          debugShowCheckedModeBanner: false,
          title: 'Mini-Tun Asymmetric',
          theme: ThemeData(
            useMaterial3: true,
            brightness: pal.isLight ? Brightness.light : Brightness.dark,
            scaffoldBackgroundColor: pal.bg,
            fontFamily: 'Segoe UI',
          ),
          home: const HomePage(),
        );
      },
    );
  }
}

class HomePage extends StatefulWidget {
  const HomePage({super.key});
  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  List<Profile> _profiles = [];
  int _selected = -1;

  @override
  void initState() {
    super.initState();
    _load();
    // Reload when the engine is swapped from mock → real sidecar.
    engineReady.addListener(_load);
  }

  @override
  void dispose() {
    engineReady.removeListener(_load);
    super.dispose();
  }

  Future<void> _load() async {
    final p = await engine.loadProfiles();
    if (!mounted) return;
    setState(() {
      _profiles = p;
      if (_profiles.isNotEmpty && _selected < 0) _selected = 0;
      if (_selected >= _profiles.length) _selected = _profiles.isEmpty ? -1 : 0;
    });
  }

  String _t(String k) => i18n.t(k);

  void _toast(String msg) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(msg),
      behavior: SnackBarBehavior.floating,
      duration: const Duration(milliseconds: 1800),
    ));
  }

  Future<void> _openModal({int editIdx = -1}) async {
    final result = await showProfileModal(
      context,
      pal: appTheme.palette,
      t: _t,
      existing: editIdx >= 0 ? _profiles[editIdx] : null,
    );
    if (result == null) return;
    final updated = editIdx >= 0
        ? await engine.updateProfile(editIdx, result)
        : await engine.addProfile(result);
    setState(() {
      _profiles = updated;
      if (_selected < 0 && _profiles.isNotEmpty) _selected = _profiles.length - 1;
    });
    _toast(_t('toastSaved'));
  }

  Future<void> _delete(int i) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        backgroundColor: appTheme.palette.bg2,
        content: Text('${_t('delConfirm')} "${_profiles[i].name}"?',
            style: TextStyle(color: appTheme.palette.text)),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c, false), child: Text(_t('cancel'))),
          TextButton(onPressed: () => Navigator.pop(c, true), child: Text(_t('delConfirm'))),
        ],
      ),
    );
    if (ok != true) return;
    final updated = await engine.deleteProfile(i);
    setState(() {
      _profiles = updated;
      if (_selected == i) {
        _selected = _profiles.isEmpty ? -1 : 0;
      } else if (_selected > i) {
        _selected--;
      }
    });
    _toast(_t('toastDeleted'));
  }

  void _toggleConnect() {
    final st = engine.status.value.state;
    if (st == VpnState.connected || st == VpnState.connecting) {
      engine.disconnect();
      return;
    }
    if (_selected < 0 || _selected >= _profiles.length) {
      _toast(_t('noServer'));
      return;
    }
    engine.connectIndex(_selected);
  }

  @override
  Widget build(BuildContext context) {
    // Rebuild on theme/language change so presets + custom gradient apply live
    // (no restart). HomePage is held as a const child of MaterialApp, so it must
    // subscribe itself rather than rely on the parent rebuilding it.
    return AnimatedBuilder(
      animation: Listenable.merge([appTheme, i18n]),
      builder: (context, _) {
        final pal = appTheme.palette;
        return Scaffold(
          backgroundColor: pal.bg,
          body: DitheredBackground(
            pal: pal,
            child: Column(
          children: [
            _TitleBar(pal: pal),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(18, 14, 18, 26),
                children: [
                  _DialSection(pal: pal, t: _t, onTap: _toggleConnect),
                  const SizedBox(height: 18),
                  _StatsRow(pal: pal, t: _t),
                  const SizedBox(height: 18),
                  _ServersHeader(pal: pal, t: _t, onAdd: () => _openModal()),
                  const SizedBox(height: 9),
                  if (_profiles.isEmpty)
                    _EmptyServers(pal: pal, t: _t)
                  else
                    ..._profiles.asMap().entries.map((e) => Padding(
                          padding: const EdgeInsets.only(bottom: 9),
                          child: ProfileTile(
                            pal: pal,
                            profile: e.value,
                            selected: e.key == _selected,
                            onTap: () => setState(() => _selected = e.key),
                            onEdit: () => _openModal(editIdx: e.key),
                            onDelete: () => _delete(e.key),
                          ),
                        )),
                ],
              ),
            ),
          ],
        ),
      ),
        );
      },
    );
  }
}

class _TitleBar extends StatelessWidget {
  final Palette pal;
  const _TitleBar({required this.pal});
  @override
  Widget build(BuildContext context) {
    final isDesktop = Platform.isWindows || Platform.isLinux || Platform.isMacOS;
    // Strictly TWO colors, 50/50, centerLeft→centerRight. For a custom 3–5 color
    // theme we take the first two (closest/neighbouring stops) so the bar stays
    // soft instead of cramming the whole palette into a short wide strip.
    final src = pal.gradient.length >= 2 ? pal.gradient : [pal.brand, pal.brand2];
    final c1 = src[0], c2 = src[1];
    // Honest contrast: luminance of the two bar colors. Bright bar → dark glyphs,
    // dark bar → white. Recomputed every build, so it flips live on theme change.
    double luma(Color c) => (0.299 * c.red + 0.587 * c.green + 0.114 * c.blue) / 255;
    final fg = ((luma(c1) + luma(c2)) / 2) > 0.62 ? Colors.black87 : Colors.white;
    return GestureDetector(
      onPanStart: isDesktop ? (_) => windowManager.startDragging() : null,
      child: Container(
        height: 44,
        padding: const EdgeInsets.only(left: 12),
        decoration: BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.centerLeft, end: Alignment.centerRight,
            colors: [c1, c2],
          ),
        ),
        child: Row(
          children: [
            const SizedBox(width: 2),
            SizedBox(width: 22, height: 22, child: LogoMark(color: fg)),
            const SizedBox(width: 9),
            Text('Mini-Tun ',
                style: TextStyle(color: fg, fontSize: 13, fontWeight: FontWeight.w700)),
            Text('Asymmetric',
                style: TextStyle(color: fg, fontSize: 13, fontWeight: FontWeight.w800)),
            const Spacer(),
            _TbBtn(icon: Icons.settings, fg: fg, onTap: () => showSettingsSheet(context, appTheme, i18n)),
            if (isDesktop) ...[
              _TbBtn(icon: Icons.remove, fg: fg, onTap: () => windowManager.minimize()),
              _TbBtn(
                  icon: Icons.crop_square,
                  fg: fg,
                  onTap: () async {
                    if (await windowManager.isMaximized()) {
                      windowManager.unmaximize();
                    } else {
                      windowManager.maximize();
                    }
                  }),
              _TbBtn(icon: Icons.close, fg: fg, onTap: () => windowManager.close(), danger: true),
            ],
          ],
        ),
      ),
    );
  }
}

class _TbBtn extends StatelessWidget {
  final IconData icon;
  final VoidCallback onTap;
  final bool danger;
  final Color fg;
  const _TbBtn({required this.icon, required this.onTap, required this.fg, this.danger = false});
  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      hoverColor: danger ? const Color(0xFFe53935) : fg.withOpacity(.18),
      child: SizedBox(
        width: 46,
        height: 44,
        child: Icon(icon, size: 16, color: fg.withOpacity(.9)),
      ),
    );
  }
}

class _DialSection extends StatelessWidget {
  final Palette pal;
  final String Function(String) t;
  final VoidCallback onTap;
  const _DialSection({required this.pal, required this.t, required this.onTap});
  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<VpnStatus>(
      valueListenable: engine.status,
      builder: (context, s, _) {
        final label = {
          VpnState.connected: t('sConnected'),
          VpnState.connecting: t('sConnecting'),
          VpnState.disconnected: t('sDisconnected'),
          VpnState.error: t('sError'),
        }[s.state]!;
        return Column(
          children: [
            const SizedBox(height: 8),
            DialButton(pal: pal, state: s.state, onTap: onTap),
            const SizedBox(height: 18),
            Text(label, style: TextStyle(color: pal.text, fontSize: 19, fontWeight: FontWeight.w700)),
            const SizedBox(height: 3),
            Text(
              s.tunnelIp.isNotEmpty
                  ? '▸ ${s.tunnelIp}${s.transport.isNotEmpty ? '  ·  ${s.transport}' : ''}${s.slaveRttMs > 0 ? '  ·  ${s.slaveRttMs}ms' : ''}'
                  : '',
              style: TextStyle(color: pal.muted, fontSize: 12, fontFamily: 'Consolas'),
            ),
          ],
        );
      },
    );
  }
}

String _fmtBytes(int b) {
  if (b < 1024) return '$b B';
  if (b < 1048576) return '${(b / 1024).toStringAsFixed(1)} KB';
  if (b < 1073741824) return '${(b / 1048576).toStringAsFixed(1)} MB';
  return '${(b / 1073741824).toStringAsFixed(2)} GB';
}

String _fmtTime(int s) {
  final h = s ~/ 3600, m = (s ~/ 60) % 60, x = s % 60;
  String p(int v) => v.toString().padLeft(2, '0');
  return '${p(h)}:${p(m)}:${p(x)}';
}

class _StatsRow extends StatelessWidget {
  final Palette pal;
  final String Function(String) t;
  const _StatsRow({required this.pal, required this.t});
  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<VpnStatus>(
      valueListenable: engine.status,
      builder: (context, s, _) {
        final conn = s.state == VpnState.connected;
        return Row(
          children: [
            _stat(_fmtBytes(conn ? s.upBytes : 0), t('upload'), pal.brand),
            const SizedBox(width: 10),
            _stat(_fmtBytes(conn ? s.dnBytes : 0), t('download'), pal.on),
            const SizedBox(width: 10),
            _stat(_fmtTime(conn ? s.uptimeSec : 0), t('uptime'), pal.text),
          ],
        );
      },
    );
  }

  Widget _stat(String val, String lbl, Color valColor) => Expanded(
        child: Container(
          padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 8),
          decoration: BoxDecoration(
            color: pal.surface,
            border: Border.all(color: pal.line),
            borderRadius: BorderRadius.circular(14),
          ),
          child: Column(
            children: [
              Text(val, style: TextStyle(color: valColor, fontSize: 15, fontWeight: FontWeight.w700)),
              const SizedBox(height: 3),
              Text(lbl.toUpperCase(),
                  style: TextStyle(color: pal.muted, fontSize: 10, letterSpacing: .5)),
            ],
          ),
        ),
      );
}

class _ServersHeader extends StatelessWidget {
  final Palette pal;
  final String Function(String) t;
  final VoidCallback onAdd;
  const _ServersHeader({required this.pal, required this.t, required this.onAdd});
  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Text(t('servers').toUpperCase(),
            style: TextStyle(color: pal.muted, fontSize: 12, fontWeight: FontWeight.w700, letterSpacing: 1)),
        const Spacer(),
        TextButton(
          onPressed: onAdd,
          child: Text(t('addServer'), style: TextStyle(color: pal.brand, fontWeight: FontWeight.w600)),
        ),
      ],
    );
  }
}

class _EmptyServers extends StatelessWidget {
  final Palette pal;
  final String Function(String) t;
  const _EmptyServers({required this.pal, required this.t});
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 26, horizontal: 10),
      decoration: BoxDecoration(
        border: Border.all(color: pal.line, width: 1.5),
        borderRadius: BorderRadius.circular(14),
      ),
      child: Center(child: Text(t('noServer'), style: TextStyle(color: pal.muted, fontSize: 13))),
    );
  }
}
