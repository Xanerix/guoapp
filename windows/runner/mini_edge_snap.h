#ifndef RUNNER_MINI_EDGE_SNAP_H_
#define RUNNER_MINI_EDGE_SNAP_H_

#include <windows.h>

#include <functional>

namespace mini_edge_snap {

constexpr UINT_PTR kTimerId = 0x4D45;

enum class Edge { none, left, right, top, bottom };

struct Options {
  LONG threshold;
  LONG strip;
  LONG reveal_margin;
  LONG hot_zone;
};

Edge DetectEdge(const RECT& frame, const RECT& work, LONG threshold);
RECT HiddenRect(const RECT& frame, const RECT& screen, Edge edge, LONG strip);
RECT Intersect(const RECT& a, const RECT& b);
RECT Enlarged(const RECT& value, LONG margin);
RECT ExtendedToEdge(const RECT& rect, const RECT& screen, Edge edge);
RECT HotRect(const RECT& frame, const RECT& screen, LONG hot_zone);
bool Contains(const RECT& rect, const POINT& point);
RECT ClampInside(const RECT& frame, const RECT& work);
RECT SnappedRect(const RECT& frame, const RECT& work, Edge edge);

class Controller {
 public:
  Controller() = default;

  void Init(HWND window, std::function<void(bool)> on_hidden_changed);
  void SetScale(double scale);
  void SetEnabled(bool enabled);
  void SetMiniActive(bool active);
  void SetDragActive(bool active);
  void OnDragFinished();
  void OnMoving(RECT* rect) const;
  void OnTimerTick();
  void Reset();

 private:
  void SyncActive();
  void SetHidden(bool hidden);
  void Reveal();
  void Hide(Edge edge, const RECT& frame);
  void StartTimer();
  void StopTimer();
  Options options() const;
  Edge HideableEdge(const RECT& frame, const MONITORINFO& monitor) const;
  bool IsOuterEdge(const RECT& frame, const MONITORINFO& monitor,
                   Edge edge) const;

  HWND window_ = nullptr;
  bool enabled_ = false;
  bool mini_active_ = false;
  bool dragging_ = false;
  bool hidden_ = false;
  bool revealed_ = false;
  double scale_ = 1.0;
  Edge edge_ = Edge::none;
  RECT restore_{};
  RECT drag_origin_{};
  POINT drag_cursor_{};
  bool drag_cursor_valid_ = false;
  std::function<void(bool)> on_hidden_changed_;
};

}  // namespace mini_edge_snap

#endif  // RUNNER_MINI_EDGE_SNAP_H_
