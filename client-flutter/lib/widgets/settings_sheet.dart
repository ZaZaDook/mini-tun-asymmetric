// Settings sheet: theme presets + custom gradient builder (2–5 stops) + language
// toggle. Mirrors the settings overlay in the WebView client. Persists via
// AppTheme/I18n (shared_preferences).
import 'package:flutter/material.dart';
import '../theme.dart';
import '../i18n.dart';

Future<void> showSettingsSheet(BuildContext context, AppTheme theme, I18n i18n) {
  return showModalBottomSheet(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (_) => _SettingsSheet(theme: theme, i18n: i18n),
  );
}

class _SettingsSheet extends StatefulWidget {
  final AppTheme theme;
  final I18n i18n;
  const _SettingsSheet({required this.theme, required this.i18n});
  @override
  State<_SettingsSheet> createState() => _SettingsSheetState();
}

class _SettingsSheetState extends State<_SettingsSheet> {
  late List<Color> _custom;

  @override
  void initState() {
    super.initState();
    _custom = List.of(widget.theme.custom);
  }

  String _t(String k) => widget.i18n.t(k);

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: Listenable.merge([widget.theme, widget.i18n]),
      builder: (context, _) {
        final p = widget.theme.palette;
        return Container(
          decoration: BoxDecoration(
            color: p.bg2,
            borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
            border: Border.all(color: p.line2),
          ),
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 24),
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Center(
                  child: Container(
                    width: 40, height: 4, margin: const EdgeInsets.only(bottom: 14),
                    decoration: BoxDecoration(color: p.line2, borderRadius: BorderRadius.circular(2)),
                  ),
                ),
                Text(_t('settings'), style: TextStyle(color: p.text, fontSize: 17, fontWeight: FontWeight.w700)),
                const SizedBox(height: 18),
                _grpLabel(p, _t('theme')),
                const SizedBox(height: 10),
                _ThemeSwatches(theme: widget.theme),
                const SizedBox(height: 18),
                _grpLabel(p, _t('customGrad')),
                const SizedBox(height: 10),
                _gradPreview(p),
                const SizedBox(height: 10),
                ..._custom.asMap().entries.map((e) => _stopRow(p, e.key)),
                if (_custom.length < 5)
                  TextButton.icon(
                    onPressed: () => setState(() {
                      _custom.add(const Color(0xFF7C6FF0));
                      widget.theme.setCustom(_custom);
                    }),
                    icon: Icon(Icons.add, color: p.muted, size: 18),
                    label: Text(_t('addColor'), style: TextStyle(color: p.muted)),
                  ),
                const SizedBox(height: 18),
                _grpLabel(p, _t('language')),
                const SizedBox(height: 10),
                _langToggle(p),
                const SizedBox(height: 18),
                SizedBox(
                  width: double.infinity,
                  child: TextButton(
                    onPressed: () => Navigator.pop(context),
                    style: TextButton.styleFrom(backgroundColor: p.surface2, foregroundColor: p.text),
                    child: Text(_t('close')),
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  Widget _grpLabel(Palette p, String s) => Text(s.toUpperCase(),
      style: TextStyle(color: p.muted, fontSize: 11, fontWeight: FontWeight.w700, letterSpacing: .6));

  Widget _gradPreview(Palette p) => Container(
        height: 46,
        decoration: BoxDecoration(
          gradient: LinearGradient(colors: _custom.length >= 2 ? _custom : [_custom.first, _custom.first]),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: p.line2),
        ),
      );

  Widget _stopRow(Palette p, int i) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        children: [
          GestureDetector(
            onTap: () => _pickColor(i),
            child: Container(
              width: 46, height: 34,
              decoration: BoxDecoration(
                color: _custom[i],
                borderRadius: BorderRadius.circular(9),
                border: Border.all(color: p.line2),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              '#${(_custom[i].value & 0xFFFFFF).toRadixString(16).padLeft(6, '0').toUpperCase()}',
              style: TextStyle(color: p.muted, fontFamily: 'Consolas', fontSize: 13),
            ),
          ),
          if (_custom.length > 2)
            IconButton(
              icon: Icon(Icons.close, color: p.muted2, size: 18),
              onPressed: () => setState(() {
                _custom.removeAt(i);
                widget.theme.setCustom(_custom);
              }),
            ),
        ],
      ),
    );
  }

  Future<void> _pickColor(int i) async {
    // Lightweight HSV picker via a simple palette grid (no extra package).
    final p = widget.theme.palette;
    final picked = await showDialog<Color>(
      context: context,
      builder: (c) => _SimpleColorPicker(initial: _custom[i], pal: p),
    );
    if (picked != null) {
      setState(() {
        _custom[i] = picked;
        widget.theme.setCustom(_custom);
      });
    }
  }

  Widget _langToggle(Palette p) {
    Widget seg(String code, String label) {
      final on = widget.i18n.lang == code;
      return Expanded(
        child: GestureDetector(
          onTap: () => widget.i18n.setLang(code),
          child: Container(
            margin: const EdgeInsets.all(2),
            padding: const EdgeInsets.symmetric(vertical: 9),
            decoration: BoxDecoration(
              gradient: on ? LinearGradient(colors: p.gradient.length >= 2 ? p.gradient : [p.brand, p.brand2]) : null,
              borderRadius: BorderRadius.circular(8),
            ),
            child: Center(
              child: Text(label,
                  style: TextStyle(
                      color: on ? Colors.white : p.muted, fontWeight: FontWeight.w700, fontSize: 13)),
            ),
          ),
        ),
      );
    }

    return Container(
      decoration: BoxDecoration(
        color: p.surface,
        border: Border.all(color: p.line),
        borderRadius: BorderRadius.circular(11),
      ),
      padding: const EdgeInsets.all(4),
      child: Row(children: [seg('ru', 'Русский'), seg('en', 'English')]),
    );
  }
}

class _ThemeSwatches extends StatelessWidget {
  final AppTheme theme;
  const _ThemeSwatches({required this.theme});
  @override
  Widget build(BuildContext context) {
    final swatches = <String, List<Color>>{
      'dark': [const Color(0xFF1b2740), const Color(0xFF0b0f1a)],
      'light': [Colors.white, const Color(0xFFcdd6ea)],
      'pink': [const Color(0xFFec4899), const Color(0xFF1f1420)],
      'green': [const Color(0xFF10b981), const Color(0xFF0f1f18)],
      'blue': [const Color(0xFF0ea5e9), const Color(0xFF0d2231)],
    };
    final pal = theme.palette;
    return Wrap(
      spacing: 11,
      runSpacing: 11,
      children: [
        ...swatches.entries.map((e) => _swatch(e.key, e.value, theme.name == e.key, pal)),
        // custom swatch
        GestureDetector(
          onTap: () => theme.setTheme('custom'),
          child: Container(
            width: 42, height: 42,
            decoration: BoxDecoration(
              gradient: LinearGradient(colors: theme.custom.length >= 2 ? theme.custom : [theme.custom.first, theme.custom.first]),
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: theme.name == 'custom' ? pal.text : Colors.transparent, width: 2),
            ),
            child: theme.name == 'custom'
                ? const Icon(Icons.check, color: Colors.white, size: 16)
                : null,
          ),
        ),
      ],
    );
  }

  Widget _swatch(String name, List<Color> cols, bool sel, Palette pal) => GestureDetector(
        onTap: () => theme.setTheme(name),
        child: Container(
          width: 42, height: 42,
          decoration: BoxDecoration(
            gradient: LinearGradient(begin: Alignment.topLeft, end: Alignment.bottomRight, colors: cols),
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: sel ? pal.text : Colors.transparent, width: 2),
          ),
          child: sel ? const Icon(Icons.check, color: Colors.white, size: 16) : null,
        ),
      );
}

/// A proper HSV color picker: a saturation/value square + a hue strip + a row of
/// quick swatches. No external package. Replaces the cramped 3-slider RGB pad.
class _SimpleColorPicker extends StatefulWidget {
  final Color initial;
  final Palette pal;
  const _SimpleColorPicker({required this.initial, required this.pal});
  @override
  State<_SimpleColorPicker> createState() => _SimpleColorPickerState();
}

class _SimpleColorPickerState extends State<_SimpleColorPicker> {
  late HSVColor _hsv = HSVColor.fromColor(widget.initial);

  static const _quick = [
    Color(0xFFef4444), Color(0xFFf59e0b), Color(0xFFfacc15), Color(0xFF22c55e),
    Color(0xFF10b981), Color(0xFF06b6d4), Color(0xFF3b82f6), Color(0xFF6366f1),
    Color(0xFF8b5cf6), Color(0xFFec4899), Color(0xFFffffff), Color(0xFF94a3b8),
  ];

  @override
  Widget build(BuildContext context) {
    final pal = widget.pal;
    final col = _hsv.toColor();
    return AlertDialog(
      backgroundColor: pal.bg2,
      content: SizedBox(
        width: 280,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // preview + hex
            Row(
              children: [
                Container(
                  width: 44, height: 44,
                  decoration: BoxDecoration(color: col, borderRadius: BorderRadius.circular(10),
                      border: Border.all(color: pal.line2)),
                ),
                const SizedBox(width: 12),
                Text('#${(col.value & 0xFFFFFF).toRadixString(16).padLeft(6, '0').toUpperCase()}',
                    style: TextStyle(color: pal.text, fontFamily: 'Consolas', fontSize: 15)),
              ],
            ),
            const SizedBox(height: 14),
            // saturation/value square
            _SVSquare(
              hue: _hsv.hue,
              saturation: _hsv.saturation,
              value: _hsv.value,
              onChanged: (s, v) => setState(() => _hsv = _hsv.withSaturation(s).withValue(v)),
            ),
            const SizedBox(height: 12),
            // hue strip
            _HueStrip(hue: _hsv.hue, onChanged: (h) => setState(() => _hsv = _hsv.withHue(h))),
            const SizedBox(height: 14),
            // quick swatches
            Wrap(
              spacing: 8, runSpacing: 8,
              children: _quick
                  .map((c) => GestureDetector(
                        onTap: () => setState(() => _hsv = HSVColor.fromColor(c)),
                        child: Container(
                          width: 26, height: 26,
                          decoration: BoxDecoration(color: c, shape: BoxShape.circle,
                              border: Border.all(color: pal.line2)),
                        ),
                      ))
                  .toList(),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        TextButton(onPressed: () => Navigator.pop(context, col), child: const Text('OK')),
      ],
    );
  }
}

/// Saturation (x) × Value (y) selection square for a fixed hue.
class _SVSquare extends StatelessWidget {
  final double hue, saturation, value;
  final void Function(double s, double v) onChanged;
  const _SVSquare({required this.hue, required this.saturation, required this.value, required this.onChanged});

  void _handle(Offset local, Size size) {
    final s = (local.dx / size.width).clamp(0.0, 1.0);
    final v = (1 - local.dy / size.height).clamp(0.0, 1.0);
    onChanged(s, v);
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, c) {
      final size = Size(c.maxWidth, 150);
      return GestureDetector(
        onPanDown: (d) => _handle(d.localPosition, size),
        onPanUpdate: (d) => _handle(d.localPosition, size),
        child: SizedBox(
          width: size.width, height: size.height,
          child: Stack(
            children: [
              Container(
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(10),
                  gradient: LinearGradient(
                    colors: [Colors.white, HSVColor.fromAHSV(1, hue, 1, 1).toColor()],
                  ),
                ),
              ),
              Container(
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(10),
                  gradient: const LinearGradient(
                    begin: Alignment.topCenter, end: Alignment.bottomCenter,
                    colors: [Colors.transparent, Colors.black],
                  ),
                ),
              ),
              Positioned(
                left: saturation * size.width - 7,
                top: (1 - value) * size.height - 7,
                child: Container(
                  width: 14, height: 14,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    border: Border.all(color: Colors.white, width: 2),
                    boxShadow: const [BoxShadow(color: Colors.black54, blurRadius: 3)],
                  ),
                ),
              ),
            ],
          ),
        ),
      );
    });
  }
}

/// Horizontal hue (0–360) strip.
class _HueStrip extends StatelessWidget {
  final double hue;
  final ValueChanged<double> onChanged;
  const _HueStrip({required this.hue, required this.onChanged});

  void _handle(Offset local, double width) =>
      onChanged((local.dx / width).clamp(0.0, 1.0) * 360);

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, c) {
      final w = c.maxWidth;
      return GestureDetector(
        onPanDown: (d) => _handle(d.localPosition, w),
        onPanUpdate: (d) => _handle(d.localPosition, w),
        child: SizedBox(
          width: w, height: 22,
          child: Stack(
            children: [
              Container(
                decoration: BoxDecoration(
                  borderRadius: BorderRadius.circular(8),
                  gradient: const LinearGradient(colors: [
                    Color(0xFFff0000), Color(0xFFffff00), Color(0xFF00ff00),
                    Color(0xFF00ffff), Color(0xFF0000ff), Color(0xFFff00ff), Color(0xFFff0000),
                  ]),
                ),
              ),
              Positioned(
                left: (hue / 360) * w - 6,
                top: -1,
                child: Container(
                  width: 12, height: 24,
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.white, width: 2),
                    boxShadow: const [BoxShadow(color: Colors.black54, blurRadius: 3)],
                  ),
                ),
              ),
            ],
          ),
        ),
      );
    });
  }
}
