// Engine facade the UI talks to. Two implementations share it:
//  - MockEngine: in-memory, fake stats — for UI development (Phase A1a).
//  - HttpEngine: drives the privileged Go sidecar over loopback (Phase A1b+).
// Profiles are index-addressed because the sidecar's connect/CRUD API is.
import 'dart:async';
import 'package:flutter/foundation.dart';
import 'models.dart';

enum VpnState { disconnected, connecting, connected, error }

class VpnStatus {
  final VpnState state;
  final String tunnelIp;
  final String transport;
  final int upBytes;
  final int dnBytes;
  final int uptimeSec;
  final int slaveRttMs; // -1 if unknown
  const VpnStatus({
    this.state = VpnState.disconnected,
    this.tunnelIp = '',
    this.transport = '',
    this.upBytes = 0,
    this.dnBytes = 0,
    this.uptimeSec = 0,
    this.slaveRttMs = -1,
  });
}

/// The interface every backend implements. Profile mutations return the updated
/// list (the source of truth is the backend, e.g. the sidecar's config.json).
abstract class Engine {
  ValueListenable<VpnStatus> get status;
  Future<List<Profile>> loadProfiles();
  Future<List<Profile>> addProfile(Profile p);
  Future<List<Profile>> updateProfile(int index, Profile p);
  Future<List<Profile>> deleteProfile(int index);
  Future<void> connectIndex(int index);
  Future<void> disconnect();
}

/// MockEngine: fakes the connect lifecycle + ticking stats and keeps profiles in
/// memory, so the UI is fully exercisable without the Go core.
class MockEngine implements Engine {
  final _status = ValueNotifier<VpnStatus>(const VpnStatus());
  final List<Profile> _profiles = [
    Profile(name: 'Mini-tun (µTP)', masterAddr: '203.0.113.10', transport: 'utp', authToken: 'demo'),
  ];
  Timer? _ticker;
  int _up = 0, _dn = 0, _t = 0;
  String _tr = '';

  @override
  ValueListenable<VpnStatus> get status => _status;

  @override
  Future<List<Profile>> loadProfiles() async => List.of(_profiles);

  @override
  Future<List<Profile>> addProfile(Profile p) async {
    _profiles.add(p);
    return List.of(_profiles);
  }

  @override
  Future<List<Profile>> updateProfile(int index, Profile p) async {
    if (index >= 0 && index < _profiles.length) _profiles[index] = p;
    return List.of(_profiles);
  }

  @override
  Future<List<Profile>> deleteProfile(int index) async {
    if (index >= 0 && index < _profiles.length) _profiles.removeAt(index);
    return List.of(_profiles);
  }

  @override
  Future<void> connectIndex(int index) async {
    final p = _profiles[index];
    _tr = p.transport == 'auto' ? 'utp' : p.transport;
    _status.value = const VpnStatus(state: VpnState.connecting);
    await Future.delayed(const Duration(milliseconds: 900));
    _up = _dn = _t = 0;
    _ticker?.cancel();
    _ticker = Timer.periodic(const Duration(seconds: 1), (_) {
      _t++;
      _up += 40000 + (_t * 137 % 9000);
      _dn += 120000 + (_t * 911 % 40000);
      _status.value = VpnStatus(
        state: VpnState.connected, tunnelIp: '10.8.0.7', transport: _tr,
        upBytes: _up, dnBytes: _dn, uptimeSec: _t, slaveRttMs: 28,
      );
    });
    _status.value = VpnStatus(state: VpnState.connected, tunnelIp: '10.8.0.7', transport: _tr, slaveRttMs: 28);
  }

  @override
  Future<void> disconnect() async {
    _ticker?.cancel();
    _ticker = null;
    _status.value = const VpnStatus(state: VpnState.disconnected);
  }
}
