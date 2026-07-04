// DitheredBackground: a radial gradient with ordered-dither noise on top to kill
// 8-bit color banding (the concentric "halo rings" Gemini flagged). Two fixes
// combined: (1) extra interpolation stops widen the transition; (2) a faint
// per-pixel noise overlay (CustomPainter) breaks up the hard 8-bit steps.
import 'dart:math' as math;
import 'package:flutter/material.dart';
import '../theme.dart';

class DitheredBackground extends StatelessWidget {
  final Palette pal;
  final Widget child;
  const DitheredBackground({super.key, required this.pal, required this.child});

  @override
  Widget build(BuildContext context) {
    // Blend halo→bg through an intermediate stop so the ramp has more steps to
    // dither against (a wider, softer falloff than a single 2-color jump).
    final mid = Color.lerp(pal.halo, pal.bg, 0.5)!;
    return DecoratedBox(
      decoration: BoxDecoration(
        gradient: RadialGradient(
          center: const Alignment(0, -1.05),
          radius: 1.6, // wider spread = gentler rings
          colors: [pal.halo, mid, pal.bg],
          stops: const [0.0, 0.4, 0.95],
        ),
      ),
      child: CustomPaint(
        foregroundPainter: _DitherPainter(),
        child: child,
      ),
    );
  }
}

/// Paints a faint static noise field (±1 LSB-ish) over the whole area. Mixing a
/// little high-frequency noise dithers the smooth gradient so adjacent 8-bit
/// bands blend perceptually instead of showing a hard edge. Deterministic
/// (seeded) so it doesn't shimmer between frames. Public + reusable (also used
/// to de-band the title-bar gradient).
class DitherPainter extends CustomPainter {
  final int seed;
  const DitherPainter({this.seed = 0x9E3779B9});

  @override
  void paint(Canvas canvas, Size size) {
    final rnd = math.Random(seed);
    final count = math.min(9000, (size.width * size.height / 14).round());
    final white = Paint()..color = const Color(0x06FFFFFF);
    final black = Paint()..color = const Color(0x06000000);
    for (var i = 0; i < count; i++) {
      final x = rnd.nextDouble() * size.width;
      final y = rnd.nextDouble() * size.height;
      canvas.drawRect(Rect.fromLTWH(x, y, 1, 1), rnd.nextBool() ? white : black);
    }
  }

  @override
  bool shouldRepaint(DitherPainter old) => false;
}

// Back-compat alias for the background's foreground painter.
class _DitherPainter extends DitherPainter {
  const _DitherPainter();
}
