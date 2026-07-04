// ProfileTile: one server row — server glyph, name + address, carrier badge,
// edit/delete buttons, selection radio. Mirrors the .profile markup.
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';
import '../models.dart';
import '../theme.dart';
import 'carrier_icon.dart';

class ProfileTile extends StatelessWidget {
  final Palette pal;
  final Profile profile;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback onEdit;
  final VoidCallback onDelete;
  const ProfileTile({
    super.key,
    required this.pal,
    required this.profile,
    required this.selected,
    required this.onTap,
    required this.onEdit,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    // custom-port profile shows its chosen carrier; else the transport id.
    final cid = profile.transport.isEmpty ? 'auto' : profile.transport;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 13),
        decoration: BoxDecoration(
          color: pal.surface,
          border: Border.all(color: selected ? pal.brand : pal.line, width: 1.5),
          borderRadius: BorderRadius.circular(14),
        ),
        child: Row(
          children: [
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(
                // Solid brand fill (flat monochrome plate). On a custom theme
                // brand = the gradient's first color, which may be light — so the
                // glyph color is chosen for contrast against it (not always white).
                color: pal.brand,
                borderRadius: BorderRadius.circular(11),
              ),
              child: Builder(builder: (_) {
                final l = (0.299 * pal.brand.red + 0.587 * pal.brand.green + 0.114 * pal.brand.blue) / 255;
                final glyph = l > 0.62 ? Colors.black87 : Colors.white;
                return SvgPicture.asset(
                  'assets/carriers/servers.svg',
                  width: 21,
                  height: 21,
                  colorFilter: ColorFilter.mode(glyph, BlendMode.srcIn),
                );
              }),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(profile.name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: pal.text, fontSize: 14.5, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 2),
                  Text(profile.masterAddr,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: pal.muted, fontSize: 11.5, fontFamily: 'Consolas')),
                ],
              ),
            ),
            CarrierIcon(carrierId: cid, pal: pal, size: 20),
            IconButton(
              icon: Icon(Icons.edit_outlined, size: 16, color: pal.muted2),
              onPressed: onEdit,
              splashRadius: 18,
            ),
            IconButton(
              icon: Icon(Icons.delete_outline, size: 16, color: pal.muted2),
              onPressed: onDelete,
              splashRadius: 18,
            ),
            const SizedBox(width: 4),
            Container(
              width: 20,
              height: 20,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: selected ? pal.brand : pal.line2, width: 2),
              ),
              child: selected
                  ? Center(
                      child: Container(
                        width: 10,
                        height: 10,
                        decoration: BoxDecoration(shape: BoxShape.circle, color: pal.brand),
                      ),
                    )
                  : null,
            ),
          ],
        ),
      ),
    );
  }
}
