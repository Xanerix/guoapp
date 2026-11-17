// This file is a part of media_kit
// (https://github.com/media-kit/media-kit).
//
// Copyright © 2021 & onwards, Hitesh Kumar Saini <saini123hitesh@gmail.com>.
// All rights reserved.
// Use of this source code is governed by MIT license that can be found in the
// LICENSE file.

#ifndef VIDEO_OUTPUT_MANAGER_H_
#define VIDEO_OUTPUT_MANAGER_H_

#include <flutter/plugin_registrar_windows.h>

#include <unordered_map>

#include "thread_pool.h"
#include "video_output.h"

// Creates & disposes |VideoOutput| instances for video embedding.
//
// The methods in this class are thread-safe & run on separate worker thread so
// that they don't block Flutter's UI thread while platform channels are being
// invoked.
class VideoOutputManager {
 public:
  VideoOutputManager(flutter::PluginRegistrarWindows* registrar);

  // Creates a new |VideoOutput| instance. It's texture ID may be used to render
  // the video. The changes in it's texture ID & video dimensions will be
  // notified via the |texture_update_callback|.
  void Create(
      int64_t handle,
      VideoOutputConfiguration configuration,
      std::function<void(int64_t, int64_t, int64_t)> texture_update_callback);

  // Sets the required video output size.
  // This forces |VideoOutput| to resize the D3D11 render target / shared
  // texture (or the S/W pixel buffer).
  void SetSize(int64_t handle,
               std::optional<int64_t> width,
               std::optional<int64_t> height);

  // Destroys the |VideoOutput| with given handle.
  void Dispose(int64_t handle, std::function<void()> callback);

  ~VideoOutputManager();

 private:
  std::mutex mutex_ = std::mutex();
  // All the operations involving the D3D11 device / immediate context or
  // libmpv must be performed on the same single thread: mpv renders with the
  // device's immediate context and other threads must not use it at the same
  // time. Not doing so results in access violations & crashes.
  //
  // Technically, the correct place to do all the video rendering (& thus
  // resize) etc. is on Flutter's render thread itself (exposed as callback in
  // |flutter::GpuSurfaceTexture| & |flutter::PixelBufferTexture|). However,
  // this slows down the UI too much. So, a good idea seemed to have a separate
  // worker thread which queues all the rendering related jobs & performs them
  // orderly. |ThreadPool| is exactly that.
  //
  // Following operations are performed through the |ThreadPool|:
  //
  // * Rendering of video frame i.e. |mpv_render_context_render| with
  //   |MPV_RENDER_PARAM_D3D11_FBO|, after being notified by
  //   |mpv_render_context_set_update_callback|.
  // * Creation / Disposal of new |VideoOutput|.
  //     * For creation, |mpv_render_context_create| & instantiation of a new
  //       |D3D11SurfaceManager| is done through |ThreadPool| (in |VideoOutput|
  //       constructor).
  //     * For disposal, |ThreadPool| ensures that all the pending |Render| or
  //       |Resize| tasks are completed before freeing the |D3D11SurfaceManager|
  //       & |mpv_render_context| etc.
  // * Resizing |D3D11SurfaceManager|'s render target & shared texture, and
  //   re-registering Flutter textures (|GpuSurfaceTexture| /
  //   |PixelBufferTexture|).
  std::unique_ptr<ThreadPool> thread_pool_ = std::make_unique<ThreadPool>(1);
  std::unique_ptr<ThreadPool> operations_ = std::make_unique<ThreadPool>(1);
  flutter::PluginRegistrarWindows* registrar_ = nullptr;
  std::unordered_map<int64_t, std::unique_ptr<VideoOutput>> video_outputs_ = {};
};

#endif  // VIDEO_OUTPUT_MANAGER_H_
