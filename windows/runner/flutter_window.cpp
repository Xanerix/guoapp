#include "flutter_window.h"

#include <commctrl.h>
#include <dwmapi.h>

#include <algorithm>
#include <cmath>
#include <optional>
#include <string>
#include <flutter/standard_method_codec.h>

#include "flutter/generated_plugin_registrant.h"

#include "mini_edge_snap.h"

namespace {

constexpr double kMiniExtent = 480;
constexpr double kMiniMinExtent = 200;
constexpr double kMiniMargin = 24;
constexpr int kBossKeyId = 1;

std::optional<int> IntArgument(const flutter::EncodableMap& args,
                               const char* key) {
  const auto found = args.find(flutter::EncodableValue(key));
  if (found == args.end()) return std::nullopt;
  if (const auto* value = std::get_if<int32_t>(&found->second)) return *value;
  if (const auto* value = std::get_if<int64_t>(&found->second)) {
    return static_cast<int>(*value);
  }
  return std::nullopt;
}

bool BoolArgument(const flutter::EncodableMap& args, const char* key) {
  const auto found = args.find(flutter::EncodableValue(key));
  if (found == args.end()) return false;
  const auto* value = std::get_if<bool>(&found->second);
  return value && *value;
}

UINT BossKeyCode(const std::string& key) {
  if (key.size() == 1 && ((key[0] >= 'A' && key[0] <= 'Z') ||
                          (key[0] >= '0' && key[0] <= '9'))) {
    return key[0];
  }
  if (key.size() >= 2 && key.size() <= 3 && key[0] == 'F') {
    const int number = std::atoi(key.c_str() + 1);
    if (number >= 1 && number <= 12) return VK_F1 + number - 1;
  }
  return 0;
}

SIZE MiniSize(double aspect, double extent) {
  if (aspect >= 1) {
    return {std::lround(extent), std::lround(extent / aspect)};
  }
  return {std::lround(extent * aspect), std::lround(extent)};
}

double WindowScale(HWND window) {
  return FlutterDesktopGetDpiForHWND(window) / 96.0;
}

LRESULT CALLBACK DeclineAccessibilityQueries(HWND window, UINT message,
                                             WPARAM wparam, LPARAM lparam,
                                             UINT_PTR, DWORD_PTR) {
  if (message == WM_GETOBJECT) {
    return DefWindowProc(window, message, wparam, lparam);
  }
  if (message == WM_NCDESTROY) {
    RemoveWindowSubclass(window, DeclineAccessibilityQueries, 0);
  }
  return DefSubclassProc(window, message, wparam, lparam);
}

}  // namespace

FlutterWindow::FlutterWindow(const flutter::DartProject& project)
    : project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) {
    return false;
  }

  RECT frame = GetClientArea();

  // The size here must match the window dimensions to avoid unnecessary surface
  // creation / destruction in the startup path.
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  // Ensure that basic setup of the controller was successful.
  if (!flutter_controller_->engine() || !flutter_controller_->view()) {
    return false;
  }
  RegisterPlugins(flutter_controller_->engine());
  media_controls_ = std::make_unique<MediaControls>(
      GetHandle(), flutter_controller_->engine()->messenger());
  device_channel_ =
      std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
          flutter_controller_->engine()->messenger(), "duanju/device",
          &flutter::StandardMethodCodec::GetInstance());
  device_channel_->SetMethodCallHandler(
      [this](const flutter::MethodCall<flutter::EncodableValue>& call,
             std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result) {
        if (call.method_name() == "setFullScreen") {
          const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
          const bool* enabled = nullptr;
          if (args) {
            const auto found = args->find(flutter::EncodableValue("enabled"));
            if (found != args->end()) enabled = std::get_if<bool>(&found->second);
          }
          if (!enabled) {
            result->Error("bad_args", "enabled is required");
            return;
          }
          SetFullScreen(*enabled);
          result->Success();
          return;
        }
        if (call.method_name() == "setMiniWindow") {
          const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
          const bool* enabled = nullptr;
          const double* aspect = nullptr;
          if (args) {
            const auto found = args->find(flutter::EncodableValue("enabled"));
            if (found != args->end()) enabled = std::get_if<bool>(&found->second);
            const auto ratio = args->find(flutter::EncodableValue("aspect"));
            if (ratio != args->end()) aspect = std::get_if<double>(&ratio->second);
          }
          if (!enabled || (*enabled && (!aspect || !(*aspect > 0)))) {
            result->Error("bad_args", "enabled and aspect are required");
            return;
          }
          if (!*enabled) {
            result->Success(ExitMiniWindow());
            return;
          }
          EnterMiniWindow(*aspect, *args);
          result->Success();
          return;
        }
        if (call.method_name() == "windowFrame") {
          result->Success(WindowFrame());
          return;
        }
        if (call.method_name() == "setWindowFrame") {
          const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
          if (!args || !SetWindowFrame(*args)) {
            result->Error("bad_args", "left, top, width and height are required");
            return;
          }
          result->Success();
          return;
        }
        if (call.method_name() == "setBossKey") {
          result->Success(flutter::EncodableValue(SetBossKey(
              std::get_if<flutter::EncodableMap>(call.arguments()))));
          return;
        }
        if (call.method_name() == "setMiniEdgeSnap") {
          const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
          edge_snap_.SetEnabled(args && BoolArgument(*args, "enabled"));
          result->Success();
          return;
        }
        if (call.method_name() != "playbackPower") {
          result->NotImplemented();
          return;
        }
        SYSTEM_POWER_STATUS power{};
        const bool known = GetSystemPowerStatus(&power) != 0;
        MEMORYSTATUSEX memory{};
        memory.dwLength = sizeof(memory);
        const bool memory_known = GlobalMemoryStatusEx(&memory) != 0;
        flutter::EncodableMap values;
        values[flutter::EncodableValue("batterySaver")] =
            flutter::EncodableValue(known && power.SystemStatusFlag == 1);
        values[flutter::EncodableValue("onBattery")] =
            flutter::EncodableValue(!known || power.ACLineStatus != 1);
        values[flutter::EncodableValue("lowMemory")] =
            flutter::EncodableValue(!memory_known || memory.ullTotalPhys < 4ULL * 1024 * 1024 * 1024);
        result->Success(flutter::EncodableValue(values));
      });
  if (FAILED(CoCreateInstance(CLSID_TaskbarList, nullptr, CLSCTX_INPROC_SERVER,
                              IID_PPV_ARGS(&taskbar_))) ||
      FAILED(taskbar_->HrInit())) {
    taskbar_.Reset();
  }
  edge_snap_.Init(GetHandle(), [this](bool hidden) {
    if (device_channel_) {
      device_channel_->InvokeMethod(
          "miniEdgeHidden", std::make_unique<flutter::EncodableValue>(hidden));
    }
  });
  SetChildContent(flutter_controller_->view()->GetNativeWindow());
  SetWindowSubclass(flutter_controller_->view()->GetNativeWindow(),
                    DeclineAccessibilityQueries, 0, 0);

  flutter_controller_->engine()->SetNextFrameCallback([&]() {
    ShowWindow(GetHandle(), restore_maximized_ ? SW_SHOWMAXIMIZED : SW_SHOWNORMAL);
  });

  // Flutter can complete the first frame before the "show window" callback is
  // registered. The following call ensures a frame is pending to ensure the
  // window is shown. It is a no-op if the first frame hasn't completed yet.
  flutter_controller_->ForceRedraw();

  return true;
}

void FlutterWindow::SetFullScreen(bool enabled) {
  if (enabled == fullscreen_) {
    return;
  }
  HWND window = GetHandle();
  if (enabled) {
    MONITORINFO monitor{};
    monitor.cbSize = sizeof(monitor);
    if (!GetMonitorInfo(MonitorFromWindow(window, MONITOR_DEFAULTTONEAREST),
                        &monitor)) {
      return;
    }
    placement_before_fullscreen_.length = sizeof(WINDOWPLACEMENT);
    GetWindowPlacement(window, &placement_before_fullscreen_);
    SetWindowLong(window, GWL_STYLE,
                  GetWindowLong(window, GWL_STYLE) & ~WS_MAXIMIZE);
    fullscreen_ = true;
    const RECT target = monitor.rcMonitor;
    SetWindowPos(window, nullptr, target.left, target.top,
                 target.right - target.left, target.bottom - target.top,
                 SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
  } else {
    fullscreen_ = false;
    SetWindowPos(window, nullptr, 0, 0, 0, 0,
                 SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE |
                     SWP_FRAMECHANGED);
    BOOL transitions_disabled = TRUE;
    DwmSetWindowAttribute(window, DWMWA_TRANSITIONS_FORCEDISABLED,
                          &transitions_disabled, sizeof(transitions_disabled));
    SetWindowPlacement(window, &placement_before_fullscreen_);
    transitions_disabled = FALSE;
    DwmSetWindowAttribute(window, DWMWA_TRANSITIONS_FORCEDISABLED,
                          &transitions_disabled, sizeof(transitions_disabled));
  }
  if (taskbar_) {
    taskbar_->MarkFullscreenWindow(window, enabled);
  }
}

void FlutterWindow::EnterMiniWindow(double aspect,
                                    const flutter::EncodableMap& args) {
  HWND window = GetHandle();
  const double scale = WindowScale(window);
  mini_aspect_ = aspect;
  edge_snap_.SetScale(scale);
  if (mini_) {
    edge_snap_.Reset();
    RECT frame;
    GetWindowRect(window, &frame);
    const SIZE size = MiniSize(
        aspect, std::max(frame.right - frame.left, frame.bottom - frame.top));
    SetWindowPos(window, nullptr, 0, 0, size.cx, size.cy,
                 SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE);
    return;
  }
  fullscreen_before_mini_ = fullscreen_;
  if (fullscreen_) {
    SetFullScreen(false);
  }
  placement_before_mini_.length = sizeof(WINDOWPLACEMENT);
  GetWindowPlacement(window, &placement_before_mini_);
  if (IsZoomed(window)) {
    ShowWindow(window, SW_RESTORE);
  }
  const auto left = IntArgument(args, "left");
  const auto top = IntArgument(args, "top");
  const auto extent = IntArgument(args, "extent");
  RECT target{};
  bool placed = false;
  if (left && top && extent && *extent >= kMiniMinExtent * scale) {
    const SIZE size = MiniSize(aspect, *extent);
    target = {*left, *top, *left + size.cx, *top + size.cy};
    placed = MonitorFromRect(&target, MONITOR_DEFAULTTONULL) != nullptr;
  }
  if (!placed) {
    MONITORINFO monitor{};
    monitor.cbSize = sizeof(monitor);
    GetMonitorInfo(MonitorFromWindow(window, MONITOR_DEFAULTTONEAREST),
                   &monitor);
    const SIZE size = MiniSize(aspect, kMiniExtent * scale);
    const LONG margin = std::lround(kMiniMargin * scale);
    target.right = monitor.rcWork.right - margin;
    target.bottom = monitor.rcWork.bottom - margin;
    target.left = target.right - size.cx;
    target.top = target.bottom - size.cy;
  }
  style_before_mini_ = GetWindowLong(window, GWL_STYLE);
  SetWindowLong(window, GWL_STYLE, style_before_mini_ & ~WS_CAPTION);
  mini_ = true;
  edge_snap_.Reset();
  SetWindowPos(window, HWND_TOPMOST, target.left, target.top,
               target.right - target.left, target.bottom - target.top,
               SWP_FRAMECHANGED);
  edge_snap_.SetMiniActive(true);
}

flutter::EncodableValue FlutterWindow::ExitMiniWindow() {
  if (!mini_) {
    return flutter::EncodableValue();
  }
  HWND window = GetHandle();
  edge_snap_.SetMiniActive(false);
  edge_snap_.Reset();
  RECT frame;
  GetWindowRect(window, &frame);
  mini_ = false;
  SetWindowLong(window, GWL_STYLE, style_before_mini_);
  SetWindowPos(window, HWND_NOTOPMOST, 0, 0, 0, 0,
               SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_FRAMECHANGED);
  SetWindowPlacement(window, &placement_before_mini_);
  if (fullscreen_before_mini_) {
    SetFullScreen(true);
  }
  flutter::EncodableMap values;
  values[flutter::EncodableValue("left")] =
      flutter::EncodableValue(static_cast<int32_t>(frame.left));
  values[flutter::EncodableValue("top")] =
      flutter::EncodableValue(static_cast<int32_t>(frame.top));
  values[flutter::EncodableValue("extent")] = flutter::EncodableValue(
      static_cast<int32_t>(std::max(frame.right - frame.left,
                                    frame.bottom - frame.top)));
  return flutter::EncodableValue(values);
}

flutter::EncodableValue FlutterWindow::WindowFrame() {
  HWND window = GetHandle();
  WINDOWPLACEMENT placement{};
  placement.length = sizeof(placement);
  if (mini_) {
    placement = placement_before_mini_;
  } else if (fullscreen_) {
    placement = placement_before_fullscreen_;
  } else if (!GetWindowPlacement(window, &placement)) {
    return flutter::EncodableValue();
  }
  const bool minimized = placement.showCmd == SW_SHOWMINIMIZED;
  const bool maximized = placement.showCmd == SW_SHOWMAXIMIZED ||
      (minimized && (placement.flags & WPF_RESTORETOMAXIMIZED));
  RECT frame;
  if (mini_ || fullscreen_ || maximized || minimized) {
    frame = placement.rcNormalPosition;
    MONITORINFO monitor{};
    monitor.cbSize = sizeof(monitor);
    if (GetMonitorInfo(MonitorFromRect(&frame, MONITOR_DEFAULTTONEAREST),
                       &monitor)) {
      OffsetRect(&frame, monitor.rcWork.left - monitor.rcMonitor.left,
                 monitor.rcWork.top - monitor.rcMonitor.top);
    }
  } else if (!GetWindowRect(window, &frame)) {
    return flutter::EncodableValue();
  }
  flutter::EncodableMap values;
  values[flutter::EncodableValue("maximized")] =
      flutter::EncodableValue(static_cast<int32_t>(maximized));
  values[flutter::EncodableValue("left")] =
      flutter::EncodableValue(static_cast<int32_t>(frame.left));
  values[flutter::EncodableValue("top")] =
      flutter::EncodableValue(static_cast<int32_t>(frame.top));
  values[flutter::EncodableValue("width")] =
      flutter::EncodableValue(static_cast<int32_t>(frame.right - frame.left));
  values[flutter::EncodableValue("height")] =
      flutter::EncodableValue(static_cast<int32_t>(frame.bottom - frame.top));
  return flutter::EncodableValue(values);
}

bool FlutterWindow::SetWindowFrame(const flutter::EncodableMap& args) {
  const auto left = IntArgument(args, "left");
  const auto top = IntArgument(args, "top");
  const auto width = IntArgument(args, "width");
  const auto height = IntArgument(args, "height");
  if (!left || !top || !width || !height || *width <= 0 || *height <= 0) {
    return false;
  }
  HWND window = GetHandle();
  const int title = static_cast<int>(std::lround(40 * WindowScale(window)));
  RECT strip{*left, *top, *left + *width, *top + std::min(*height, title)};
  if (!MonitorFromRect(&strip, MONITOR_DEFAULTTONULL)) {
    restore_maximized_ = false;
    return true;
  }
  restore_maximized_ = BoolArgument(args, "maximized");
  for (int pass = 0; pass < 2; ++pass) {
    SetWindowPos(window, nullptr, *left, *top, *width, *height,
                 SWP_NOZORDER | SWP_NOACTIVATE);
  }
  if (restore_maximized_ && IsWindowVisible(window)) {
    ShowWindow(window, SW_SHOWMAXIMIZED);
  }
  return true;
}

bool FlutterWindow::SetBossKey(const flutter::EncodableMap* args) {
  HWND window = GetHandle();
  UnregisterHotKey(window, kBossKeyId);
  if (!args) return true;
  const auto found = args->find(flutter::EncodableValue("key"));
  const auto* key =
      found == args->end() ? nullptr : std::get_if<std::string>(&found->second);
  const UINT code = key ? BossKeyCode(*key) : 0;
  if (!code) return false;
  UINT modifiers = MOD_NOREPEAT;
  if (BoolArgument(*args, "ctrl")) modifiers |= MOD_CONTROL;
  if (BoolArgument(*args, "alt")) modifiers |= MOD_ALT;
  if (BoolArgument(*args, "shift")) modifiers |= MOD_SHIFT;
  if (BoolArgument(*args, "meta")) modifiers |= MOD_WIN;
  return RegisterHotKey(window, kBossKeyId, modifiers, code) != 0;
}

void FlutterWindow::ToggleHidden() {
  HWND window = GetHandle();
  if (IsWindowVisible(window)) {
    if (device_channel_) device_channel_->InvokeMethod("bossKeyHidden", nullptr);
    ShowWindow(window, SW_HIDE);
    return;
  }
  ShowWindow(window, IsIconic(window) ? SW_RESTORE : SW_SHOW);
  SetForegroundWindow(window);
}

void FlutterWindow::KeepMiniAspect(WPARAM edge, RECT* frame) {
  double width = frame->right - frame->left;
  double height = frame->bottom - frame->top;
  if (edge == WMSZ_TOP || edge == WMSZ_BOTTOM) {
    width = height * mini_aspect_;
  } else {
    height = width / mini_aspect_;
  }
  const double minimum = kMiniMinExtent * WindowScale(GetHandle());
  const double extent = std::max(width, height);
  if (extent < minimum) {
    width *= minimum / extent;
    height *= minimum / extent;
  }
  if (edge == WMSZ_LEFT || edge == WMSZ_TOPLEFT || edge == WMSZ_BOTTOMLEFT) {
    frame->left = frame->right - std::lround(width);
  } else {
    frame->right = frame->left + std::lround(width);
  }
  if (edge == WMSZ_TOP || edge == WMSZ_TOPLEFT || edge == WMSZ_TOPRIGHT) {
    frame->top = frame->bottom - std::lround(height);
  } else {
    frame->bottom = frame->top + std::lround(height);
  }
}

void FlutterWindow::OnDestroy() {
  HMODULE ffmpeg_runtime = nullptr;
  GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_PIN, L"libffmpegkit.dll",
                     &ffmpeg_runtime);
  UnregisterHotKey(GetHandle(), kBossKeyId);
  media_controls_.reset();
  device_channel_.reset();
  taskbar_.Reset();
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
  if (message == WM_ENTERSIZEMOVE) {
    edge_snap_.SetDragActive(true);
  } else if (message == WM_MOVING) {
    edge_snap_.OnMoving(reinterpret_cast<RECT*>(lparam));
  } else if (message == WM_EXITSIZEMOVE) {
    edge_snap_.SetScale(WindowScale(hwnd));
    edge_snap_.OnDragFinished();
  }

  // Give Flutter, including plugins, an opportunity to handle window messages.
  if (flutter_controller_) {
    std::optional<LRESULT> result =
        flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam,
                                                      lparam);
    if (result) {
      return *result;
    }
  }

  switch (message) {
    case WM_NCCALCSIZE:
      if ((fullscreen_ || mini_) && wparam) {
        return 0;
      }
      break;
    case WM_NCACTIVATE:
      if (mini_) {
        return DefWindowProc(hwnd, message, wparam, -1);
      }
      break;
    case WM_SIZING:
      if (mini_) {
        KeepMiniAspect(wparam, reinterpret_cast<RECT*>(lparam));
        return TRUE;
      }
      break;
    case WM_SYSCOMMAND:
      if (mini_ && (wparam & 0xFFF0) == SC_MOVE) {
        const LONG style = GetWindowLong(hwnd, GWL_STYLE);
        SetWindowLong(hwnd, GWL_STYLE,
                      style & ~(WS_THICKFRAME | WS_MAXIMIZEBOX));
        const LRESULT moved = DefWindowProc(hwnd, message, wparam, lparam);
        SetWindowLong(hwnd, GWL_STYLE, style);
        return moved;
      }
      break;
    case WM_DPICHANGED:
      edge_snap_.SetScale(WindowScale(hwnd));
      break;
    case WM_TIMER:
      if (wparam == mini_edge_snap::kTimerId) {
        edge_snap_.OnTimerTick();
        return 0;
      }
      break;
    case WM_HOTKEY:
      if (wparam == kBossKeyId) {
        ToggleHidden();
        return 0;
      }
      break;
    case WM_FONTCHANGE:
      flutter_controller_->engine()->ReloadSystemFonts();
      break;
    case MediaControls::kCommandMessage:
      if (media_controls_) media_controls_->HandleCommand(wparam, lparam);
      return 0;
  }

  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
