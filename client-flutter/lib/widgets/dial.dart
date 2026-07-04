// DialButton: the connect button shaped like our logo — a downward triangle with
// three arrowheads orbiting its perimeter. Idle = parked at edge midpoints;
// connecting/connected = running clockwise; color follows the theme/state.
// Ported from the .dial CSS (orbit animation) in the old index.html.
import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import '../theme.dart';
import '../engine.dart';

/// Arrowhead size multiplier, set at build time so we can ship 200% / 250%
/// variants for visual comparison:
///   flutter build ... --dart-define=ARROW_SCALE_X100=250
/// Base size is relative to the dial (200px), so it scales with the button.
const double kArrowScale =
    int.fromEnvironment('ARROW_SCALE_X100', defaultValue: 200) / 100.0;

class DialButton extends StatefulWidget {
  final Palette pal;
  final VpnState state;
  final VoidCallback onTap;
  const DialButton({super.key, required this.pal, required this.state, required this.onTap});
  @override
  State<DialButton> createState() => _DialButtonState();
}

class _DialButtonState extends State<DialButton> with SingleTickerProviderStateMixin {
  late final Ticker _ticker;
  double _progress = 0; // 0..1 position around the perimeter (continuous)
  double _speed = 0; // current revolutions/sec
  Duration _last = Duration.zero;
  bool _parking = false; // gliding to the nearest parked position after stop

  // Target speeds (rev/sec) per state. Idle = parked. Connecting spins up
  // (search/handshake feel); connected settles to a slow, calm glide.
  static const double _spinConnecting = 0.85;
  static const double _spinConnected = 0.28; // calm steady glide (was 0.6 — too fast)
  static const double _accel = 0.5; // slow ramp-up

  // Parked positions are at progress ≡ 0 (mod 1/3): the three arrows sit on the
  // edge midpoints. On stop we glide to the nearest such point over ~3–5s rather
  // than freezing mid-edge.
  static const double _park = 1.0 / 3.0;

  @override
  void initState() {
    super.initState();
    _ticker = createTicker(_tick)..start();
  }

  void _tick(Duration now) {
    final dt = _last == Duration.zero ? 0.0 : (now - _last).inMicroseconds / 1e6;
    _last = now;
    final running = widget.state == VpnState.connecting || widget.state == VpnState.connected;

    if (running) {
      _parking = false;
      final target = widget.state == VpnState.connecting ? _spinConnecting : _spinConnected;
      _speed += (target - _speed) * (_accel * dt).clamp(0.0, 1.0) * 4;
      if ((target - _speed).abs() < 0.005) _speed = target;
      if (_speed.abs() > 1e-4) {
        setState(() => _progress = (_progress + _speed * dt) % 1.0);
      }
      return;
    }

    // Stopped: keep gliding forward but ease the speed down toward 0, and as it
    // gets slow, steer the remaining motion so we land exactly on a parked slot
    // (a little "cheat" that stretches the wind-down to a few seconds).
    if (_speed > 1e-4 || _parking) {
      _parking = true;
      // distance (forward) to the next parked multiple of 1/3
      final frac = (_progress % _park);
      final toNext = frac < 1e-4 ? 0.0 : (_park - frac);
      // ease speed down; floor it so we always creep to the slot, then snap
      _speed += (0.0 - _speed) * (0.6 * dt).clamp(0.0, 1.0) * 4;
      if (_speed < 0.12) _speed = 0.12; // gentle creep so the glide lasts ~3–5s
      var step = _speed * dt;
      if (step >= toNext && toNext >= 0) {
        // reached the parked slot — snap and stop
        setState(() {
          _progress = (_progress + toNext) % 1.0;
          _speed = 0;
          _parking = false;
        });
      } else {
        setState(() => _progress = (_progress + step) % 1.0);
      }
    }
  }

  @override
  void dispose() {
    _ticker.dispose();
    super.dispose();
  }

  Color get _edgeColor {
    switch (widget.state) {
      case VpnState.connected:
        return widget.pal.on;
      case VpnState.connecting:
        return widget.pal.warn;
      case VpnState.error:
        return widget.pal.err;
      case VpnState.disconnected:
        return widget.pal.muted;
    }
  }

  @override
  Widget build(BuildContext context) {
    final glow = widget.state == VpnState.connected;
    return GestureDetector(
      onTap: widget.onTap,
      child: SizedBox(
        width: 200,
        height: 200,
        child: CustomPaint(
          painter: _DialPainter(
            color: _edgeColor,
            progress: _progress,
            running: _speed.abs() > 0.0001,
            glow: glow ? widget.pal.onGlow : null,
          ),
        ),
      ),
    );
  }
}

class _DialPainter extends CustomPainter {
  final Color color;
  final double progress; // 0..1 around the perimeter
  final bool running;
  final Color? glow;
  _DialPainter({required this.color, required this.progress, required this.running, this.glow});

  Offset _p(Size s, double x, double y) => Offset(x / 100 * s.width, y / 100 * s.height);

  @override
  void paint(Canvas canvas, Size size) {
    final tl = _p(size, 17.9, 21.7), tr = _p(size, 83.2, 21.7), ap = _p(size, 50.5, 78.2);
    final verts = [tl, tr, ap];

    if (glow != null) {
      final g = Paint()
        ..color = glow!
        ..maskFilter = const MaskFilter.blur(BlurStyle.normal, 14);
      final tri = Path()..addPolygon(verts, true);
      canvas.drawPath(tri, g..style = PaintingStyle.stroke..strokeWidth = 6);
    }

    final edge = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 7.2
      ..strokeJoin = StrokeJoin.round
      ..strokeCap = StrokeCap.round;
    final tri = Path()..addPolygon(verts, true);
    canvas.drawPath(tri, edge);

    // three arrowheads, 120° (1/3) apart around the perimeter
    final arr = Paint()..color = color..style = PaintingStyle.fill;
    for (var i = 0; i < 3; i++) {
      // parked positions at edge midpoints: 1/6, 1/2, 5/6
      final base = (i * 2 + 1) / 6.0;
      final pos = (base + progress) % 1.0;
      _drawArrow(canvas, size, verts, pos, arr);
    }
  }

  void _drawArrow(Canvas canvas, Size size, List<Offset> verts, double t, Paint paint) {
    // map t in [0,1) to a point + direction along the triangle perimeter
    final segLens = <double>[];
    double total = 0;
    for (var i = 0; i < 3; i++) {
      final a = verts[i], b = verts[(i + 1) % 3];
      final l = (b - a).distance;
      segLens.add(l);
      total += l;
    }
    double target = t * total;
    for (var i = 0; i < 3; i++) {
      if (target <= segLens[i] || i == 2) {
        final a = verts[i], b = verts[(i + 1) % 3];
        final f = (segLens[i] == 0) ? 0.0 : (target / segLens[i]).clamp(0.0, 1.0);
        final pt = Offset.lerp(a, b, f)!;
        final dir = (b - a);
        final ang = math.atan2(dir.dy, dir.dx);
        canvas.save();
        canvas.translate(pt.dx, pt.dy);
        canvas.rotate(ang);
        // Arrow scaled to the dial size (200 viewBox baseline) × build-time
        // multiplier. h≈unit, so the head stays proportional on any dial size.
        final u = (size.width / 200.0) * kArrowScale;
        final path = Path()
          ..moveTo(-4.5 * u, -6 * u)
          ..lineTo(6 * u, 0)
          ..lineTo(-4.5 * u, 6 * u)
          ..close();
        canvas.drawPath(path, paint);
        canvas.restore();
        return;
      }
      target -= segLens[i];
    }
  }

  @override
  bool shouldRepaint(_DialPainter old) =>
      old.progress != progress || old.color != color || old.running != running || old.glow != glow;
}
