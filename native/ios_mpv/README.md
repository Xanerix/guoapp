# iOS VideoToolbox 原始帧

常规设备包通过 `python3 scripts/build_ios.py` 构建。需要 Xcode、Flutter、CocoaPods、
Go、Meson、Ninja、CMake 和 pkgconf。脚本使用固定版本 libmpv-darwin-build v0.6.0
构建 mpv 0.36.0，缓存位于 `native/build/ios/mpv/`。CocoaPods 在嵌入和签名前替换
设备版 Mpv.framework 的可执行文件。模拟器沿用上游框架。

iOS 的默认和硬解直通模式优先把 VideoToolbox 像素缓冲区交给 Flutter 纹理及
AVSampleBufferDisplayLayer。兼容模式继续使用软件纹理。设备缺少自定义后端或初始化
失败时，也会使用软件纹理。若解码器退回软件帧或像素格式不支持直接显示，自定义后端
将帧转换为缓冲池管理的 BGRA。

私有 API 名称为 `guoapp-ios-pixelbuffer`。渲染参数 1000 指向包含一个缓冲区指针
和 Double 时间戳的输出记录。mpv 交出一个 retain，Swift 接管并在下一帧释放上一帧
引用。Flutter 和画中画层均可分别持有缓冲区。画中画接收端通过 AVFoundation 的
背压控制提交频率。

需要给已有签名包单独替换 libmpv 时，可运行：

```sh
python3 native/ios_mpv/stage_test_app.py Runner.app libmpv.2.dylib GuoApp-Test.app --identity SIGNING_ID
```
