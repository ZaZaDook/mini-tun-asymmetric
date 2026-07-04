// Validation logic tests (mask/parse), ported from the WebView client's JS suite.
import 'package:flutter_test/flutter_test.dart';
import 'package:mini_tun_asymmetric/models.dart';

void main() {
  test('IPv4 mask clamps and strips', () {
    expect(maskIPv4('256'), '255');
    expect(maskIPv4('abc12'), '12');
    expect(maskIPv4('1.2.3.4.5'), '1.2.3.4');
    expect(maskIPv4('999'), '255');
  });

  test('IPv4 validate', () {
    expect(validateHost(AddrType.ipv4, '203.0.113.10'), true);
    expect(validateHost(AddrType.ipv4, '256.1.1.1'), false);
    expect(validateHost(AddrType.ipv4, '1.2.3'), false);
    expect(validateHost(AddrType.ipv4, '123123'), false);
  });

  test('IPv6 mask + validate', () {
    expect(maskIPv6('2606::::1'), '2606::1');
    expect(validateHost(AddrType.ipv6, 'fd00::2'), true);
    expect(validateHost(AddrType.ipv6, 'fd00:::2'), false);
    expect(validateHost(AddrType.ipv6, 'fd0g::2'), false);
  });

  test('domain mask + validate', () {
    expect(maskDomain('vpn!!.example.com'), 'vpn.example.com');
    expect(validateHost(AddrType.domain, 'vpn.example.com'), true);
    expect(validateHost(AddrType.domain, 'localhost'), false);
  });

  test('parsePorts rejects ranges and junk', () {
    expect(parsePorts('1024-4334'), null);
    expect(parsePorts('443, 80, 7777'), [443, 80, 7777]);
    expect(parsePorts('0'), null);
    expect(parsePorts('70000'), null);
    expect(parsePorts('abc'), null);
  });
}
