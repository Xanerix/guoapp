const repository = 'Xanerix/guoapp';
const platforms = new Set(['android', 'ios', 'windows', 'darwin', 'linux']);
const builds = new Set(['guoapp', 'hongguojian', 'zhenguojian']);
const moduleClientHeader = 'guoapp';
const moduleIdPattern = /^[a-z0-9][a-z0-9_-]{0,31}$/;
const moduleFilePattern = /^[A-Za-z0-9._-]+\.zip$/;
const moduleHashPattern = /^[0-9a-f]{64}$/;
const modulePackageLimit = 8 * 1024 * 1024;
const moduleRoute = /^\/v1\/modules\/([a-z0-9][a-z0-9_-]{0,31})\/([^/]+)$/;

const json = (body, status = 200, extraHeaders = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...extraHeaders },
  });

const beijingDay = () =>
  new Date(Date.now() + 8 * 3600 * 1000).toISOString().slice(0, 10);

const timingSafeEqual = (a, b) => {
  const left = new TextEncoder().encode(a);
  const right = new TextEncoder().encode(b);
  if (left.length !== right.length) return false;
  let diff = 0;
  for (let i = 0; i < left.length; i += 1) {
    diff |= left[i] ^ right[i];
  }
  return diff === 0;
};

const authorizePublish = (request, env) => {
  const token = env.MODULE_PUBLISH_TOKEN;
  if (!token) return false;
  const auth = request.headers.get('Authorization') ?? '';
  if (!auth.startsWith('Bearer ')) return false;
  return timingSafeEqual(auth.slice(7).trim(), token);
};

const sha256Hex = async (buffer) => {
  const digest = await crypto.subtle.digest('SHA-256', buffer);
  return [...new Uint8Array(digest)]
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('');
};

const toBytes = (value) => {
  if (value instanceof Uint8Array) return value;
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (Array.isArray(value)) return new Uint8Array(value);
  return new Uint8Array();
};

const handleModuleGet = async (request, env, ctx, moduleId, file) => {
  if (request.headers.get('X-App-Client') !== moduleClientHeader) {
    return new Response('Not Found', { status: 404 });
  }
  const cache = caches.default;
  const cacheKey = new Request(request.url, { method: 'GET' });
  const cached = await cache.match(cacheKey);
  if (cached) return cached;

  let response;
  if (file === 'latest.json') {
    const raw = await env.DB.prepare('SELECT value FROM meta WHERE key = ?1')
      .bind(`module:${moduleId}:latest`)
      .first('value');
    if (!raw) return new Response('Not Found', { status: 404 });
    response = new Response(raw, {
      status: 200,
      headers: {
        'Content-Type': 'application/json',
        'Cache-Control': 'public, max-age=120',
      },
    });
  } else {
    if (!moduleFilePattern.test(file) || file.includes('..')) {
      return new Response('Not Found', { status: 404 });
    }
    const row = await env.DB.prepare(
      'SELECT content, sha256 FROM module_packages WHERE module_id = ?1 AND filename = ?2',
    )
      .bind(moduleId, file)
      .first();
    if (!row || !row.content) return new Response('Not Found', { status: 404 });
    response = new Response(toBytes(row.content), {
      status: 200,
      headers: {
        'Content-Type': 'application/zip',
        'Cache-Control': 'public, max-age=31536000, immutable',
        ETag: `"${row.sha256}"`,
      },
    });
  }
  ctx.waitUntil(cache.put(cacheKey, response.clone()));
  return response;
};

const handleModulePut = async (request, env, moduleId, file) => {
  if (!authorizePublish(request, env)) {
    return json({ error: 'unauthorized' }, 401);
  }
  if (!moduleIdPattern.test(moduleId)) {
    return json({ error: 'invalid module id' }, 400);
  }
  const cache = caches.default;
  const cacheKey = new Request(request.url, { method: 'GET' });

  if (file === 'latest.json') {
    let manifest;
    try {
      manifest = await request.json();
    } catch {
      return json({ error: 'invalid json' }, 400);
    }
    if (!manifest || manifest.format !== 1 || typeof manifest.apis !== 'object' || manifest.apis === null) {
      return json({ error: 'invalid manifest' }, 400);
    }
    const encoded = JSON.stringify(manifest);
    await env.DB.prepare(
      'INSERT INTO meta (key, value) VALUES (?1, ?2) ON CONFLICT (key) DO UPDATE SET value = excluded.value',
    )
      .bind(`module:${moduleId}:latest`, encoded)
      .run();
    await cache.delete(cacheKey);
    return json({ ok: true });
  }

  if (!moduleFilePattern.test(file) || file.includes('..')) {
    return json({ error: 'invalid filename' }, 400);
  }
  const raw = await request.arrayBuffer();
  if (raw.byteLength === 0 || raw.byteLength > modulePackageLimit) {
    return json({ error: 'invalid package size' }, 400);
  }
  const expected = (request.headers.get('X-Package-SHA256') ?? '').trim().toLowerCase();
  const actual = await sha256Hex(raw);
  if (expected && (!moduleHashPattern.test(expected) || expected !== actual)) {
    return json({ error: 'sha256 mismatch' }, 400);
  }
  await env.DB.prepare(
    `INSERT INTO module_packages (module_id, filename, content, sha256, updated_at)
     VALUES (?1, ?2, ?3, ?4, ?5)
     ON CONFLICT (module_id, filename) DO UPDATE SET content = excluded.content, sha256 = excluded.sha256, updated_at = excluded.updated_at`,
  )
    .bind(moduleId, file, new Uint8Array(raw), actual, Math.floor(Date.now() / 1000))
    .run();
  await cache.delete(cacheKey);
  return json({ ok: true, sha256: actual, size: raw.byteLength });
};

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    const moduleMatch = moduleRoute.exec(url.pathname);
    if (moduleMatch) {
      const [, moduleId, file] = moduleMatch;
      if (request.method === 'GET') {
        return handleModuleGet(request, env, ctx, moduleId, file);
      }
      if (request.method === 'PUT') {
        return handleModulePut(request, env, moduleId, file);
      }
      return new Response('Method Not Allowed', { status: 405 });
    }
    if (request.method === 'GET' && url.pathname === '/v1/latest') {
      const latest = await env.DB.prepare(
        "SELECT value FROM meta WHERE key = 'latest'",
      ).first('value');
      return json(latest ? JSON.parse(latest) : {});
    }
    if (request.method !== 'POST' || url.pathname !== '/v1/ping') {
      return new Response('Not Found', { status: 404 });
    }
    let body;
    try {
      body = await request.json();
    } catch {
      return json({ error: 'invalid' }, 400);
    }
    const { id, version, platform, build } = body ?? {};
    if (
      !/^[0-9a-f]{32}$/.test(id) ||
      !/^\d+\.\d+\.\d+\+\d+$/.test(version) ||
      !platforms.has(platform) ||
      !builds.has(build)
    ) {
      return json({ error: 'invalid' }, 400);
    }
    await env.DB.prepare(
      `INSERT INTO pings (day, id, version, platform, build) VALUES (?1, ?2, ?3, ?4, ?5)
       ON CONFLICT (day, id) DO UPDATE SET version = excluded.version, platform = excluded.platform, build = excluded.build`,
    )
      .bind(beijingDay(), id, version, platform, build)
      .run();
    return new Response(null, { status: 204 });
  },

  async scheduled(_, env) {
    const response = await fetch(
      `https://api.github.com/repos/${repository}/releases/latest`,
      {
        headers: {
          Accept: 'application/vnd.github+json',
          'User-Agent': 'guoapp-update',
        },
      },
    );
    if (!response.ok) return;
    const release = await response.json();
    const match = /^app-v(\d+\.\d+\.\d+)-(\d+)$/.exec(release.tag_name ?? '');
    if (!match) return;
    const latest = { version: `${match[1]}+${match[2]}`, url: release.html_url };
    await env.DB.prepare(
      "INSERT INTO meta (key, value) VALUES ('latest', ?1) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
    )
      .bind(JSON.stringify(latest))
      .run();
  },
};
