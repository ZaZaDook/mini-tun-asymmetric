// LogoMark: the recycling-loop triangle (PC→A→B→PC) drawn as a vector, matching
// the app icon and the WebView client's title-bar logo. Three nodes at the
// vertices, three filled arrowheads on the edge midpoints.
import 'package:flutter/material.dart';

class LogoMark extends StatelessWidget {
  final Color color;
  const LogoMark({super.key, required this.color});
  @override
  Widget build(BuildContext context) => CustomPaint(painter: _LogoPainter(color));
}

class _LogoPainter extends CustomPainter {
  final Color color;
  _LogoPainter(this.color);

  // viewBox 0..100, mapped into the widget box.
  Offset _p(Size s, double x, double y) => Offset(x / 100 * s.width, y / 100 * s.height);

  @override
  void paint(Canvas canvas, Size size) {
    final stroke = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = size.width * 0.07
      ..strokeJoin = StrokeJoin.round
      ..strokeCap = StrokeCap.round;
    final fill = Paint()..color = color..style = PaintingStyle.fill;

    final tl = _p(size, 17.9, 21.7), tr = _p(size, 83.2, 21.7), ap = _p(size, 50.5, 78.2);

    // triangle outline
    final tri = Path()
      ..moveTo(tl.dx, tl.dy)
      ..lineTo(tr.dx, tr.dy)
      ..lineTo(ap.dx, ap.dy)
      ..close();
    canvas.drawPath(tri, stroke);

    // nodes
    for (final n in [tl, tr, ap]) {
      canvas.drawCircle(n, size.width * 0.09, fill);
    }
  }

  @override
  bool shouldRepaint(_LogoPainter old) => old.color != color;
}
