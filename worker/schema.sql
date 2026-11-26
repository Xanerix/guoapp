CREATE TABLE IF NOT EXISTS pings (
  day TEXT NOT NULL,
  id TEXT NOT NULL,
  version TEXT NOT NULL,
  platform TEXT NOT NULL,
  build TEXT NOT NULL,
  PRIMARY KEY (day, id)
);

CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS module_packages (
  module_id TEXT NOT NULL,
  filename TEXT NOT NULL,
  content BLOB NOT NULL,
  sha256 TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (module_id, filename)
);
