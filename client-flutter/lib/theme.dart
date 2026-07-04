// Theme system ported from the WebView client's CSS custom properties.
// 5 presets (dark/light/pink/green/blue) + a custom 2–5 stop gradient. The
// active palette drives the title-bar/dial gradient, surfaces, text, accents.
//
// Persisted natively via shared_preferences (replaces the old localStorage,
// which broke across restarts because the UI ran on a random loopback port).
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// A resolved palette: the colors the whole UI reads from.
class Palette {
  final Color bg, bg2, surface, surface2, line, line2;
  final Color text, muted, muted2;
  final Color brand, brand2, brandGlow;
  final Color on, onGlow, warn, err, halo;
  final List<Color> gradient; // title-bar / dial gradient stops
  final bool isLight;
  const Palette({
    required this.bg, required this.bg2, required this.surface, required this.surface2,
    required this.line, required this.line2, required this.text, required this.muted,
    required this.muted2, required this.brand, required this.brand2, required this.brandGlow,
    required this.on, required this.onGlow, required this.warn, required this.err,
    required this.halo, required this.gradient, required this.isLight,
  });
}

Color _h(int v) => Color(0xFF000000 | v);

/// Per-preset title-bar gradient ends (mirrors PRESET_GRAD in the old index.html).
const Map<String, List<int>> _presetGrad = {
  'dark': [0x3b82f6, 0x6366f1],
  'light': [0x2563eb, 0x4f46e5],
  'pink': [0xec4899, 0xd946ef],
  'green': [0x10b981, 0x22c55e],
  'blue': [0x0ea5e9, 0x3b82f6],
};

const List<String> kPresets = ['dark', 'light', 'pink', 'green', 'blue'];

/// sRGB-weighted perceived luminance 0..1 (for picking light vs dark base on custom).
double _luma(Color c) =>
    (0.299 * c.red + 0.587 * c.green + 0.114 * c.blue) / 255.0;

double _avgLuma(List<Color> cols) =>
    cols.map(_luma).reduce((a, b) => a + b) / cols.length;

/// color-mix(in srgb, a pct%, b) equivalent.
Color _mix(Color a, Color b, double pct) =>
    Color.lerp(b, a, pct.clamp(0, 100) / 100)!;

Palette _darkBase(List<int> grad) {
  final brand = _h(grad.first), brand2 = _h(grad.last);
  return Palette(
    bg: _h(0x0b0f1a), bg2: _h(0x0e1422), surface: _h(0x131a2b), surface2: _h(0x192337),
    line: _h(0x222d44), line2: _h(0x2c3a57), text: _h(0xeef2fb), muted: _h(0x7d8aa6),
    muted2: _h(0x5d6b80), brand: brand, brand2: brand2,
    brandGlow: brand.withOpacity(.45), on: _h(0x22c55e), onGlow: _h(0x22c55e).withOpacity(.5),
    warn: _h(0xf59e0b), err: _h(0xef4444), halo: _h(0x172240),
    gradient: [brand, brand2], isLight: false,
  );
}

/// Resolve a preset name to a palette. Non-preset names fall back to dark.
Palette resolvePreset(String name) {
  final grad = _presetGrad[name] ?? _presetGrad['dark']!;
  switch (name) {
    case 'light':
      final brand = _h(grad.first), brand2 = _h(grad.last);
      return Palette(
        bg: _h(0xeef1f7), bg2: _h(0xe6ebf4), surface: _h(0xffffff), surface2: _h(0xf3f6fc),
        line: _h(0xd9e0ee), line2: _h(0xc5cfe2), text: _h(0x1a2235), muted: _h(0x69748c),
        muted2: _h(0x9aa6bd), brand: brand, brand2: brand2, brandGlow: brand.withOpacity(.30),
        on: _h(0x16a34a), onGlow: _h(0x16a34a).withOpacity(.35), warn: _h(0xf59e0b),
        err: _h(0xef4444), halo: _h(0xd6e0f5), gradient: [brand, brand2], isLight: true,
      );
    case 'pink':
      return _tintDark(grad, bg: 0x120912, bg2: 0x170d18, surface: 0x1f1420,
          surface2: 0x2a1b2d, line: 0x3a2440, line2: 0x4a2f52, halo: 0x2a1330);
    case 'green':
      return _tintDark(grad, bg: 0x07120d, bg2: 0x0a1712, surface: 0x0f1f18,
          surface2: 0x152a20, line: 0x1d3a2c, line2: 0x27503c, halo: 0x0c2418);
    case 'blue':
      return _tintDark(grad, bg: 0x06121b, bg2: 0x091a26, surface: 0x0d2231,
          surface2: 0x123042, line: 0x163a4f, line2: 0x1d5069, halo: 0x0a2233);
    default:
      return _darkBase(grad);
  }
}

Palette _tintDark(List<int> grad,
    {required int bg, required int bg2, required int surface, required int surface2,
    required int line, required int line2, required int halo}) {
  final brand = _h(grad.first), brand2 = _h(grad.last);
  return Palette(
    bg: _h(bg), bg2: _h(bg2), surface: _h(surface), surface2: _h(surface2),
    line: _h(line), line2: _h(line2), text: _h(0xeef2fb), muted: _h(0x7d8aa6),
    muted2: _h(0x5d6b80), brand: brand, brand2: brand2, brandGlow: brand.withOpacity(.45),
    on: _h(0x22c55e), onGlow: _h(0x22c55e).withOpacity(.5), warn: _h(0xf59e0b),
    err: _h(0xef4444), halo: _h(halo), gradient: [brand, brand2], isLight: false,
  );
}

/// Build a palette from a custom gradient (2–5 hex stops). Light vs dark base is
/// chosen by the palette's average luminance, matching avgLuma()>0.62 in the old UI.
Palette resolveCustom(List<Color> cols) {
  final light = _avgLuma(cols) > 0.62;
  final c0 = cols.first;
  final base = light ? resolvePreset('light') : _darkBase([cols.first.value & 0xFFFFFF, cols.last.value & 0xFFFFFF]);
  final whiteBase = light;
  return Palette(
    bg: base.bg, bg2: base.bg2,
    surface: _mix(c0, light ? _h(0xffffff) : _h(0x131a2b), light ? 8 : 12),
    surface2: _mix(c0, light ? _h(0xf3f6fc) : _h(0x192337), light ? 12 : 16),
    line: _mix(c0, light ? _h(0xd9e0ee) : _h(0x222d44), light ? 16 : 20),
    line2: _mix(c0, light ? _h(0xc5cfe2) : _h(0x2c3a57), light ? 22 : 24),
    text: base.text, muted: base.muted, muted2: base.muted2,
    brand: cols.first, brand2: cols.last, brandGlow: cols.first.withOpacity(.45),
    on: base.on, onGlow: base.onGlow, warn: base.warn, err: base.err,
    halo: cols.first.withOpacity(.16),
    gradient: cols, isLight: whiteBase,
  );
}

/// AppTheme holds the current theme selection + custom stops and notifies the UI.
class AppTheme extends ChangeNotifier {
  String _name = 'dark';
  List<Color> _custom = [_h(0xff5f6d), _h(0x845ec2), _h(0x2c73d2)];

  String get name => _name;
  List<Color> get custom => List.unmodifiable(_custom);
  Palette get palette =>
      _name == 'custom' ? resolveCustom(_custom) : resolvePreset(_name);

  static const _kName = 'theme';
  static const _kCustom = 'customColors';

  Future<void> load() async {
    final p = await SharedPreferences.getInstance();
    _name = p.getString(_kName) ?? 'dark';
    final raw = p.getStringList(_kCustom);
    if (raw != null && raw.length >= 2) {
      _custom = raw.map((h) => _h(int.parse(h.replaceFirst('#', ''), radix: 16))).toList();
    }
    notifyListeners();
  }

  Future<void> _save() async {
    final p = await SharedPreferences.getInstance();
    await p.setString(_kName, _name);
    await p.setStringList(_kCustom,
        _custom.map((c) => '#${(c.value & 0xFFFFFF).toRadixString(16).padLeft(6, '0')}').toList());
  }

  void setTheme(String name) {
    _name = name;
    _save();
    notifyListeners();
  }

  void setCustom(List<Color> cols) {
    _custom = cols;
    if (_name == 'custom') {} // palette recomputes via getter
    _save();
    notifyListeners();
  }
}
