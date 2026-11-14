const categories = [
  { id: "romance", name: "言情" },
  { id: "suspense", name: "悬疑" },
];
const dramasPerPage = 12;
const pages = 3;
const episodes = 8;

function validCategory(category) {
  return category === "" || categories.some((entry) => entry.id === category);
}

function drama(sourceId) {
  const title = "JS 示例短剧 " + sourceId;
  return {
    id: host.dramaId(sourceId),
    source: host.source,
    sourceId,
    title,
    name: title,
    desc: "JS 示例模块生成的假数据。",
    intro: "JS 示例模块生成的假数据。",
    totalEpisode: episodes,
  };
}

function dramaPage(page, sourceId) {
  if (page < 1 || page > pages) {
    return { items: [], hasMore: false };
  }
  const items = [];
  for (let offset = 1; offset <= dramasPerPage; offset++) {
    items.push(drama(sourceId((page - 1) * dramasPerPage + offset)));
  }
  return { items, hasMore: page < pages };
}

module.exports = {
  validCategory,

  categories() {
    return categories;
  },

  catalogPage(page, category) {
    if (!validCategory(category)) {
      throw new Error("JS 示例模块没有分类 " + category);
    }
    const prefix = category || categories[0].id;
    return dramaPage(page, (index) => prefix + "-" + index);
  },

  search(page, query) {
    if (query.trim() === "") {
      return { items: [], hasMore: false };
    }
    return dramaPage(page, (index) => "search-" + index);
  },

  detail(sourceId) {
    if (sourceId.trim() === "") {
      throw new Error("JS 示例模块剧集 ID 为空");
    }
    const chapters = [];
    for (let episode = 1; episode <= episodes; episode++) {
      chapters.push({
        id: host.chapterId(sourceId, String(episode)),
        source: host.source,
        title: "第 " + episode + " 集",
        currentEpisode: episode,
      });
    }
    return { drama: drama(sourceId), chapters };
  },

  resolveMedia() {
    throw new Error("JS 示例模块只有假数据，不能播放");
  },
};
