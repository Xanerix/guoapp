const watch = document.querySelector("[data-watch]");
const player = watch.querySelector("[data-player]");
const sheet = watch.querySelector("[data-watch-sheet]");
const episodeForm = sheet.querySelector("[data-episode-form]");
const seasonOptions = sheet.querySelector("[data-season-options]");
const episodeStatus = sheet.querySelector("[data-episode-status]");
let seasonsLoaded = false;
let seasonsLoading = false;
let switchingSeason = false;
let toastTimer;

function notify(message) {
  const toast = watch.querySelector("[data-watch-toast]");
  toast.hidden = false;
  toast.textContent = message;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toast.hidden = true; }, 4000);
}

async function requestJSON(url, options) {
  const response = await fetch(url, options);
  if (response.redirected) {
    window.location.assign("/login");
    throw new Error("登录已过期");
  }
  if (!response.ok) {
    const error = new Error((await response.text()).trim());
    error.status = response.status;
    throw error;
  }
  return response.json();
}

function choice(label, selected) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  button.setAttribute("aria-pressed", String(selected));
  return button;
}

function markSelected(container, button) {
  container.querySelectorAll("[aria-pressed]").forEach(item => {
    item.setAttribute("aria-pressed", String(item === button));
  });
}

function showEpisodeStatus(message) {
  episodeStatus.textContent = message;
  episodeStatus.hidden = !message;
}

async function selectSeason(button, id) {
  if (switchingSeason || id === episodeForm.elements.id.value) return;
  switchingSeason = true;
  const options = episodeForm.querySelector(".episode-options");
  options.hidden = true;
  showEpisodeStatus("正在读取该季分集…");
  try {
    const episodes = await requestJSON("/watch/episodes?" + new URLSearchParams({ id }));
    if (!episodes.length) throw new Error("该季暂无分集");
    const buttons = episodes.map(episode => {
      const item = document.createElement("button");
      item.name = "episode";
      item.value = episode.index;
      item.textContent = episode.label;
      if (id === watch.dataset.id && String(episode.index) === watch.dataset.episode) {
        item.setAttribute("aria-current", "true");
      }
      return item;
    });
    episodeForm.elements.id.value = id;
    options.replaceChildren(...buttons);
    markSelected(seasonOptions, button);
    showEpisodeStatus("");
  } catch {
    showEpisodeStatus("暂时无法读取该季分集，请重试。");
  } finally {
    options.hidden = false;
    switchingSeason = false;
  }
}

async function loadSeasons() {
  if (seasonsLoaded || seasonsLoading) return;
  seasonsLoading = true;
  showEpisodeStatus("正在查看可切换的季…");
  try {
    const seasons = await requestJSON("/watch/seasons?" + new URLSearchParams({ id: watch.dataset.id }));
    seasonOptions.replaceChildren(...seasons.map(season => {
      const button = choice(`第 ${season.number} 季`, season.id === episodeForm.elements.id.value);
      button.addEventListener("click", () => selectSeason(button, season.id));
      return button;
    }));
    seasonsLoaded = true;
    showEpisodeStatus("");
  } catch {
    showEpisodeStatus("季列表暂未加载，仍可选择当前季的分集。");
  } finally {
    seasonsLoading = false;
  }
}

const subtitleOptions = sheet.querySelector("[data-subtitle-options]");
function renderSubtitles() {
  if (!player) return;
  const tracks = Array.from(player.textTracks).filter(track =>
    track.kind === "subtitles" || track.kind === "captions"
  );
  sheet.querySelector("[data-subtitle-status]").hidden = tracks.length > 0;
  const variants = [[null, "关闭"], ...tracks.map((track, index) =>
    [track, track.label || track.language || `字幕 ${index + 1}`]
  )];
  subtitleOptions.replaceChildren(...variants.map(([track, label]) => {
    const button = choice(label, track ? track.mode === "showing" : !tracks.some(item => item.mode === "showing"));
    button.addEventListener("click", () => {
      for (const item of tracks) item.mode = item === track ? "showing" : "disabled";
      sheet.close();
    });
    return button;
  }));
}

const qualityOptions = sheet.querySelector("[data-quality-options]");
const qualityStatus = sheet.querySelector("[data-quality-status]");
const episodeLabels = new Map(Array.from(episodeForm.querySelectorAll("[name=episode]"),
  button => [Number(button.value), button.textContent]));
let requestedQuality = 0;
let switchingMedia = false;
let resumeMedia = null;

function resolveEpisode(episode, quality) {
  return requestJSON("/watch/quality", {
    method: "POST",
    body: new URLSearchParams({ id: watch.dataset.id, episode, quality }),
  });
}

function loadMedia(playback, quality, resume) {
  resumeMedia = { time: resume ? player.currentTime : 0, playing: !resume || !player.paused, rate: player.playbackRate };
  requestedQuality = quality;
  const buttons = [0, ...playback.qualities].map(value => {
    const button = choice(value === 0 ? "自动" : `${value}P`, value === requestedQuality);
    button.dataset.quality = value;
    return button;
  });
  qualityOptions.replaceChildren(...buttons);
  qualityStatus.textContent = playback.quality ? `当前 ${playback.quality}P` : "当前线路";
  player.src = playback.url;
  player.load();
}

async function changeQuality(quality) {
  if (!player || switchingMedia || (quality === requestedQuality && !player.error)) return;
  switchingMedia = true;
  const nextEpisode = Number(watch.dataset.episode) + 1;
  let advance = false;
  qualityStatus.textContent = "正在切换画质…";
  try {
    const playback = await resolveEpisode(watch.dataset.episode, quality);
    advance = player.ended && episodeLabels.has(nextEpisode);
    if (advance) requestedQuality = quality;
    else loadMedia(playback, quality, true);
    sheet.close();
  } catch (error) {
    qualityStatus.textContent = error.message || "切换画质失败，请重试。";
    if (!sheet.open) notify(qualityStatus.textContent);
    advance = player.ended && episodeLabels.has(nextEpisode);
  } finally {
    switchingMedia = false;
    if (advance) moveEpisode(1, true);
  }
}

async function moveEpisode(step, automatic = false) {
  if (!player || switchingMedia || resumeMedia) return;
  const index = Number(watch.dataset.episode) + step;
  const label = episodeLabels.get(index);
  if (label === undefined) {
    if (!automatic) notify(step < 0 ? "已经是本季第一集" : "已经是本季最后一集");
    return;
  }
  switchingMedia = true;
  if (automatic && sheet.open) sheet.close();
  notify(`正在加载${label}…`);
  try {
    let quality = requestedQuality;
    let playback;
    try {
      playback = await resolveEpisode(index, quality);
    } catch (error) {
      if (quality === 0 || error.status !== 422) throw error;
      quality = 0;
      playback = await resolveEpisode(index, quality);
    }
    watch.dataset.episode = index;
    watch.querySelector("[data-playing-label]").textContent = label;
    if (episodeForm.elements.id.value === watch.dataset.id) {
      for (const button of episodeForm.querySelectorAll("[name=episode]")) {
        if (Number(button.value) === index) button.setAttribute("aria-current", "true");
        else button.removeAttribute("aria-current");
      }
    }
    loadMedia(playback, quality, false);
    notify(label);
  } catch {
    notify(`${label}加载失败，请重新滑动或在选集里重试。`);
  } finally {
    switchingMedia = false;
  }
}

qualityOptions.addEventListener("click", event => {
  const item = event.target.closest("[data-quality]");
  if (item) changeQuality(Number(item.dataset.quality));
});

for (const trigger of watch.querySelectorAll("[data-open]")) {
  trigger.addEventListener("click", () => {
    for (const panel of sheet.querySelectorAll("[data-panel]")) panel.hidden = panel.dataset.panel !== trigger.dataset.open;
    sheet.querySelector("#watch-sheet-heading").textContent = trigger.textContent;
    if (trigger.dataset.open === "episodes") loadSeasons();
    if (trigger.dataset.open === "subtitles") renderSubtitles();
    if (trigger.dataset.open === "speed") {
      const active = sheet.querySelector(`[data-rate="${player.playbackRate}"]`);
      markSelected(sheet.querySelector('[data-panel="speed"]'), active);
    }
    sheet.showModal();
  });
}
sheet.addEventListener("click", event => {
  if (event.target === sheet) sheet.close();
});
if (player) {
  const error = watch.querySelector("[data-player-error]");
  const stage = player.closest(".watch-stage");
  let swipe = null;
  stage.addEventListener("touchstart", event => {
    swipe = null;
    if (event.touches.length !== 1 || sheet.open || player.webkitDisplayingFullscreen) return;
    const touch = event.touches[0];
    const bounds = stage.getBoundingClientRect();
    if (touch.clientY > bounds.bottom - 56 || touch.clientX < 18 || touch.clientX > bounds.right - 18) return;
    swipe = { id: touch.identifier, x: touch.clientX, y: touch.clientY };
  }, { passive: true });
  stage.addEventListener("touchcancel", () => { swipe = null; }, { passive: true });
  stage.addEventListener("touchend", event => {
    const from = swipe;
    swipe = null;
    if (!from || event.touches.length !== 0) return;
    const touch = Array.from(event.changedTouches).find(item => item.identifier === from.id);
    if (!touch) return;
    const dx = touch.clientX - from.x, dy = touch.clientY - from.y;
    if (Math.abs(dy) >= 55 && Math.abs(dy) >= Math.abs(dx) * 1.5) {
      moveEpisode(dy < 0 ? 1 : -1);
    }
  }, { passive: true });
  player.addEventListener("ended", () => {
    if (!player.error) moveEpisode(1, true);
  });
  player.addEventListener("error", () => { error.hidden = false; resumeMedia = null; });
  player.addEventListener("loadedmetadata", () => {
    error.hidden = true;
    const resume = resumeMedia;
    resumeMedia = null;
    if (!resume) return;
    if (resume.time > 0) {
      const time = Number.isFinite(player.duration) ? Math.min(resume.time, player.duration) : resume.time;
      player.currentTime = time;
    }
    player.playbackRate = resume.rate;
    if (resume.playing) player.play().catch(() => notify("请点播放器的播放按钮继续观看。"));
  });
  if (player.error) error.hidden = false;
  for (const event of ["addtrack", "removetrack", "change"]) {
    player.textTracks.addEventListener(event, () => {
      if (sheet.open && !sheet.querySelector('[data-panel="subtitles"]').hidden) renderSubtitles();
    });
  }
  for (const button of sheet.querySelectorAll("[data-rate]")) {
    button.setAttribute("aria-pressed", "false");
    button.addEventListener("click", () => {
      player.defaultPlaybackRate = player.playbackRate = Number(button.dataset.rate);
      sheet.close();
    });
  }
  watch.querySelector("[data-fullscreen]").addEventListener("click", async () => {
    try {
      if (player.webkitSupportsFullscreen && player.webkitEnterFullscreen) player.webkitEnterFullscreen();
      else if (document.fullscreenEnabled && player.requestFullscreen) await player.requestFullscreen();
      else notify("视频就绪后可试用播放器自带的全屏按钮。");
    } catch {
      notify("暂时无法进入全屏，请先开始播放后重试。");
    }
  });
}
