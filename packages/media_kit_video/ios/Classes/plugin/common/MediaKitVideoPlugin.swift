import AVKit
import Flutter

public class MediaKitVideoPlugin: NSObject, FlutterPlugin {
  private static let CHANNEL_NAME = "com.alexmercerind/media_kit_video"

  public static func register(with registrar: FlutterPluginRegistrar) {
    let binaryMessenger = registrar.messenger()
    let registry = registrar.textures()
    let utils: UtilsProtocol? = nil

    let channel = FlutterMethodChannel(
      name: CHANNEL_NAME,
      binaryMessenger: binaryMessenger
    )
    let instance = MediaKitVideoPlugin(
      registry: registry,
      channel: channel,
      utils: utils
    )
    registrar.addMethodCallDelegate(instance, channel: channel)
    if #available(iOS 15.0, *) {
      let pipChannel = FlutterMethodChannel(name: "duanju/ios_pip", binaryMessenger: binaryMessenger)
      let pip = AVPictureInPictureController.isPictureInPictureSupported()
        ? PictureInPicture(channel: pipChannel) : nil
      if let pip {
        instance.videoOutputManager.onFrame = { [weak instance] handle, texture in
          guard pip.needsFrame(handle: handle),
                let frame = texture.copyPixelBuffer()?.takeRetainedValue() else { return }
          pip.enqueue(
            frame, handle: handle,
            decodedFrame: instance?.videoOutputManager.ownsFrameBuffers(handle: handle) ?? false
          )
        }
      }
      pipChannel.setMethodCallHandler { [weak instance] call, result in
        let args = call.arguments as? [String: Any]
        switch call.method {
        case "pictureInPictureStatus":
          result([
            "supported": pip != nil,
            "possible": pip?.possible ?? false,
          ])
        case "updatePictureInPicture":
          let handle = (args?["handle"] as? NSNumber)?.int64Value
          pip?.update(
            handle: handle, playing: args?["playing"] as? Bool ?? false,
            autoEnter: args?["autoEnter"] as? Bool ?? false
          )
          if let pip, let handle, pip.needsFrame(handle: handle),
             let frame = instance?.videoOutputManager.currentFrame(handle: handle) {
            pip.enqueue(
            frame, handle: handle,
            decodedFrame: instance?.videoOutputManager.ownsFrameBuffers(handle: handle) ?? false
          )
          }
          result(nil)
        case "enterPictureInPicture":
          if let pip, let handle = pip.selectedHandle,
             let frame = instance?.videoOutputManager.currentFrame(handle: handle) {
            pip.enqueue(
              frame, handle: handle, refresh: true,
              decodedFrame: instance?.videoOutputManager.ownsFrameBuffers(handle: handle) ?? false
            )
          }
          result(["requested": pip?.start() ?? false])
        case "stopPictureInPicture":
          pip?.stop()
          result(nil)
        default:
          result(FlutterMethodNotImplemented)
        }
      }
    }

  }

  private let channel: FlutterMethodChannel
  private let videoOutputManager: VideoOutputManager
  private let utils: UtilsProtocol?

  init(
    registry: FlutterTextureRegistry,
    channel: FlutterMethodChannel,
    utils: UtilsProtocol?
  ) {
    self.channel = channel
    videoOutputManager = VideoOutputManager(
      registry: registry
    )
    self.utils = utils
  }

  public func handle(
    _ call: FlutterMethodCall,
    result: @escaping FlutterResult
  ) {
    switch call.method {
    case "VideoOutputManager.Create":
      handleCreateMethodCall(call.arguments, result)
    case "VideoOutputManager.SetSize":
      handleSetSizeMethodCall(call.arguments, result)
    case "VideoOutputManager.Dispose":
      handleDisposeMethodCall(call.arguments, result)
    case "Utils.EnterNativeFullscreen":
      handleEnterNativeFullscreenMethodCall(call.arguments, result)
    case "Utils.ExitNativeFullscreen":
      handleExitNativeFullscreenMethodCall(call.arguments, result)
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  private func handleCreateMethodCall(
    _ arguments: Any?,
    _ result: FlutterResult
  ) {
    let args = arguments as? [String: Any]
    let handleStr = args?["handle"] as! String
    let handle: Int64? = Int64(handleStr)
    let configDict = args?["configuration"] as! [String: Any]
    let configuration = VideoOutputConfiguration.fromDict(configDict)

    assert(handle != nil, "handle must be an Int64")

    videoOutputManager.create(
      handle: handle!,
      configuration: configuration,
      textureUpdateCallback: { (_ textureId: Int64, _ size: CGSize) in
        self.channel.invokeMethod(
          "VideoOutput.Resize",
          arguments: [
            "handle": handle!,
            "id": textureId,
            "rect": [
              "top": 0,
              "left": 0,
              "width": size.width,
              "height": size.height,
            ],
          ] as [String: Any]
        )
      }
    )

    result(nil)
  }

  private func handleSetSizeMethodCall(
    _ arguments: Any?,
    _ result: FlutterResult
  ) {
    let args = arguments as? [String: Any]
    let handleStr = args?["handle"] as! String
    let widthStr = args?["width"] as! String
    let heightStr = args?["height"] as! String

    let handle: Int64? = Int64(handleStr)
    let width: Int64? = Int64(widthStr)
    let height: Int64? = Int64(heightStr)

    assert(handle != nil, "handle must be an Int64")

    self.videoOutputManager.setSize(
      handle: handle!,
      width: width,
      height: height
    )

    result(nil)
  }

  private func handleDisposeMethodCall(
    _ arguments: Any?,
    _ result: FlutterResult
  ) {
    let args = arguments as? [String: Any]
    let handleStr = args?["handle"] as! String
    let handle: Int64? = Int64(handleStr)

    assert(handle != nil, "handle must be an Int64")

    videoOutputManager.destroy(
      handle: handle!
    )

    result(nil)
  }

  private func handleEnterNativeFullscreenMethodCall(
    _: Any?,
    _ result: FlutterResult
  ) {
    if utils == nil {
      return result(FlutterMethodNotImplemented)
    }

    utils?.enterNativeFullscreen()
    result(nil)
  }

  private func handleExitNativeFullscreenMethodCall(
    _: Any?,
    _ result: FlutterResult
  ) {
    if utils == nil {
      return result(FlutterMethodNotImplemented)
    }

    utils?.exitNativeFullscreen()
    result(nil)
  }
}
