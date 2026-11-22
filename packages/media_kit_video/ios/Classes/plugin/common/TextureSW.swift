#if canImport(Flutter)
  import Flutter
#elseif canImport(FlutterMacOS)
  import FlutterMacOS
#endif

public class TextureSW: NSObject, FlutterTexture, ResizableTextureProtocol {
  public typealias UpdateCallback = () -> Void

  private let handle: OpaquePointer
  private let updateCallback: UpdateCallback
  private var renderContext: OpaquePointer?
  private var textureContexts = SwappableObjectManager<TextureSWContext>(
    objects: [],
    skipCheckArgs: true
  )

  init(
    handle: OpaquePointer,
    updateCallback: @escaping UpdateCallback
  ) {
    self.handle = handle
    self.updateCallback = updateCallback

    super.init()

    initMPV()
  }

  deinit {
    disposePixelBuffer()
    disposeMPV()
  }

  public func copyPixelBuffer() -> Unmanaged<CVPixelBuffer>? {
    let textureContext = textureContexts.current
    if textureContext == nil {
      return nil
    }

    return Unmanaged.passRetained(textureContext!.pixelBuffer)
  }

  private func initMPV() {
    let api = UnsafeMutableRawPointer(
      mutating: (MPV_RENDER_API_TYPE_SW as NSString).utf8String
    )
    var params: [mpv_render_param] = [
      mpv_render_param(type: MPV_RENDER_PARAM_API_TYPE, data: api),
      mpv_render_param(type: MPV_RENDER_PARAM_INVALID, data: nil),
    ]

    MPVHelpers.checkError(
      mpv_render_context_create(&renderContext, handle, &params)
    )

    mpv_render_context_set_update_callback(
      renderContext,
      { (ctx) in
        let that = unsafeBitCast(ctx, to: TextureSW.self)
        DispatchQueue.main.async {
          that.updateCallback()
        }
      },
      UnsafeMutableRawPointer(Unmanaged.passUnretained(self).toOpaque())
    )
  }

  private func disposeMPV() {
    mpv_render_context_set_update_callback(renderContext, nil, nil)
    mpv_render_context_free(renderContext)
  }

  public func resize(_ size: CGSize) {
    if size.width == 0 || size.height == 0 {
      return
    }

    NSLog("TextureSW: resize: \(size.width)x\(size.height)")
    createPixelBuffer(size)
  }

  private func createPixelBuffer(_ size: CGSize) {
    disposePixelBuffer()

    textureContexts.reinit(
      objects: [
        TextureSWContext(size: size),
        TextureSWContext(size: size),
        TextureSWContext(size: size),
      ],
      skipCheckArgs: true
    )
  }

  private func disposePixelBuffer() {
    textureContexts.reinit(objects: [], skipCheckArgs: true)
  }

  public func render(_ size: CGSize) {
    guard let textureContext = textureContexts.nextAvailable() else { return }
    let pixelBuffer = textureContext.pixelBuffer

    do {
      CVPixelBufferLockBaseAddress(pixelBuffer, [])
      defer { CVPixelBufferUnlockBaseAddress(pixelBuffer, []) }

      var dimensions = [Int32(size.width), Int32(size.height)]
      var stride = CVPixelBufferGetBytesPerRow(pixelBuffer)
      let pixels = CVPixelBufferGetBaseAddress(pixelBuffer)
      dimensions.withUnsafeMutableBufferPointer { dimensions in
        withUnsafeMutablePointer(to: &stride) { stride in
          "bgr0".withCString { format in
            var params = [
              mpv_render_param(type: MPV_RENDER_PARAM_SW_SIZE, data: dimensions.baseAddress),
              mpv_render_param(type: MPV_RENDER_PARAM_SW_FORMAT, data: UnsafeMutableRawPointer(mutating: format)),
              mpv_render_param(type: MPV_RENDER_PARAM_SW_STRIDE, data: stride),
              mpv_render_param(type: MPV_RENDER_PARAM_SW_POINTER, data: pixels),
              mpv_render_param(type: MPV_RENDER_PARAM_INVALID, data: nil),
            ]
            mpv_render_context_render(renderContext, &params)
          }
        }
      }
    }

    textureContexts.pushAsReady(textureContext)
  }
}
