// HttpEngine: the real backend for desktop. It talks to the privileged Go
// sidecar (mini-tun-asymmetric-agent) over loopback HTTP with a bearer token.
//
// The sidecar writes %AppData%/MiniTunAsymmetric/agent-endpoint.json = {url,
// token, pid}; we read it, then poll /api/status once per second and expose the
// same Engine interface the UI already uses (so swapping MockEngine→HttpEngine
// changes nothing in the widgets).
import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';
import 'engine.dart';
import 'models.dart';

class HttpEngine implements Engine {
  final _status = ValueNotifier<VpnStatus>(const VpnStatus());
  final HttpClient _http = HttpClient();
  String _base = '';
  String _token = '';
  Timer? _poll;
  bool _ready = false;

  @override
  ValueListenable<VpnStatus> get status => _status;

  /// Locate the sidecar endpoint file and start polling. Returns false if the
  /// sidecar hasn't written its endpoint yet (caller may retry / spawn it).
  Future<bool> init() async {
    final ok = await _readEndpoint();
    if (!ok) return false;
    _ready = true;
    _poll?.cancel();
    _poll = Timer.periodic(const Duration(seconds: 1), (_) => _refresh());
    _refresh();
    return true;
  }

  Future<File> _endpointFile() async {
    // Windows: %AppData%\MiniTunAsymmetric ; matches the Go agent's appDataDir.
    final base = Platform.environment['APPDATA'] ??
        (await getApplicationSupportDirectory()).path;
    return File('$base${Platform.pathSeparator}MiniTunAsymmetric'
        '${Platform.pathSeparator}agent-endpoint.json');
  }

  Future<bool> _readEndpoint() async {
    try {
      final f = await _endpointFile();
      if (!await f.exists()) return false;
      final j = jsonDecode(await f.readAsString()) as Map<String, dynamic>;
      _base = j['url'] as String;
      _token = j['token'] as String;
      return _base.isNotEmpty && _token.isNotEmpty;
    } catch (_) {
      return false;
    }
  }

  Future<Map<String, dynamic>?> _get(String path) async {
    try {
      final req = await _http.getUrl(Uri.parse('$_base$path'));
      req.headers.set('Authorization', 'Bearer $_token');
      final resp = await req.close();
      if (resp.statusCode != 200) return null;
      final body = await resp.transform(utf8.decoder).join();
      return jsonDecode(body) as Map<String, dynamic>;
    } catch (_) {
      return null;
    }
  }

  Future<dynamic> _send(String method, String path, [Object? body]) async {
    final req = await _http.openUrl(method, Uri.parse('$_base$path'));
    req.headers.set('Authorization', 'Bearer $_token');
    if (body != null) {
      req.headers.contentType = ContentType.json;
      req.add(utf8.encode(jsonEncode(body)));
    }
    final resp = await req.close();
    final text = await resp.transform(utf8.decoder).join();
    if (resp.statusCode >= 400) {
      throw Exception('agent $method $path: ${resp.statusCode} $text');
    }
    return text.isEmpty ? null : jsonDecode(text);
  }

  Future<void> _refresh() async {
    final s = await _get('/api/status');
    if (s == null) return;
    _status.value = VpnStatus(
      state: _parseState(s['state'] as String? ?? ''),
      tunnelIp: s['tunnel_ip'] as String? ?? '',
      transport: s['transport'] as String? ?? '',
      upBytes: (s['up_bytes'] as num?)?.toInt() ?? 0,
      dnBytes: (s['dn_bytes'] as num?)?.toInt() ?? 0,
      uptimeSec: (s['uptime_s'] as num?)?.toInt() ?? 0,
      slaveRttMs: (s['slave_rtt_ms'] as num?)?.toInt() ?? -1,
    );
  }

  VpnState _parseState(String s) {
    switch (s) {
      case 'Connected':
        return VpnState.connected;
      case 'Connecting...':
        return VpnState.connecting;
      case 'Error':
        return VpnState.error;
      default:
        return VpnState.disconnected;
    }
  }

  @override
  Future<List<Profile>> loadProfiles() async {
    if (!_ready) return [];
    try {
      final req = await _http.getUrl(Uri.parse('$_base/api/profiles'));
      req.headers.set('Authorization', 'Bearer $_token');
      final resp = await req.close();
      final body = await resp.transform(utf8.decoder).join();
      final list = jsonDecode(body) as List? ?? [];
      return list.map((e) => Profile.fromJson(e as Map<String, dynamic>)).toList();
    } catch (_) {
      return [];
    }
  }

  /// Profile CRUD mapped to the agent's REST verbs.
  @override
  Future<List<Profile>> addProfile(Profile p) async =>
      _profilesFrom(await _send('POST', '/api/profiles', p.toJson()));

  @override
  Future<List<Profile>> updateProfile(int index, Profile p) async =>
      _profilesFrom(await _send('PUT', '/api/profiles', {'index': index, 'profile': p.toJson()}));

  @override
  Future<List<Profile>> deleteProfile(int index) async =>
      _profilesFrom(await _send('DELETE', '/api/profiles', {'index': index}));

  List<Profile> _profilesFrom(dynamic v) {
    final list = (v as List?) ?? [];
    return list.map((e) => Profile.fromJson(e as Map<String, dynamic>)).toList();
  }

  @override
  Future<void> connectIndex(int index) async {
    _status.value = const VpnStatus(state: VpnState.connecting);
    await _send('POST', '/api/connect', {'index': index});
  }

  @override
  Future<void> disconnect() async {
    await _send('POST', '/api/disconnect');
  }

  void dispose() {
    _poll?.cancel();
    _http.close(force: true);
  }
}
