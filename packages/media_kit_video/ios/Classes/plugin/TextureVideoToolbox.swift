import CoreVideo
import Flutter

/// Pulls decoded buffers from the optional GuoApp libmpv backend on the video worker.
final class TextureVideoToolbox: NSObject, FlutterTexture, ResizableTextureProtocol {
  private struct Frame {
    var buffer: Unmanaged<CVPixelBuffer>?
    var pts: Double = 0
  }

  private let updateCallback: () -> Void
  private let frameLock = NSLock()
  private let updateLock = NSLock()
  private var updateScheduled = false
  private var latest: CVPixelBuffer?
  private var loggedFirstFrame = false
  private var context: OpaquePointer?

  init?(handle: OpaquePointer, updateCallback: @escaping () -> Void) {
    self.updateCallback = updateCallback
    super.init()

    let status = "guoapp-ios-pixelbuffer".withCString { api in
      var parameters = [
        mpv_render_param(
          type: MPV_RENDER_PARAM_API_TYPE, data: UnsafeMutableRawPointer(mutating: api)),
        mpv_render_param(type: MPV_RENDER_PARAM_INVALID, data: nil),
      ]
      return mpv_render_context_create(&context, handle, &parameters)
    }
    guard status >= 0, let context else {
      NSLog("GuoApp VideoToolbox frame backend unavailable: %d", status)
      return nil
    }

    mpv_render_context_set_update_callback(context, { opaque in
      guard let opaque else { return }
      let output = Unmanaged<TextureVideoToolbox>.fromOpaque(opaque).takeUnretainedValue()
      output.scheduleUpdate()
    }, Unmanaged.passUnretained(self).toOpaque())
  }

  private func scheduleUpdate() {
    updateLock.lock()
    guard !updateScheduled else {
      updateLock.unlock()
      return
    }
    updateScheduled = true
    updateLock.unlock()
    DispatchQueue.main.async { [weak self] in self?.updateCallback() }
  }

  deinit {
    if let context {
      mpv_render_context_set_update_callback(context, nil, nil)
      mpv_render_context_free(context)
    }
  }

  func resize(_ size: CGSize) {}

  func render(_ size: CGSize) {
    guard let context else { return }
    updateLock.lock()
    updateScheduled = false
    updateLock.unlock()
    var frame = Frame()
    let status = withUnsafeMutablePointer(to: &frame) { output in
      var parameters = [
        mpv_render_param(
          type: mpv_render_param_type(rawValue: 1000), data: UnsafeMutableRawPointer(output)),
        mpv_render_param(type: MPV_RENDER_PARAM_INVALID, data: nil),
      ]
      return mpv_render_context_render(context, &parameters)
    }
    guard status >= 0 else { return }
    let buffer = frame.buffer?.takeRetainedValue()
    if !loggedFirstFrame, let buffer {
      loggedFirstFrame = true
      NSLog("GuoApp VideoToolbox first frame: %dx%d pts=%f",
            CVPixelBufferGetWidth(buffer), CVPixelBufferGetHeight(buffer), frame.pts)
    }
    frameLock.lock()
    latest = buffer
    frameLock.unlock()
  }

  func copyPixelBuffer() -> Unmanaged<CVPixelBuffer>? {
    frameLock.lock()
    defer { frameLock.unlock() }
    return latest.map { Unmanaged.passRetained($0) }
  }
}
