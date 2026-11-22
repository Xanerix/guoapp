import CoreVideo

public class TextureSWContext {
  public let pixelBuffer: CVPixelBuffer

  init(size: CGSize) {
    self.pixelBuffer = TextureSWContext.createPixelBuffer(size)
  }

  deinit {
    TextureSWContext.disposePixelBuffer(pixelBuffer)
  }

  private static func createPixelBuffer(_ size: CGSize) -> CVPixelBuffer {
    let attrs: [String: Any] = [
      kCVPixelBufferMetalCompatibilityKey as String: true,
      kCVPixelBufferIOSurfacePropertiesKey as String: [:],
    ]

    var pixelBuffer: CVPixelBuffer?
    let cvret = CVPixelBufferCreate(
      kCFAllocatorDefault,
      Int(size.width),
      Int(size.height),
      kCVPixelFormatType_32BGRA,
      attrs as CFDictionary,
      &pixelBuffer
    )
    assert(cvret == kCVReturnSuccess, "CVPixelBufferCreate")

    return pixelBuffer!
  }

  private static func disposePixelBuffer(_ pixelBuffer: CVPixelBuffer) {
    // 'CVPixelBufferRelease' is unavailable: Core Foundation objects are
    // automatically memory managed
  }
}
