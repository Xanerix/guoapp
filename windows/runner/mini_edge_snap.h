#ifndef RUNNER_MINI_EDGE_SNAP_H_
#define RUNNER_MINI_EDGE_SNAP_H_

#include <windows.h>

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
RECT HiddenRect(const RECT& frame, const RECT& work, Edge edge, LONG strip);
RECT Intersect(const RECT& a, const RECT& b);
RECT Enlarged(const RECT& value, LONG margin);
RECT HotRect(const RECT& frame, const RECT& work, LONG hot_zone);
bool Contains(const RECT& rect, const POINT& point);
RECT ClampInside(const RECT& frame, const RECT& work);
RECT SnappedRect(const RECT& frame, const RECT& work, Edge edge);

class Controller {
 public:
  Controller() = default;

  void Init(HWND window);
  void SetScale(double scale);
  void SetEnabled(bool enabled);
  void SetMiniActive(bool active);
  void SetDragActive(bool active);
  void OnDragFinished();
  void OnTimerTick();
  void Reset();

 private:
  void SyncActive();
  void Reveal();
  void Hide(Edge edge, const RECT& frame);
  void StartTimer();
  void StopTimer();
  Options options() const;

  HWND window_ = nullptr;
  bool enabled_ = false;
  bool mini_active_ = false;
  bool dragging_ = false;
  bool hidden_ = false;
  bool revealed_ = false;
  double scale_ = 1.0;
  Edge edge_ = Edge::none;
  RECT restore_{};
};

}  // namespace mini_edge_snap

#endif  // RUNNER_MINI_EDGE_SNAP_H_
