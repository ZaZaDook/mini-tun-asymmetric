// Bilingual strings (ru/en) ported from the T table in the old index.html.
// Persisted via shared_preferences.
import 'package:flutter/widgets.dart';
import 'package:shared_preferences/shared_preferences.dart';

const Map<String, Map<String, String>> _strings = {
  'en': {
    'upload': 'Upload', 'download': 'Download', 'uptime': 'Uptime', 'servers': 'Servers',
    'addServer': '+ Add', 'labelName': 'Name', 'labelAddr': 'Master address',
    'labelToken': 'Auth Token (base64)', 'labelCarrier': 'Transport', 'phName': 'e.g. My server',
    'phToken': 'paste base64 token', 'phTokenSet': '✓ token set — leave blank to keep',
    'labelPorts': 'Ports (comma-separated)', 'phPorts': '443, 80, 7777',
    'hintPorts': 'Single ports 1–65535, comma-separated. Ranges (1024-4334) are not allowed.',
    'labelCpCarrier': 'Carrier for custom port', 'save': 'Save', 'cancel': 'Cancel',
    'close': 'Close', 'settings': 'Settings', 'theme': 'Theme', 'language': 'Language',
    'customGrad': 'Custom gradient (2–5 colors)', 'addColor': '+ Add color',
    'modalAdd': 'Add Server', 'modalEdit': 'Edit Server', 'errEmpty': 'Name and address are required',
    'errAddr': 'Enter a valid address for the selected type',
    'errPorts': 'Enter ports 1–65535, comma-separated (no ranges)', 'toastSaved': 'Saved',
    'toastDeleted': 'Deleted', 'noServer': 'Add and select a server first',
    'sDisconnected': 'Disconnected', 'sConnected': 'Protected', 'sConnecting': 'Connecting…',
    'sError': 'Error', 'delConfirm': 'Delete',
  },
  'ru': {
    'upload': 'Отдача', 'download': 'Приём', 'uptime': 'Время', 'servers': 'Серверы',
    'addServer': '+ Добавить', 'labelName': 'Название', 'labelAddr': 'Адрес мастера',
    'labelToken': 'Токен авторизации (base64)', 'labelCarrier': 'Транспорт', 'phName': 'напр. Мой сервер',
    'phToken': 'вставьте base64 токен', 'phTokenSet': '✓ токен сохранён — оставьте пустым',
    'labelPorts': 'Порты (через запятую)', 'phPorts': '443, 80, 7777',
    'hintPorts': 'Одиночные порты 1–65535 через запятую. Диапазоны (1024-4334) нельзя.',
    'labelCpCarrier': 'Носитель для кастомного порта', 'save': 'Сохранить', 'cancel': 'Отмена',
    'close': 'Закрыть', 'settings': 'Настройки', 'theme': 'Тема', 'language': 'Язык',
    'customGrad': 'Свой градиент (2–5 цветов)', 'addColor': '+ Добавить цвет',
    'modalAdd': 'Добавить сервер', 'modalEdit': 'Изменить сервер', 'errEmpty': 'Название и адрес обязательны',
    'errAddr': 'Введите корректный адрес выбранного типа',
    'errPorts': 'Введите порты 1–65535 через запятую (без диапазонов)', 'toastSaved': 'Сохранено',
    'toastDeleted': 'Удалено', 'noServer': 'Сначала добавьте и выберите сервер',
    'sDisconnected': 'Отключено', 'sConnected': 'Защищено', 'sConnecting': 'Подключение…',
    'sError': 'Ошибка', 'delConfirm': 'Удалить',
  },
};

class I18n extends ChangeNotifier {
  String _lang = 'ru';
  String get lang => _lang;

  Future<void> load() async {
    final p = await SharedPreferences.getInstance();
    _lang = p.getString('lang') ?? 'ru';
    notifyListeners();
  }

  String t(String key) => _strings[_lang]?[key] ?? _strings['en']?[key] ?? key;

  Future<void> setLang(String l) async {
    _lang = l;
    final p = await SharedPreferences.getInstance();
    await p.setString('lang', l);
    notifyListeners();
  }
}
