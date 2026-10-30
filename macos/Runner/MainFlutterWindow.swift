import CFNetwork
import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)

    RegisterGeneratedPlugins(registry: flutterViewController)
    let channel = FlutterMethodChannel(
      name: "duanju/device", binaryMessenger: flutterViewController.engine.binaryMessenger)
    channel.setMethodCallHandler { [weak self] call, result in
      if call.method == "windowFrame" {
        guard let window = self, !window.styleMask.contains(.fullScreen),
              !window.isZoomed, !window.isMiniaturized else {
          result(nil)
          return
        }
        let frame = window.frame
        result(["left": frame.origin.x, "top": frame.origin.y,
                "width": frame.size.width, "height": frame.size.height])
        return
      }
      if call.method == "setWindowFrame" {
        guard let window = self, let args = call.arguments as? [String: Any],
              let left = (args["left"] as? NSNumber)?.doubleValue,
              let top = (args["top"] as? NSNumber)?.doubleValue,
              let width = (args["width"] as? NSNumber)?.doubleValue,
              let height = (args["height"] as? NSNumber)?.doubleValue,
              width > 0, height > 0 else {
          result(FlutterError(code: "bad_args",
                              message: "left, top, width and height are required", details: nil))
          return
        }
        let frame = NSRect(x: left, y: top, width: width, height: height)
        let title = NSRect(x: left, y: top + height - min(height, 40), width: width,
                           height: min(height, 40))
        if NSScreen.screens.contains(where: { $0.visibleFrame.intersects(title) }) {
          window.setFrame(frame, display: true)
        }
        result(nil)
        return
      }
      guard call.method == "systemProxy" else {
        result(FlutterMethodNotImplemented)
        return
      }
      let settings = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any] ?? [:]
      func address(_ prefix: String) -> String {
        guard (settings["\(prefix)Enable"] as? NSNumber)?.boolValue == true,
              let host = settings["\(prefix)Proxy"] as? String,
              let port = settings["\(prefix)Port"] as? NSNumber,
              !host.isEmpty, port.intValue > 0 else { return "" }
        let name = host.contains(":") ? "[\(host)]" : host
        return "http://\(name):\(port.intValue)"
      }
      let http = address("HTTP")
      let https = address("HTTPS")
      result(["http": http, "https": https.isEmpty ? http : https,
              "bypass": settings["ExceptionsList"] as? [String] ?? [],
              "pac": (settings["ProxyAutoConfigEnable"] as? NSNumber)?.boolValue == true])
    }

    super.awakeFromNib()
  }
}
