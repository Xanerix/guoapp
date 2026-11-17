#include "media_controls.h"

#include <flutter/standard_method_codec.h>
#include <systemmediatransportcontrolsinterop.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Storage.h>
#include <winrt/Windows.Storage.Streams.h>

#include <chrono>

namespace {

using winrt::Windows::Media::MediaPlaybackStatus;
using winrt::Windows::Media::MediaPlaybackType;
using winrt::Windows::Media::PlaybackPositionChangeRequestedEventArgs;
using winrt::Windows::Media::SystemMediaTransportControls;
using winrt::Windows::Media::SystemMediaTransportControlsButton;
using winrt::Windows::Media::SystemMediaTransportControlsButtonPressedEventArgs;
using winrt::Windows::Media::SystemMediaTransportControlsTimelineProperties;

enum Command : WPARAM { kPlay, kPause, kNext, kPrevious, kSeek };

const char* CommandName(WPARAM command) {
  switch (command) {
    case kPlay:
      return "play";
    case kPause:
      return "pause";
    case kNext:
      return "next";
    case kPrevious:
      return "previous";
    case kSeek:
      return "seek";
  }
  return nullptr;
}

const flutter::EncodableValue* Find(const flutter::EncodableMap& args,
                                    const char* key) {
  const auto found = args.find(flutter::EncodableValue(key));
  return found == args.end() ? nullptr : &found->second;
}

std::string StringArgument(const flutter::EncodableMap& args, const char* key) {
  const auto* value = Find(args, key);
  const auto* text = value ? std::get_if<std::string>(value) : nullptr;
  return text ? *text : std::string();
}

bool BoolArgument(const flutter::EncodableMap& args, const char* key) {
  const auto* value = Find(args, key);
  const auto* flag = value ? std::get_if<bool>(value) : nullptr;
  return flag && *flag;
}

double NumberArgument(const flutter::EncodableMap& args, const char* key) {
  const auto* value = Find(args, key);
  if (!value) return 0;
  if (const auto* number = std::get_if<double>(value)) return *number;
  if (const auto* number = std::get_if<int32_t>(value)) return *number;
  if (const auto* number = std::get_if<int64_t>(value)) {
    return static_cast<double>(*number);
  }
  return 0;
}

std::chrono::milliseconds Milliseconds(double value) {
  return std::chrono::milliseconds(static_cast<int64_t>(value > 0 ? value : 0));
}

}  // namespace

MediaControls::MediaControls(HWND window, flutter::BinaryMessenger* messenger)
    : channel_(std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
          messenger, "duanju/media",
          &flutter::StandardMethodCodec::GetInstance())) {
  try {
    auto interop = winrt::get_activation_factory<
        SystemMediaTransportControls, ISystemMediaTransportControlsInterop>();
    winrt::check_hresult(interop->GetForWindow(
        window, winrt::guid_of<SystemMediaTransportControls>(),
        winrt::put_abi(controls_)));
    button_token_ = controls_.ButtonPressed(
        [window](const SystemMediaTransportControls&,
                 const SystemMediaTransportControlsButtonPressedEventArgs& args) {
          WPARAM command;
          switch (args.Button()) {
            case SystemMediaTransportControlsButton::Play:
              command = kPlay;
              break;
            case SystemMediaTransportControlsButton::Pause:
            case SystemMediaTransportControlsButton::Stop:
              command = kPause;
              break;
            case SystemMediaTransportControlsButton::Next:
              command = kNext;
              break;
            case SystemMediaTransportControlsButton::Previous:
              command = kPrevious;
              break;
            default:
              return;
          }
          PostMessage(window, kCommandMessage, command, 0);
        });
    position_token_ = controls_.PlaybackPositionChangeRequested(
        [window](const SystemMediaTransportControls&,
                 const PlaybackPositionChangeRequestedEventArgs& args) {
          const auto position =
              std::chrono::duration_cast<std::chrono::milliseconds>(
                  args.RequestedPlaybackPosition())
                  .count();
          PostMessage(window, kCommandMessage, kSeek,
                      static_cast<LPARAM>(position));
        });
  } catch (const winrt::hresult_error&) {
    controls_ = nullptr;
  }
  channel_->SetMethodCallHandler(
      [this](const flutter::MethodCall<flutter::EncodableValue>& call,
             std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result) {
        if (call.method_name() == "update") {
          const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
          if (args) Update(*args);
          result->Success();
          return;
        }
        if (call.method_name() == "clear") {
          Clear();
          result->Success();
          return;
        }
        result->NotImplemented();
      });
}

MediaControls::~MediaControls() {
  channel_->SetMethodCallHandler(nullptr);
  if (!controls_) return;
  Clear();
  controls_.ButtonPressed(button_token_);
  controls_.PlaybackPositionChangeRequested(position_token_);
}

void MediaControls::HandleCommand(WPARAM command, LPARAM position) {
  const char* name = CommandName(command);
  if (!name) return;
  flutter::EncodableMap args{
      {flutter::EncodableValue("command"), flutter::EncodableValue(name)}};
  if (command == kSeek) {
    args[flutter::EncodableValue("positionMs")] =
        flutter::EncodableValue(static_cast<int64_t>(position));
  }
  channel_->InvokeMethod(
      "command", std::make_unique<flutter::EncodableValue>(std::move(args)));
}

void MediaControls::Update(const flutter::EncodableMap& args) {
  if (!controls_) return;
  try {
    const bool playing = BoolArgument(args, "playing");
    const double duration = NumberArgument(args, "durationMs");
    controls_.IsEnabled(true);
    controls_.IsPlayEnabled(true);
    controls_.IsPauseEnabled(true);
    controls_.IsStopEnabled(true);
    controls_.IsNextEnabled(BoolArgument(args, "hasNext"));
    controls_.IsPreviousEnabled(BoolArgument(args, "hasPrevious"));
    controls_.PlaybackStatus(playing ? MediaPlaybackStatus::Playing
                                     : MediaPlaybackStatus::Paused);
    const double rate = NumberArgument(args, "rate");
    controls_.PlaybackRate(rate > 0 ? rate : 1);

    auto updater = controls_.DisplayUpdater();
    updater.Type(MediaPlaybackType::Video);
    updater.VideoProperties().Title(
        winrt::to_hstring(StringArgument(args, "title")));
    updater.VideoProperties().Subtitle(
        winrt::to_hstring(StringArgument(args, "subtitle")));
    const std::string artwork = StringArgument(args, "artwork");
    if (artwork != artwork_) {
      artwork_ = artwork;
      updater.Thumbnail(nullptr);
      if (!artwork.empty()) {
        winrt::Windows::Storage::StorageFile::GetFileFromPathAsync(
            winrt::to_hstring(artwork))
            .Completed([updater](const auto& operation, auto status) {
              if (status != winrt::Windows::Foundation::AsyncStatus::Completed) {
                return;
              }
              try {
                updater.Thumbnail(
                    winrt::Windows::Storage::Streams::RandomAccessStreamReference::
                        CreateFromFile(operation.GetResults()));
                updater.Update();
              } catch (const winrt::hresult_error&) {
              }
            });
      }
    }
    updater.Update();

    SystemMediaTransportControlsTimelineProperties timeline;
    timeline.StartTime(std::chrono::milliseconds(0));
    timeline.MinSeekTime(std::chrono::milliseconds(0));
    timeline.EndTime(Milliseconds(duration));
    timeline.MaxSeekTime(Milliseconds(duration));
    timeline.Position(Milliseconds(NumberArgument(args, "positionMs")));
    controls_.UpdateTimelineProperties(timeline);
  } catch (const winrt::hresult_error&) {
  }
}

void MediaControls::Clear() {
  if (!controls_) return;
  try {
    artwork_.clear();
    controls_.PlaybackStatus(MediaPlaybackStatus::Closed);
    controls_.DisplayUpdater().ClearAll();
    controls_.DisplayUpdater().Update();
    controls_.IsEnabled(false);
  } catch (const winrt::hresult_error&) {
  }
}
