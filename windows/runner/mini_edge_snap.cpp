#include "mini_edge_snap.h"

#include <algorithm>
#include <cmath>
#include <utility>

namespace mini_edge_snap {
namespace {

constexpr double kThresholdDip = 14;
constexpr double kStripDip = 2;
constexpr double kRevealMarginDip = 12;
constexpr double kHotZoneDip = 6;

LONG Round(double value) {
  return static_cast<LONG>(std::lround(value));
}

RECT WindowFrame(HWND window) {
  RECT frame{};
  GetWindowRect(window, &frame);
  return frame;
}

MONITORINFO MonitorAreas(HWND window) {
  MONITORINFO info{};
  info.cbSize = sizeof(info);
  if (!GetMonitorInfo(MonitorFromWindow(window, MONITOR_DEFAULTTONEAREST),
                      &info)) {
    info.rcMonitor = {0, 0, GetSystemMetrics(SM_CXSCREEN),
                      GetSystemMetrics(SM_CYSCREEN)};
    info.rcWork = info.rcMonitor;
  }
  return info;
}

void MoveWindowTo(HWND window, const RECT& target) {
  SetWindowPos(window, nullptr, target.left, target.top, 0, 0,
               SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE);
}

bool OverlapsOtherMonitor(HWND window, const RECT& rect) {
  struct Search {
    HMONITOR own;
    bool found;
  } search{MonitorFromWindow(window, MONITOR_DEFAULTTONEAREST), false};
  EnumDisplayMonitors(
      nullptr, &rect,
      [](HMONITOR monitor, HDC, LPRECT, LPARAM data) -> BOOL {
        auto* search = reinterpret_cast<Search*>(data);
        if (monitor == search->own) return TRUE;
        search->found = true;
        return FALSE;
      },
      reinterpret_cast<LPARAM>(&search));
  return search.found;
}

}  // namespace

Edge DetectEdge(const RECT& frame, const RECT& work, LONG threshold) {
  const LONG work_width = work.right - work.left;
  const LONG work_height = work.bottom - work.top;
  if (work_width <= 0 || work_height <= 0) return Edge::none;
  const LONG window_width = frame.right - frame.left;
  const LONG window_height = frame.bottom - frame.top;
  const LONG x_limit = std::min(threshold, work_width / 3);
  const LONG y_limit = std::min(threshold, work_height / 3);

  if (window_width <= work_width &&
      frame.bottom > work.top && frame.top < work.bottom) {
    if (frame.left - work.left <= x_limit) return Edge::left;
    if (work.right - frame.right <= x_limit) return Edge::right;
  }
  if (window_height <= work_height &&
      frame.right > work.left && frame.left < work.right) {
    if (frame.top - work.top <= y_limit) return Edge::top;
    if (work.bottom - frame.bottom <= y_limit) return Edge::bottom;
  }
  return Edge::none;
}

RECT HiddenRect(const RECT& frame, const RECT& screen, Edge edge, LONG strip) {
  const LONG width = frame.right - frame.left;
  const LONG height = frame.bottom - frame.top;
  const LONG safe_strip = std::clamp(strip, LONG{1}, std::max(LONG{1}, std::min(width, height) / 2));
  RECT target = frame;
  switch (edge) {
    case Edge::left:
      target.right = screen.left + safe_strip;
      target.left = target.right - width;
      break;
    case Edge::right:
      target.left = screen.right - safe_strip;
      target.right = target.left + width;
      break;
    case Edge::top:
      target.bottom = screen.top + safe_strip;
      target.top = target.bottom - height;
      break;
    case Edge::bottom:
      target.top = screen.bottom - safe_strip;
      target.bottom = target.top + height;
      break;
    case Edge::none:
      break;
  }
  return target;
}

RECT Intersect(const RECT& a, const RECT& b) {
  return {std::max(a.left, b.left), std::max(a.top, b.top),
          std::min(a.right, b.right), std::min(a.bottom, b.bottom)};
}

RECT Enlarged(const RECT& value, LONG margin) {
  return {value.left - margin, value.top - margin, value.right + margin,
          value.bottom + margin};
}

RECT ExtendedToEdge(const RECT& rect, const RECT& screen, Edge edge) {
  RECT result = rect;
  switch (edge) {
    case Edge::left:
      result.left = std::min(result.left, screen.left);
      break;
    case Edge::right:
      result.right = std::max(result.right, screen.right);
      break;
    case Edge::top:
      result.top = std::min(result.top, screen.top);
      break;
    case Edge::bottom:
      result.bottom = std::max(result.bottom, screen.bottom);
      break;
    case Edge::none:
      break;
  }
  return result;
}

RECT HotRect(const RECT& frame, const RECT& screen, LONG hot_zone) {
  const RECT visible = Intersect(frame, screen);
  return Intersect(Enlarged(visible, hot_zone), screen);
}

bool Contains(const RECT& rect, const POINT& point) {
  return point.x >= rect.left && point.x < rect.right &&
         point.y >= rect.top && point.y < rect.bottom;
}

RECT ClampInside(const RECT& frame, const RECT& work) {
  RECT result = frame;
  const LONG width = result.right - result.left;
  const LONG height = result.bottom - result.top;
  if (width >= work.right - work.left) {
    result.left = work.left;
    result.right = work.right;
  } else {
    result.left = std::clamp(result.left, work.left, work.right - width);
    result.right = result.left + width;
  }
  if (height >= work.bottom - work.top) {
    result.top = work.top;
    result.bottom = work.bottom;
  } else {
    result.top = std::clamp(result.top, work.top, work.bottom - height);
    result.bottom = result.top + height;
  }
  return result;
}

RECT SnappedRect(const RECT& frame, const RECT& work, Edge edge) {
  const LONG width = frame.right - frame.left;
  const LONG height = frame.bottom - frame.top;
  RECT result = ClampInside(frame, work);
  switch (edge) {
    case Edge::left:
      result.left = work.left;
      result.right = work.left + width;
      break;
    case Edge::right:
      result.right = work.right;
      result.left = work.right - width;
      break;
    case Edge::top:
      result.top = work.top;
      result.bottom = work.top + height;
      break;
    case Edge::bottom:
      result.bottom = work.bottom;
      result.top = work.bottom - height;
      break;
    case Edge::none:
      break;
  }
  return result;
}

Options Controller::options() const {
  return {Round(kThresholdDip * scale_), Round(kStripDip * scale_),
          Round(kRevealMarginDip * scale_), Round(kHotZoneDip * scale_)};
}

Edge Controller::HideableEdge(const RECT& frame,
                              const MONITORINFO& monitor) const {
  const Edge edge = DetectEdge(frame, monitor.rcWork, options().threshold);
  if (edge == Edge::none) return edge;
  const RECT hidden = HiddenRect(SnappedRect(frame, monitor.rcWork, edge),
                                 monitor.rcMonitor, edge, options().strip);
  return OverlapsOtherMonitor(window_, hidden) ? Edge::none : edge;
}

void Controller::Init(HWND window,
                      std::function<void(bool)> on_hidden_changed) {
  window_ = window;
  on_hidden_changed_ = std::move(on_hidden_changed);
}

void Controller::SetHidden(bool hidden) {
  if (hidden_ == hidden) return;
  hidden_ = hidden;
  if (on_hidden_changed_) on_hidden_changed_(hidden);
}

void Controller::SetScale(double scale) {
  if (scale > 0) scale_ = scale;
}

void Controller::SetEnabled(bool enabled) {
  if (enabled_ == enabled) return;
  enabled_ = enabled;
  SyncActive();
}

void Controller::SetMiniActive(bool active) {
  if (mini_active_ == active) return;
  mini_active_ = active;
  SyncActive();
}

void Controller::SetDragActive(bool active) {
  dragging_ = active;
}

void Controller::SyncActive() {
  if (enabled_ && mini_active_ && window_) return;
  Reveal();
  StopTimer();
  SetHidden(false);
  revealed_ = false;
  edge_ = Edge::none;
}

void Controller::StartTimer() {
  if (window_) SetTimer(window_, kTimerId, 90, nullptr);
}

void Controller::StopTimer() {
  if (window_) KillTimer(window_, kTimerId);
}

void Controller::Reveal() {
  if (!window_) return;
  if (hidden_) MoveWindowTo(window_, restore_);
  SetHidden(false);
  revealed_ = false;
  edge_ = Edge::none;
}

void Controller::Hide(Edge edge, const RECT& frame) {
  if (!window_ || edge == Edge::none) return;
  const MONITORINFO monitor = MonitorAreas(window_);
  restore_ = SnappedRect(frame, monitor.rcWork, edge);
  const RECT target =
      HiddenRect(restore_, monitor.rcMonitor, edge, options().strip);
  MoveWindowTo(window_, target);
  SetHidden(true);
  revealed_ = false;
  edge_ = edge;
  StartTimer();
}

void Controller::OnDragFinished() {
  dragging_ = false;
  if (!enabled_ || !mini_active_ || !window_) return;
  const RECT frame = WindowFrame(window_);
  const MONITORINFO monitor = MonitorAreas(window_);
  const RECT& work = monitor.rcWork;
  const Edge edge = HideableEdge(frame, monitor);
  if (edge == Edge::none) {
    if (hidden_) {
      Reveal();
    } else {
      restore_ = ClampInside(frame, work);
      revealed_ = false;
      edge_ = Edge::none;
    }
    StopTimer();
    return;
  }
  if (hidden_ && edge_ == edge) return;
  Hide(edge, frame);
}

void Controller::OnTimerTick() {
  if (!enabled_ || !mini_active_ || !window_ || dragging_) return;
  POINT cursor{};
  if (!GetCursorPos(&cursor)) return;
  const RECT frame = WindowFrame(window_);
  const MONITORINFO monitor = MonitorAreas(window_);
  if (hidden_) {
    if (Contains(HotRect(frame, monitor.rcMonitor, options().hot_zone),
                 cursor)) {
      MoveWindowTo(window_, restore_);
      SetHidden(false);
      revealed_ = true;
    }
    return;
  }
  if (!revealed_) return;
  const Edge edge =
      edge_ != Edge::none ? edge_ : HideableEdge(frame, monitor);
  const RECT keep = ExtendedToEdge(Enlarged(frame, options().reveal_margin),
                                   monitor.rcMonitor, edge);
  if (!Contains(keep, cursor)) Hide(edge, frame);
}

void Controller::Reset() {
  Reveal();
  StopTimer();
  SetHidden(false);
  revealed_ = false;
  dragging_ = false;
  edge_ = Edge::none;
  restore_ = {};
}

}  // namespace mini_edge_snap
