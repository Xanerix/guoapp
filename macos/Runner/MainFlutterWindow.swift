import CFNetwork
import Carbon
import Cocoa
import FlutterMacOS

#if arch(x86_64)
private func useSkiaRendering() {
  guard let method = class_getInstanceMethod(
    FlutterDartProject.self, NSSelectorFromString("enableImpeller")) else { return }
  let disabled: @convention(block) (AnyObject) -> ObjCBool = { _ in false }
  method_setImplementation(method, imp_implementationWithBlock(disabled))
}
#endif

private let bossKeyCodes: [String: Int] = [
  "A": kVK_ANSI_A, "B": kVK_ANSI_B, "C": kVK_ANSI_C, "D": kVK_ANSI_D, "E": kVK_ANSI_E,
  "F": kVK_ANSI_F, "G": kVK_ANSI_G, "H": kVK_ANSI_H, "I": kVK_ANSI_I, "J": kVK_ANSI_J,
  "K": kVK_ANSI_K, "L": kVK_ANSI_L, "M": kVK_ANSI_M, "N": kVK_ANSI_N, "O": kVK_ANSI_O,
  "P": kVK_ANSI_P, "Q": kVK_ANSI_Q, "R": kVK_ANSI_R, "S": kVK_ANSI_S, "T": kVK_ANSI_T,
  "U": kVK_ANSI_U, "V": kVK_ANSI_V, "W": kVK_ANSI_W, "X": kVK_ANSI_X, "Y": kVK_ANSI_Y,
  "Z": kVK_ANSI_Z,
  "0": kVK_ANSI_0, "1": kVK_ANSI_1, "2": kVK_ANSI_2, "3": kVK_ANSI_3, "4": kVK_ANSI_4,
  "5": kVK_ANSI_5, "6": kVK_ANSI_6, "7": kVK_ANSI_7, "8": kVK_ANSI_8, "9": kVK_ANSI_9,
  "F1": kVK_F1, "F2": kVK_F2, "F3": kVK_F3, "F4": kVK_F4, "F5": kVK_F5, "F6": kVK_F6,
  "F7": kVK_F7, "F8": kVK_F8, "F9": kVK_F9, "F10": kVK_F10, "F11": kVK_F11, "F12": kVK_F12,
]

private var bossKeyRef: EventHotKeyRef?
private var bossKeyHandler: EventHandlerRef?

private func toggleHidden() {
  if NSApp.isHidden {
    NSApp.unhide(nil)
    NSApp.activate(ignoringOtherApps: true)
  } else {
    NSApp.hide(nil)
  }
}

private func setBossKey(_ args: [String: Any]?) -> Bool {
  if let ref = bossKeyRef {
    UnregisterEventHotKey(ref)
    bossKeyRef = nil
  }
  guard let args else { return true }
  guard let key = args["key"] as? String, let code = bossKeyCodes[key] else { return false }
  if bossKeyHandler == nil {
    var type = EventTypeSpec(
      eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
    InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
      toggleHidden()
      return noErr
    }, 1, &type, nil, &bossKeyHandler)
  }
  var modifiers: UInt32 = 0
  if args["ctrl"] as? Bool == true { modifiers |= UInt32(controlKey) }
  if args["alt"] as? Bool == true { modifiers |= UInt32(optionKey) }
  if args["shift"] as? Bool == true { modifiers |= UInt32(shiftKey) }
  if args["meta"] as? Bool == true { modifiers |= UInt32(cmdKey) }
  let id = EventHotKeyID(signature: OSType(0x444A424B), id: 1)
  return RegisterEventHotKey(
    UInt32(code), modifiers, id, GetApplicationEventTarget(), 0, &bossKeyRef) == noErr
}

class MainFlutterWindow: NSWindow {
  override func awakeFromNib() {
    #if arch(x86_64)
    useSkiaRendering()
    #endif
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
      if call.method == "setBossKey" {
        result(setBossKey(call.arguments as? [String: Any]))
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
