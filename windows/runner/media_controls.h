#ifndef RUNNER_MEDIA_CONTROLS_H_
#define RUNNER_MEDIA_CONTROLS_H_

#include <windows.h>

#include <flutter/binary_messenger.h>
#include <flutter/encodable_value.h>
#include <flutter/method_channel.h>
#include <winrt/Windows.Media.h>

#include <memory>
#include <string>

class MediaControls {
 public:
  static constexpr UINT kCommandMessage = WM_APP + 0x47;

  MediaControls(HWND window, flutter::BinaryMessenger* messenger);
  ~MediaControls();

  void HandleCommand(WPARAM command, LPARAM position);

 private:
  void Update(const flutter::EncodableMap& args);
  void Clear();

  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> channel_;
  winrt::Windows::Media::SystemMediaTransportControls controls_{nullptr};
  winrt::event_token button_token_{};
  winrt::event_token position_token_{};
  std::string artwork_;
};

#endif  // RUNNER_MEDIA_CONTROLS_H_
