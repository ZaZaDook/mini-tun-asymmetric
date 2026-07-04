// Data models + carrier catalog + strict address/port validation, ported 1:1
// from the WebView client's JS (maskIPv4/v6/domain, parsePorts, validateHost).

/// One saved server profile (mirrors config.ClientProfile on the Go side).
class Profile {
  String name;
  String masterAddr; // host only — no port (carrier picks the port)
  String authToken; // base64
  String transport; // auto|utp|webrtc|quic|cs2
  List<int> customPorts; // empty unless "custom port" mode

  Profile({
    required this.name,
    required this.masterAddr,
    this.authToken = '',
    this.transport = 'auto',
    List<int>? customPorts,
  }) : customPorts = customPorts ?? [];

  Map<String, dynamic> toJson() => {
        'name': name,
        'master_addr': masterAddr,
        'auth_token': authToken,
        'transport': transport,
        if (customPorts.isNotEmpty) 'custom_ports': customPorts,
      };

  factory Profile.fromJson(Map<String, dynamic> j) => Profile(
        name: j['name'] ?? '',
        masterAddr: j['master_addr'] ?? '',
        authToken: j['auth_token'] ?? '',
        transport: j['transport'] ?? 'auto',
        customPorts: (j['custom_ports'] as List?)?.map((e) => e as int).toList() ?? [],
      );
}

/// Transport carriers shown in the Transport dropdown. `asset` is the brand SVG
/// (null = a built-in vector icon drawn in code).
class Carrier {
  final String id;
  final Map<String, String> label;
  final Map<String, String> sub;
  final String? asset;
  const Carrier(this.id, this.label, this.sub, this.asset);
}

const List<Carrier> kCarriers = [
  Carrier('auto', {'ru': 'Авто', 'en': 'Auto'}, {'ru': 'перебор', 'en': 'fallback'}, null),
  Carrier('utp', {'ru': 'BitTorrent (µTP)', 'en': 'BitTorrent (µTP)'}, {'ru': ':6881', 'en': ':6881'},
      'assets/carriers/utorrent.svg'),
  Carrier('webrtc', {'ru': 'Видеозвонок (WebRTC)', 'en': 'Video call (WebRTC)'},
      {'ru': ':3478', 'en': ':3478'}, 'assets/carriers/webrtc.svg'),
  Carrier('quic', {'ru': 'QUIC / HTTP-3', 'en': 'QUIC / HTTP-3'}, {'ru': ':443', 'en': ':443'},
      'assets/carriers/quic.svg'),
  Carrier('cs2', {'ru': 'CS2 (legacy)', 'en': 'CS2 (legacy)'}, {'ru': ':7000', 'en': ':7000'},
      'assets/carriers/cs.svg'),
  Carrier('custom', {'ru': 'Кастомный порт', 'en': 'Custom port'}, {'ru': 'свой порт', 'en': 'own port'}, null),
];

/// Carriers selectable inside the custom-port panel (concrete only).
List<Carrier> get cpCarriers =>
    kCarriers.where((c) => c.id != 'auto' && c.id != 'custom').toList();

Carrier? carrierById(String id) {
  for (final c in kCarriers) {
    if (c.id == id) return c;
  }
  return null;
}

// ── Address types + strict masks (ported from index.html) ──
enum AddrType { ipv4, ipv6, domain }

/// IPv4 mask: digits + dots only, ≤4 octets of ≤3 digits each, each clamped ≤255.
String maskIPv4(String s) {
  s = s.replaceAll(RegExp(r'[^0-9.]'), '');
  var parts = s.split('.');
  if (parts.length > 4) parts = parts.sublist(0, 4);
  parts = parts.map((p) {
    if (p.length > 3) p = p.substring(0, 3);
    if (p.isNotEmpty) {
      final n = int.parse(p);
      p = (n > 255 ? 255 : n).toString();
    }
    return p;
  }).toList();
  return parts.join('.');
}

/// IPv6 mask: hex + colon only, collapse 3+ colons to ::, ≤8 groups of ≤4 hex.
String maskIPv6(String s) {
  s = s.replaceAll(RegExp(r'[^0-9a-fA-F:]'), '');
  s = s.replaceAll(RegExp(r':{3,}'), '::');
  var g = s.split(':').map((x) => x.length > 4 ? x.substring(0, 4) : x).toList();
  if (g.length > 8) g = g.sublist(0, 8);
  return g.join(':');
}

/// Domain mask: lowercase [a-z0-9.-], no leading dot/hyphen, no empty labels.
String maskDomain(String s) {
  s = s.replaceAll(RegExp(r'[^a-zA-Z0-9.-]'), '').toLowerCase();
  s = s.replaceAll(RegExp(r'^[.-]+'), '');
  s = s.replaceAll(RegExp(r'\.{2,}'), '.');
  return s.length > 253 ? s.substring(0, 253) : s;
}

String maskAddr(AddrType t, String s) {
  switch (t) {
    case AddrType.ipv4:
      return maskIPv4(s);
    case AddrType.ipv6:
      return maskIPv6(s);
    case AddrType.domain:
      return maskDomain(s);
  }
}

/// Final strict validation on save (mask blocks most, this rejects incomplete).
bool validateHost(AddrType t, String h) {
  h = h.trim();
  if (h.isEmpty) return false;
  switch (t) {
    case AddrType.ipv4:
      final m = RegExp(r'^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$').firstMatch(h);
      if (m == null) return false;
      for (var i = 1; i <= 4; i++) {
        final o = m.group(i)!;
        final n = int.parse(o);
        if (o.length > 3 || n < 0 || n > 255 || n.toString() != int.parse(o).toString()) {
          return false;
        }
      }
      return true;
    case AddrType.ipv6:
      if (!RegExp(r'^[0-9a-fA-F:]+$').hasMatch(h)) return false;
      if (h.contains(':::')) return false;
      final dcompress = '::'.allMatches(h).length;
      if (dcompress > 1) return false;
      final groups = h.split(':');
      if (dcompress == 0 && groups.length != 8) return false;
      if (dcompress == 1 && groups.length > 8) return false;
      return groups.every((g) => g.isEmpty || RegExp(r'^[0-9a-fA-F]{1,4}$').hasMatch(g));
    case AddrType.domain:
      return RegExp(r'^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$').hasMatch(h);
  }
}

/// parsePorts: comma-separated single ports 1–65535. Ranges/junk → null.
List<int>? parsePorts(String s) {
  s = s.trim();
  if (s.isEmpty) return null;
  final parts = s.split(',').map((x) => x.trim()).where((x) => x.isNotEmpty).toList();
  if (parts.isEmpty) return null;
  final out = <int>[];
  for (final x in parts) {
    if (!RegExp(r'^\d+$').hasMatch(x)) return null;
    final n = int.parse(x);
    if (n < 1 || n > 65535) return null;
    out.add(n);
  }
  return out;
}

/// Infer address type from a saved value (for the edit modal).
AddrType inferAddrType(String v) {
  if (v.contains(':')) return AddrType.ipv6;
  if (RegExp(r'^[0-9.]+$').hasMatch(v) && v.contains('.')) return AddrType.ipv4;
  return v.isEmpty ? AddrType.ipv4 : AddrType.domain;
}
