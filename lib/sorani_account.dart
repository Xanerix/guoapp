import 'dart:typed_data';

class SoraniAccount {
  const SoraniAccount({this.loggedIn = false, this.name = ''});
  factory SoraniAccount.fromJson(Map<String, dynamic> json) => SoraniAccount(
    loggedIn: json['loggedIn'] == true,
    name: json['name'] as String? ?? '',
  );
  final bool loggedIn;
  final String name;
}

class SoraniCaptcha {
  const SoraniCaptcha({
    required this.id,
    required this.background,
    required this.template,
    required this.backgroundWidth,
    required this.backgroundHeight,
    required this.templateWidth,
    required this.templateHeight,
  });
  factory SoraniCaptcha.fromJson(Map<String, dynamic> json) => SoraniCaptcha(
    id: json['id'] as String,
    background: UriData.parse(
      json['backgroundImage'] as String,
    ).contentAsBytes(),
    template: UriData.parse(json['templateImage'] as String).contentAsBytes(),
    backgroundWidth: json['backgroundWidth'] as int,
    backgroundHeight: json['backgroundHeight'] as int,
    templateWidth: json['templateWidth'] as int,
    templateHeight: json['templateHeight'] as int,
  );
  final String id;
  final Uint8List background;
  final Uint8List template;
  final int backgroundWidth;
  final int backgroundHeight;
  final int templateWidth;
  final int templateHeight;
}

class SoraniCaptchaPoint {
  const SoraniCaptchaPoint(this.x, this.y, this.type, this.t);
  final int x;
  final int y;
  final String type;
  final int t;
  Map<String, Object> toJson() => {'x': x, 'y': y, 'type': type, 't': t};
}

class SoraniCaptchaTrack {
  const SoraniCaptchaTrack({
    required this.backgroundWidth,
    required this.backgroundHeight,
    required this.templateWidth,
    required this.templateHeight,
    required this.startTime,
    required this.stopTime,
    required this.points,
  });
  final int backgroundWidth;
  final int backgroundHeight;
  final int templateWidth;
  final int templateHeight;
  final int startTime;
  final int stopTime;
  final List<SoraniCaptchaPoint> points;
  Map<String, Object> toJson() => {
    'bgImageWidth': backgroundWidth,
    'bgImageHeight': backgroundHeight,
    'templateImageWidth': templateWidth,
    'templateImageHeight': templateHeight,
    'startTime': startTime,
    'stopTime': stopTime,
    'trackList': [for (final point in points) point.toJson()],
  };
}
