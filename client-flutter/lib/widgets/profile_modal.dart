// Profile add/edit modal: name, master address (type IPv4/IPv6/Domain + strict
// live mask), auth token, transport carrier dropdown, and a custom-port panel
// (carrier + comma-separated ports). Validation mirrors the WebView client.
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../models.dart';
import '../theme.dart';
import 'carrier_icon.dart';

Future<Profile?> showProfileModal(
  BuildContext context, {
  required Palette pal,
  required String Function(String) t,
  Profile? existing,
}) {
  return showDialog<Profile>(
    context: context,
    barrierColor: Colors.black.withOpacity(.7),
    builder: (_) => _ProfileModal(pal: pal, t: t, existing: existing),
  );
}

class _ProfileModal extends StatefulWidget {
  final Palette pal;
  final String Function(String) t;
  final Profile? existing;
  const _ProfileModal({required this.pal, required this.t, this.existing});
  @override
  State<_ProfileModal> createState() => _ProfileModalState();
}

class _ProfileModalState extends State<_ProfileModal> {
  late final TextEditingController _name;
  late final TextEditingController _addr;
  late final TextEditingController _token;
  late final TextEditingController _ports;
  AddrType _addrType = AddrType.ipv4;
  String _carrier = 'auto'; // main dropdown (or 'custom')
  String _cpCarrier = 'utp'; // carrier inside custom-port panel
  String? _err;

  @override
  void initState() {
    super.initState();
    final e = widget.existing;
    _name = TextEditingController(text: e?.name ?? '');
    _addr = TextEditingController(text: e?.masterAddr ?? '');
    _token = TextEditingController();
    _ports = TextEditingController(text: e?.customPorts.join(', ') ?? '');
    if (e != null) {
      _addrType = inferAddrType(e.masterAddr);
      if (e.customPorts.isNotEmpty) {
        _carrier = 'custom';
        _cpCarrier = (e.transport.isNotEmpty && e.transport != 'auto') ? e.transport : 'utp';
      } else {
        _carrier = e.transport.isEmpty ? 'auto' : e.transport;
      }
    }
  }

  @override
  void dispose() {
    _name.dispose();
    _addr.dispose();
    _token.dispose();
    _ports.dispose();
    super.dispose();
  }

  String get _addrPlaceholder => switch (_addrType) {
        AddrType.ipv4 => '203.0.113.10',
        AddrType.ipv6 => '2606:4700:4700::1111',
        AddrType.domain => 'vpn.example.com',
      };

  void _setAddrType(AddrType x) {
    setState(() {
      _addrType = x;
      // re-mask current value; clears incompatible content
      _addr.text = maskAddr(x, _addr.text);
      _err = null;
    });
  }

  void _save() {
    final name = _name.text.trim();
    final addr = _addr.text.trim();
    final token = _token.text.trim();
    if (name.isEmpty || addr.isEmpty) {
      setState(() => _err = widget.t('errEmpty'));
      return;
    }
    if (!validateHost(_addrType, addr)) {
      setState(() => _err = widget.t('errAddr'));
      return;
    }
    var carrier = _carrier;
    List<int> ports = [];
    if (_carrier == 'custom') {
      carrier = _cpCarrier;
      final parsed = parsePorts(_ports.text);
      if (parsed == null) {
        setState(() => _err = widget.t('errPorts'));
        return;
      }
      ports = parsed;
    }
    var authToken = token;
    if (widget.existing != null && token.isEmpty && widget.existing!.authToken.isNotEmpty) {
      authToken = widget.existing!.authToken; // keep existing on blank edit
    }
    Navigator.pop(
      context,
      Profile(name: name, masterAddr: addr, authToken: authToken, transport: carrier, customPorts: ports),
    );
  }

  @override
  Widget build(BuildContext context) {
    final pal = widget.pal;
    final t = widget.t;
    final hasTok = widget.existing?.authToken.isNotEmpty ?? false;
    return Dialog(
      backgroundColor: pal.bg2,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 410),
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 22, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(widget.existing != null ? t('modalEdit') : t('modalAdd'),
                  style: TextStyle(color: pal.text, fontSize: 17, fontWeight: FontWeight.w700)),
              const SizedBox(height: 16),
              _label(t('labelName')),
              _input(_name, hint: t('phName')),
              const SizedBox(height: 13),
              _label(t('labelAddr')),
              Row(
                children: [
                  SizedBox(
                    width: 104,
                    child: _AddrTypeDropdown(pal: pal, value: _addrType, onChanged: _setAddrType),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: _input(
                      _addr,
                      hint: _addrPlaceholder,
                      formatters: [_MaskFormatter((s) => maskAddr(_addrType, s))],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 13),
              _label(t('labelToken')),
              _input(_token, hint: hasTok ? t('phTokenSet') : t('phToken'), obscure: true),
              const SizedBox(height: 13),
              _label(t('labelCarrier')),
              _CarrierDropdown(
                pal: pal,
                lang: _lang,
                value: _carrier,
                items: kCarriers.map((c) => c.id).toList(),
                onChanged: (v) => setState(() => _carrier = v),
              ),
              if (_carrier == 'custom') ...[
                const SizedBox(height: 13),
                _label(t('labelCpCarrier')),
                _CarrierDropdown(
                  pal: pal,
                  lang: _lang,
                  value: _cpCarrier,
                  items: cpCarriers.map((c) => c.id).toList(),
                  onChanged: (v) => setState(() => _cpCarrier = v),
                ),
                const SizedBox(height: 13),
                _label(t('labelPorts')),
                _input(
                  _ports,
                  hint: t('phPorts'),
                  formatters: [_MaskFormatter((s) => s.replaceAll(RegExp(r'[^0-9, ]'), ''))],
                ),
                const SizedBox(height: 6),
                Text(t('hintPorts'), style: TextStyle(color: pal.muted2, fontSize: 11)),
              ],
              if (_err != null) ...[
                const SizedBox(height: 10),
                Text(_err!, style: TextStyle(color: pal.err, fontSize: 13)),
              ],
              const SizedBox(height: 16),
              Row(
                children: [
                  Expanded(
                    child: FilledButton(
                      onPressed: _save,
                      style: FilledButton.styleFrom(backgroundColor: pal.brand),
                      child: Text(t('save')),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: TextButton(
                      onPressed: () => Navigator.pop(context),
                      style: TextButton.styleFrom(backgroundColor: pal.surface2, foregroundColor: pal.text),
                      child: Text(t('cancel')),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  // language for carrier labels comes from the global i18n; passed implicitly via
  // a lightweight getter to avoid importing main. We read it through a static.
  String get _lang => activeLangGetter?.call() ?? 'ru';

  Widget _label(String s) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(s.toUpperCase(),
            style: TextStyle(color: widget.pal.muted, fontSize: 11, fontWeight: FontWeight.w600, letterSpacing: .5)),
      );

  Widget _input(TextEditingController c,
      {String? hint, bool obscure = false, List<TextInputFormatter>? formatters}) {
    final pal = widget.pal;
    return TextField(
      controller: c,
      obscureText: obscure,
      inputFormatters: formatters,
      enableSuggestions: false,
      autocorrect: false,
      style: TextStyle(color: pal.text, fontSize: 14),
      decoration: InputDecoration(
        isDense: true,
        hintText: hint,
        hintStyle: TextStyle(color: pal.muted2),
        filled: true,
        fillColor: pal.surface,
        contentPadding: const EdgeInsets.symmetric(horizontal: 13, vertical: 12),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(11),
          borderSide: BorderSide(color: pal.line, width: 1.5),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(11),
          borderSide: BorderSide(color: pal.brand, width: 1.5),
        ),
      ),
    );
  }
}

/// A hook so the modal can read the active language without importing main.dart
/// (set once at startup). Keeps the widget file dependency-light.
String Function()? activeLangGetter;

/// Live input mask formatter built from a transform function.
class _MaskFormatter extends TextInputFormatter {
  final String Function(String) transform;
  _MaskFormatter(this.transform);
  @override
  TextEditingValue formatEditUpdate(TextEditingValue oldV, TextEditingValue newV) {
    final cleaned = transform(newV.text);
    return TextEditingValue(
      text: cleaned,
      selection: TextSelection.collapsed(offset: cleaned.length),
    );
  }
}

class _AddrTypeDropdown extends StatelessWidget {
  final Palette pal;
  final AddrType value;
  final ValueChanged<AddrType> onChanged;
  const _AddrTypeDropdown({required this.pal, required this.value, required this.onChanged});
  @override
  Widget build(BuildContext context) {
    const labels = {AddrType.ipv4: 'IPv4', AddrType.ipv6: 'IPv6', AddrType.domain: 'Domain'};
    return Container(
      decoration: BoxDecoration(
        color: pal.surface,
        border: Border.all(color: pal.line, width: 1.5),
        borderRadius: BorderRadius.circular(11),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 10),
      child: DropdownButtonHideUnderline(
        child: DropdownButton<AddrType>(
          value: value,
          isExpanded: true,
          dropdownColor: pal.surface2,
          style: TextStyle(color: pal.text, fontSize: 13),
          icon: Icon(Icons.keyboard_arrow_down, color: pal.muted, size: 18),
          items: AddrType.values
              .map((t) => DropdownMenuItem(value: t, child: Text(labels[t]!)))
              .toList(),
          onChanged: (v) => v != null ? onChanged(v) : null,
        ),
      ),
    );
  }
}

class _CarrierDropdown extends StatelessWidget {
  final Palette pal;
  final String lang;
  final String value;
  final List<String> items;
  final ValueChanged<String> onChanged;
  const _CarrierDropdown({
    required this.pal,
    required this.lang,
    required this.value,
    required this.items,
    required this.onChanged,
  });
  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: pal.surface,
        border: Border.all(color: pal.line, width: 1.5),
        borderRadius: BorderRadius.circular(11),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: DropdownButtonHideUnderline(
        child: DropdownButton<String>(
          value: value,
          isExpanded: true,
          dropdownColor: pal.surface2,
          style: TextStyle(color: pal.text, fontSize: 13.5),
          icon: Icon(Icons.keyboard_arrow_down, color: pal.muted, size: 18),
          items: items.map((id) {
            final c = carrierById(id)!;
            return DropdownMenuItem(
              value: id,
              child: Row(
                children: [
                  CarrierIcon(carrierId: id, pal: pal, size: 18),
                  const SizedBox(width: 10),
                  Flexible(child: Text(c.label[lang] ?? c.label['en']!, overflow: TextOverflow.ellipsis)),
                ],
              ),
            );
          }).toList(),
          onChanged: (v) => v != null ? onChanged(v) : null,
        ),
      ),
    );
  }
}
