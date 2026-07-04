// CarrierIcon: renders a carrier's brand SVG on a theme-tinted backing plate,
// or a built-in vector for auto/custom. Mirrors carrierIcon() + --icon-bg from
// the WebView client (light plate tinted toward the gradient's first color).
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';
import '../models.dart';
import '../theme.dart';

class CarrierIcon extends StatelessWidget {
  final String carrierId;
  final Palette pal;
  final double size;
  const CarrierIcon({super.key, required this.carrierId, required this.pal, this.size = 20});

  @override
  Widget build(BuildContext context) {
    final c = carrierById(carrierId);
    if (carrierId == 'auto') {
      return Icon(Icons.autorenew, size: size, color: pal.text);
    }
    if (carrierId == 'custom') {
      return Icon(Icons.lock_outline, size: size, color: pal.text);
    }
    if (c?.asset == null) {
      return Icon(Icons.shield_outlined, size: size, color: pal.text);
    }
    // backing plate tinted toward the gradient start (icon-bg, 12%).
    final plate = Color.lerp(Colors.white, pal.gradient.first, 0.12)!;
    return Container(
      width: size + 4,
      height: size + 4,
      padding: const EdgeInsets.all(2),
      decoration: BoxDecoration(color: plate, borderRadius: BorderRadius.circular(5)),
      child: SvgPicture.asset(c!.asset!, width: size, height: size, fit: BoxFit.contain),
    );
  }
}
