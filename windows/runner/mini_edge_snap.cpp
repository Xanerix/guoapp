#include "mini_edge_snap.h"

#include <algorithm>
#include <cmath>

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

RECT MonitorWorkArea(HWND window) {
  MONITORINFO info{};
  info.cbSize = sizeof(info);
  if (GetMonitorInfo(MonitorFromWindow(window, MONITOR_DEFAULTTONEAREST),
                     &info)) {
    return info.rcWork;
  }
  return {0, 0, GetSystemMetrics(SM_CXSCREEN), GetSystemMetrics(SM_CYSCREEN)};
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

RECT HiddenRect(const RECT& frame, const RECT& work, Edge edge, LONG strip) {
  const LONG width = frame.right - frame.left;
  const LONG height = frame.bottom - frame.top;
  const LONG safe_strip = std::clamp(strip, LONG{1}, std::max(LONG{1}, std::min(width, height) / 2));
  RECT target = frame;
  switch (edge) {
    case Edge::left:
      target.right = work.left + safe_strip;
      target.left = target.right - width;
      break;
    case Edge::right:
      target.left = work.right - safe_strip;
      target.right = target.left + width;
      break;
    case Edge::top:
      target.bottom = work.top + safe_strip;
      target.top = target.bottom - height;
      break;
    case Edge::bottom:
      target.top = work.bottom - safe_strip;
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

RECT HotRect(const RECT& frame, const RECT& work, LONG hot_zone) {
  const RECT visible = Intersect(frame, work);
  return Intersect(Enlarged(visible, hot_zone), work);
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

Edge Controller::HideableEdge(const RECT& frame, const RECT& work) const {
  const Edge edge = DetectEdge(frame, work, options().threshold);
  if (edge == Edge::none) return edge;
  const RECT hidden =
      HiddenRect(SnappedRect(frame, work, edge), work, edge, options().strip);
  return OverlapsOtherMonitor(window_, hidden) ? Edge::none : edge;
}

void Controller::Init(HWND window) {
  window_ = window;
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
  hidden_ = false;
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
  hidden_ = false;
  revealed_ = false;
  edge_ = Edge::none;
}

void Controller::Hide(Edge edge, const RECT& frame) {
  if (!window_ || edge == Edge::none) return;
  const RECT work = MonitorWorkArea(window_);
  restore_ = SnappedRect(frame, work, edge);
  const RECT target = HiddenRect(restore_, work, edge, options().strip);
  MoveWindowTo(window_, target);
  hidden_ = true;
  revealed_ = false;
  edge_ = edge;
  StartTimer();
}

void Controller::OnDragFinished() {
  dragging_ = false;
  if (!enabled_ || !mini_active_ || !window_) return;
  const RECT frame = WindowFrame(window_);
  const RECT work = MonitorWorkArea(window_);
  const Edge edge = HideableEdge(frame, work);
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
  const RECT work = MonitorWorkArea(window_);
  if (hidden_) {
    if (Contains(HotRect(frame, work, options().hot_zone), cursor)) {
      MoveWindowTo(window_, restore_);
      hidden_ = false;
      revealed_ = true;
    }
    return;
  }
  if (revealed_ && !Contains(Enlarged(frame, options().reveal_margin), cursor)) {
    const Edge edge = edge_ != Edge::none
                          ? edge_
                          : HideableEdge(frame, work);
    Hide(edge, frame);
  }
}

void Controller::Reset() {
  Reveal();
  StopTimer();
  hidden_ = false;
  revealed_ = false;
  dragging_ = false;
  edge_ = Edge::none;
  restore_ = {};
}

}  // namespace mini_edge_snap
