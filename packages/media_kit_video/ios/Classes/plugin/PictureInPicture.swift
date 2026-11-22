import AVKit
import Flutter
import UIKit

@available(iOS 15.0, *)
final class PictureInPicture: NSObject, AVPictureInPictureControllerDelegate,
    AVPictureInPictureSampleBufferPlaybackDelegate {
  private let channel: FlutterMethodChannel
  private let layer = AVSampleBufferDisplayLayer()
  private lazy var controller = AVPictureInPictureController(contentSource: .init(
    sampleBufferDisplayLayer: layer,
    playbackDelegate: self
  ))
  private var pool: CVPixelBufferPool?
  private var dimensions: CGSize = .zero
  private(set) var selectedHandle: Int64?
  private var playing = false
  private var starting = false
  private var hasFrame = false
  private var possibleObservation: NSKeyValueObservation?

  var possible: Bool { selectedHandle != nil && hasFrame && controller.isPictureInPicturePossible }

  init(channel: FlutterMethodChannel) {
    self.channel = channel
    super.init()
    layer.videoGravity = .resizeAspect
    controller.delegate = self
    controller.requiresLinearPlayback = true
    possibleObservation = controller.observe(\.isPictureInPicturePossible, options: [.new]) {
      [weak self] _, _ in
      DispatchQueue.main.async { self?.publishAvailability() }
    }
  }

  func update(handle: Int64?, playing: Bool, autoEnter: Bool) {
    if self.selectedHandle != handle {
      if self.selectedHandle == nil, handle != nil {
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.playback, mode: .moviePlayback)
        try? session.setActive(true)
      }
      self.selectedHandle = handle
      hasFrame = false
      layer.flushAndRemoveImage()
      controller.invalidatePlaybackState()
      publishAvailability()
    }
    if handle == nil {
      controller.canStartPictureInPictureAutomaticallyFromInline = false
      if controller.isPictureInPictureActive {
        controller.stopPictureInPicture()
      } else if !starting {
        layer.removeFromSuperlayer()
      }
      return
    }
    controller.canStartPictureInPictureAutomaticallyFromInline = autoEnter
    if self.playing != playing {
      self.playing = playing
      controller.invalidatePlaybackState()
    }
    attachLayer()
  }

  private func attachLayer() {
    guard let window = UIApplication.shared.connectedScenes
      .compactMap({ $0 as? UIWindowScene })
      .flatMap({ $0.windows })
      .first(where: { $0.isKeyWindow }),
      let root = window.rootViewController?.view,
      let parent = root.layer.superlayer else { return }
    if layer.superlayer !== parent {
      parent.insertSublayer(layer, below: root.layer)
    }
    layer.frame = root.layer.frame
  }

  func start() -> Bool {
    guard possible else { return false }
    starting = true
    controller.startPictureInPicture()
    return true
  }

  func stop() {
    if controller.isPictureInPictureActive { controller.stopPictureInPicture() }
  }

  private func publishAvailability() {
    channel.invokeMethod("pictureInPicturePossible", arguments: possible)
  }

  func needsFrame(handle: Int64) -> Bool {
    self.selectedHandle == handle && (!hasFrame || starting || controller.isPictureInPictureActive)
  }

  func enqueue(_ source: CVPixelBuffer, handle: Int64, refresh: Bool = false) {
    guard self.selectedHandle == handle, refresh || needsFrame(handle: handle) else { return }
    if layer.superlayer == nil { attachLayer() }
    if layer.status == .failed { layer.flush() }
    guard layer.isReadyForMoreMediaData, let buffer = copyFrame(source) else { return }
    CVBufferSetAttachment(buffer, kCVImageBufferAlphaChannelIsOpaque, kCFBooleanTrue, .shouldPropagate)
    var format: CMVideoFormatDescription?
    guard CMVideoFormatDescriptionCreateForImageBuffer(
      allocator: kCFAllocatorDefault, imageBuffer: buffer, formatDescriptionOut: &format
    ) == noErr, let format else { return }
    var timing = CMSampleTimingInfo(
      duration: .invalid,
      presentationTimeStamp: CMClockGetTime(CMClockGetHostTimeClock()),
      decodeTimeStamp: .invalid
    )
    var sample: CMSampleBuffer?
    guard CMSampleBufferCreateReadyWithImageBuffer(
      allocator: kCFAllocatorDefault, imageBuffer: buffer, formatDescription: format,
      sampleTiming: &timing, sampleBufferOut: &sample
    ) == noErr, let sample else { return }
    if let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true),
       let entry = CFArrayGetValueAtIndex(attachments, 0) {
      let dictionary = unsafeBitCast(entry, to: CFMutableDictionary.self)
      CFDictionarySetValue(
        dictionary, Unmanaged.passUnretained(kCMSampleAttachmentKey_DisplayImmediately).toOpaque(),
        Unmanaged.passUnretained(kCFBooleanTrue).toOpaque()
      )
    }
    layer.enqueue(sample)
    if !hasFrame {
      hasFrame = true
      controller.invalidatePlaybackState()
      publishAvailability()
    }
  }

  // AVKit may retain a sample after mpv has recycled its render buffer.
  private func copyFrame(_ source: CVPixelBuffer) -> CVPixelBuffer? {
    let width = CVPixelBufferGetWidth(source)
    let height = CVPixelBufferGetHeight(source)
    let size = CGSize(width: width, height: height)
    if dimensions != size {
      dimensions = size
      pool = nil
    }
    if pool == nil {
      let attributes: [String: Any] = [
        kCVPixelBufferWidthKey as String: width,
        kCVPixelBufferHeightKey as String: height,
        kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA,
        kCVPixelBufferIOSurfacePropertiesKey as String: [:],
      ]
      CVPixelBufferPoolCreate(kCFAllocatorDefault, nil, attributes as CFDictionary, &pool)
    }
    guard let pool else { return nil }
    let limits = [kCVPixelBufferPoolAllocationThresholdKey as String: 8] as CFDictionary
    var destination: CVPixelBuffer?
    guard CVPixelBufferPoolCreatePixelBufferWithAuxAttributes(
      kCFAllocatorDefault, pool, limits, &destination
    ) == kCVReturnSuccess, let destination else { return nil }
    guard CVPixelBufferLockBaseAddress(source, .readOnly) == kCVReturnSuccess else { return nil }
    defer { CVPixelBufferUnlockBaseAddress(source, .readOnly) }
    guard CVPixelBufferLockBaseAddress(destination, []) == kCVReturnSuccess else { return nil }
    defer { CVPixelBufferUnlockBaseAddress(destination, []) }
    guard let from = CVPixelBufferGetBaseAddress(source),
          let to = CVPixelBufferGetBaseAddress(destination) else { return nil }
    let sourceStride = CVPixelBufferGetBytesPerRow(source)
    let targetStride = CVPixelBufferGetBytesPerRow(destination)
    for row in 0..<height {
      let input = from.advanced(by: row * sourceStride)
      let output = to.advanced(by: row * targetStride)
      memcpy(output, input, width * 4)
    }
    return destination
  }

  func pictureInPictureControllerWillStartPictureInPicture(_ controller: AVPictureInPictureController) {
    starting = true
    channel.invokeMethod("pictureInPicturePending", arguments: nil)
  }

  func pictureInPictureControllerDidStartPictureInPicture(_ controller: AVPictureInPictureController) {
    starting = false
    if selectedHandle == nil {
      controller.stopPictureInPicture()
      return
    }
    channel.invokeMethod("pictureInPictureChanged", arguments: ["active": true])
  }

  func pictureInPictureController(
    _ controller: AVPictureInPictureController, failedToStartPictureInPictureWithError error: Error
  ) {
    finish()
  }

  func pictureInPictureControllerDidStopPictureInPicture(_ controller: AVPictureInPictureController) {
    finish()
  }

  private func finish() {
    starting = false
    if selectedHandle == nil { layer.removeFromSuperlayer() }
    channel.invokeMethod("pictureInPictureChanged", arguments: ["active": false])
  }

  func pictureInPictureController(
    _ controller: AVPictureInPictureController,
    restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completion: @escaping (Bool) -> Void
  ) {
    completion(selectedHandle != nil)
  }

  func pictureInPictureController(_ controller: AVPictureInPictureController, setPlaying playing: Bool) {
    channel.invokeMethod("pictureInPictureAction", arguments: ["action": playing ? "play" : "pause"])
  }

  func pictureInPictureControllerIsPlaybackPaused(_ controller: AVPictureInPictureController) -> Bool {
    !playing
  }

  func pictureInPictureControllerTimeRangeForPlayback(_ controller: AVPictureInPictureController) -> CMTimeRange {
    hasFrame ? CMTimeRange(start: .negativeInfinity, duration: .positiveInfinity) : .invalid
  }

  func pictureInPictureController(
    _ controller: AVPictureInPictureController, didTransitionToRenderSize newRenderSize: CMVideoDimensions
  ) {}

  func pictureInPictureController(
    _ controller: AVPictureInPictureController, skipByInterval skipInterval: CMTime,
    completion completionHandler: @escaping () -> Void
  ) {
    completionHandler()
  }
}
