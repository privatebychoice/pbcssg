// Package store is the pbcssg content model (SPEC §4): a SQLite page tree with
// draft/published revisions and a key/value settings store. It uses the pure-Go
// modernc.org/sqlite driver (no cgo) so the tool stays a single cross-compiled
// binary.
//
// A page's typed content is stored opaquely as content_json on a revision; the
// store does not interpret it (the render layer does). Publishing points a page
// at a specific revision, so drafts can accumulate without going live.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pages (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  parent_id        INTEGER,                 -- soft ref to pages(id); nav tree
  path             TEXT NOT NULL UNIQUE,     -- URL path, e.g. "/", "/blog/post"
  slug             TEXT NOT NULL,
  title            TEXT NOT NULL,
  type             TEXT NOT NULL DEFAULT 'page',
  status           TEXT NOT NULL DEFAULT 'draft',   -- draft | published
  live_revision_id INTEGER,                  -- soft ref to revisions(id)
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS revisions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  page_id      INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
  content_json TEXT NOT NULL,
  author       TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  is_published INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_revisions_page ON revisions(page_id);

-- Content-addressed media library (SPEC §6.1). The sha256 of the *cleaned*
-- bytes is the primary key, so re-uploading identical content is a no-op and
-- the address never depends on metadata that was stripped.
CREATE TABLE IF NOT EXISTS assets (
  sha256     TEXT PRIMARY KEY,
  filename   TEXT NOT NULL,           -- original name, for display only
  format     TEXT NOT NULL,           -- jpeg | png | svg | webp
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL,        -- byte length of data
  data       BLOB NOT NULL,           -- cleaned, metadata-stripped bytes
  created_at INTEGER NOT NULL
);

-- Filesystem-backed media (audio/video). Large files are not stored in the DB;
-- their cleaned, metadata-stripped bytes live as content-addressed files under
-- the media root (a sibling "media/" dir), and only metadata is rowed here. The
-- sha256 of the cleaned bytes is the primary key, matching the assets table.
CREATE TABLE IF NOT EXISTS media (
  sha256     TEXT PRIMARY KEY,
  filename   TEXT NOT NULL,           -- original name, for display only
  format     TEXT NOT NULL,           -- mp4 | mov | m4a | mp3
  mime       TEXT NOT NULL,
  kind       TEXT NOT NULL,           -- video | audio
  size       INTEGER NOT NULL,        -- byte length of the stored file
  created_at INTEGER NOT NULL
);

-- Admin note for a library item: free-text context ("what this file is for").
-- Keyed by the same content address as assets/media so one table covers images
-- and audio/video alike; a separate table (rather than a column on each) needs no
-- ALTER migration and is removed together with the item it annotates.
CREATE TABLE IF NOT EXISTS media_notes (
  sha256     TEXT PRIMARY KEY,
  note       TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

-- Free-form tags for a library item (SPEC §6.14), one normalized (sha, tag) row per
-- tag so the gallery's by-tag query is a plain lookup. Keyed by the same content
-- address as assets/media; removed together with the item it annotates. Tags are
-- first-party metadata only — they never affect the content address or served bytes.
CREATE TABLE IF NOT EXISTS media_tags (
  sha256 TEXT NOT NULL,
  tag    TEXT NOT NULL,
  PRIMARY KEY (sha256, tag)
);
CREATE INDEX IF NOT EXISTS idx_media_tags_tag ON media_tags (tag);

-- Per-page key material for the deferred-reveal ("hidden") block (SPEC §6.9).
-- One random key per page, generated on first use and rekeyable from the editor.
-- The build derives each reveal block's AES key from this, so a stored key (not a
-- per-build random one) keeps the build reproducible. Keyed by page id so it
-- survives path renames and is removed with the page (ON DELETE CASCADE). The key
-- is not a secret to guard: in the obfuscation mode it ships in the page.
CREATE TABLE IF NOT EXISTS page_keys (
  page_id    INTEGER PRIMARY KEY REFERENCES pages(id) ON DELETE CASCADE,
  key        TEXT NOT NULL,           -- base64 of 32 random bytes
  updated_at INTEGER NOT NULL
);

-- Named key groups for group-gated content (SPEC §6.10). Each group holds one
-- random 256-bit KEK; the build wraps a gated block's per-block DEK under the KEK
-- of every group authorized to unlock it. The KEK is a shared bearer key
-- delivered by gate link (never typed), so it is server-only state here and only
-- ever reaches a visitor's browser via a URL fragment. An optional splash_page_id
-- associates the group with an authored welcome/landing page that deposits the KEK
-- into the visitor's keyring; ON DELETE SET NULL so deleting that page just drops
-- the association (the group falls back to the generic confirmation page).
CREATE TABLE IF NOT EXISTS key_groups (
  id             INTEGER PRIMARY KEY,
  alias          TEXT NOT NULL UNIQUE,
  kek            TEXT NOT NULL,        -- base64 of 32 random bytes
  splash_page_id INTEGER REFERENCES pages(id) ON DELETE SET NULL,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

-- Site favicon / app-icon assets (SPEC §6.11). Each row is one canonical file
-- served at a fixed site-root path (favicon.svg, favicon.ico, apple-touch-icon.png,
-- icon-192.png, icon-512.png). Keyed by that filename, so re-uploading a slot
-- replaces it. Unlike the content-addressed media library these have stable,
-- browser-expected names, so they are stored separately and emitted at the root.
CREATE TABLE IF NOT EXISTS favicons (
  name       TEXT PRIMARY KEY,        -- canonical filename (e.g. "favicon.svg")
  mime       TEXT NOT NULL,
  data       BLOB NOT NULL,           -- cleaned bytes (SVG sanitized / PNG stripped)
  updated_at INTEGER NOT NULL
);
`

// Status values for a page.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
)

// Page is a node in the content tree.
type Page struct {
	ID             int64
	ParentID       *int64
	Path           string
	Slug           string
	Title          string
	Type           string
	Status         string
	LiveRevisionID *int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Revision is one saved version of a page's content.
type Revision struct {
	ID          int64
	PageID      int64
	ContentJSON string
	Author      string
	CreatedAt   time.Time
	IsPublished bool
}

// PublishedPage is a published page together with its live revision's content.
type PublishedPage struct {
	Page
	ContentJSON string
}

// Store is a handle to the content database. It is safe for concurrent use.
type Store struct {
	db        *sql.DB
	now       func() time.Time
	mediaRoot string // filesystem dir for content-addressed audio/video files
}

// Open opens (creating if needed) the SQLite database at path and applies the
// schema. Use ":memory:" for an ephemeral database. Filesystem-backed media
// (audio/video) is stored in a sibling "media/" directory next to the database
// file; an in-memory or unnamed database has no media root and rejects such
// uploads.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: enable foreign keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	mediaRoot := ""
	if path != "" && path != ":memory:" {
		mediaRoot = filepath.Join(filepath.Dir(path), "media")
	}
	return &Store{db: db, now: time.Now, mediaRoot: mediaRoot}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// CreatePage inserts a new draft page and returns its id. Path must be unique.
func (s *Store) CreatePage(p Page) (int64, error) {
	now := s.now().Unix()
	typ := p.Type
	if typ == "" {
		typ = "page"
	}
	res, err := s.db.Exec(
		`INSERT INTO pages (parent_id, path, slug, title, type, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(p.ParentID), p.Path, p.Slug, p.Title, typ, StatusDraft, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("store: create page %q: %w", p.Path, err)
	}
	return res.LastInsertId()
}

// SaveRevision records a new (draft) revision for a page and returns its id.
func (s *Store) SaveRevision(pageID int64, contentJSON, author string) (int64, error) {
	now := s.now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO revisions (page_id, content_json, author, created_at, is_published)
		 VALUES (?, ?, ?, ?, 0)`,
		pageID, contentJSON, author, now,
	)
	if err != nil {
		return 0, fmt.Errorf("store: save revision for page %d: %w", pageID, err)
	}
	if _, err := s.db.Exec(`UPDATE pages SET updated_at = ? WHERE id = ?`, now, pageID); err != nil {
		return 0, fmt.Errorf("store: touch page %d: %w", pageID, err)
	}
	rid, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return rid, nil
}

// Publish marks revisionID as the live, published revision of pageID.
func (s *Store) Publish(pageID, revisionID int64) error {
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var owner int64
	if err := tx.QueryRow(`SELECT page_id FROM revisions WHERE id = ?`, revisionID).Scan(&owner); err != nil {
		return fmt.Errorf("store: revision %d: %w", revisionID, err)
	}
	if owner != pageID {
		return fmt.Errorf("store: revision %d does not belong to page %d", revisionID, pageID)
	}
	if _, err := tx.Exec(
		`UPDATE pages SET status = ?, live_revision_id = ?, updated_at = ? WHERE id = ?`,
		StatusPublished, revisionID, now, pageID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE revisions SET is_published = 1 WHERE id = ?`, revisionID); err != nil {
		return err
	}
	return tx.Commit()
}

// Unpublish reverts a page to draft: it clears the live revision and sets the
// status back to draft, so the page drops out of the next build. Its revisions
// are preserved — the latest is still the working draft.
func (s *Store) Unpublish(pageID int64) error {
	now := s.now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var live sql.NullInt64
	if err := tx.QueryRow(`SELECT live_revision_id FROM pages WHERE id = ?`, pageID).Scan(&live); err != nil {
		return fmt.Errorf("store: page %d: %w", pageID, err)
	}
	if _, err := tx.Exec(
		`UPDATE pages SET status = ?, live_revision_id = NULL, updated_at = ? WHERE id = ?`,
		StatusDraft, now, pageID,
	); err != nil {
		return err
	}
	if live.Valid {
		if _, err := tx.Exec(`UPDATE revisions SET is_published = 0 WHERE id = ?`, live.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Published returns every published page with its live revision's content,
// ordered by path (deterministic for builds).
func (s *Store) Published() ([]PublishedPage, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.parent_id, p.path, p.slug, p.title, p.type, p.status,
		       p.live_revision_id, p.created_at, p.updated_at, r.content_json
		FROM pages p
		JOIN revisions r ON r.id = p.live_revision_id
		WHERE p.status = ? AND p.live_revision_id IS NOT NULL
		ORDER BY p.path`, StatusPublished)
	if err != nil {
		return nil, fmt.Errorf("store: query published: %w", err)
	}
	defer rows.Close()

	var out []PublishedPage
	for rows.Next() {
		var (
			pp                 PublishedPage
			parentID, liveRev  sql.NullInt64
			createdAt, updated int64
		)
		if err := rows.Scan(
			&pp.ID, &parentID, &pp.Path, &pp.Slug, &pp.Title, &pp.Type, &pp.Status,
			&liveRev, &createdAt, &updated, &pp.ContentJSON,
		); err != nil {
			return nil, err
		}
		pp.ParentID = fromNull(parentID)
		pp.LiveRevisionID = fromNull(liveRev)
		pp.CreatedAt = time.Unix(createdAt, 0)
		pp.UpdatedAt = time.Unix(updated, 0)
		out = append(out, pp)
	}
	return out, rows.Err()
}

// Revision returns a single revision by id.
func (s *Store) Revision(id int64) (Revision, error) {
	var (
		r         Revision
		createdAt int64
		published int64
	)
	err := s.db.QueryRow(
		`SELECT id, page_id, content_json, author, created_at, is_published FROM revisions WHERE id = ?`, id,
	).Scan(&r.ID, &r.PageID, &r.ContentJSON, &r.Author, &createdAt, &published)
	if err != nil {
		return Revision{}, fmt.Errorf("store: revision %d: %w", id, err)
	}
	r.CreatedAt = time.Unix(createdAt, 0)
	r.IsPublished = published != 0
	return r, nil
}

const pageCols = `id, parent_id, path, slug, title, type, status, live_revision_id, created_at, updated_at`

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanPage(sc scanner) (Page, error) {
	var (
		p                  Page
		parentID, liveRev  sql.NullInt64
		createdAt, updated int64
	)
	if err := sc.Scan(&p.ID, &parentID, &p.Path, &p.Slug, &p.Title, &p.Type, &p.Status, &liveRev, &createdAt, &updated); err != nil {
		return Page{}, err
	}
	p.ParentID = fromNull(parentID)
	p.LiveRevisionID = fromNull(liveRev)
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updated, 0)
	return p, nil
}

// Pages returns every page ordered by path (the full tree for the editor).
func (s *Store) Pages() ([]Page, error) {
	rows, err := s.db.Query(`SELECT ` + pageCols + ` FROM pages ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("store: pages: %w", err)
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// pageSortColumns whitelists the sortable columns for PagesPage (column names
// can't be parameterized, so only these fixed values reach the query).
var pageSortColumns = map[string]string{
	"title":   "title COLLATE NOCASE",
	"path":    "path COLLATE NOCASE",
	"status":  "status",
	"updated": "updated_at",
}

// PagesPage returns a sorted, paginated slice of pages plus the total count. sort
// is one of title|path|status|updated (default path); asc controls direction. A
// stable secondary sort by path keeps ordering deterministic.
func (s *Store) PagesPage(sort string, asc bool, limit, offset int) ([]Page, int, error) {
	col, ok := pageSortColumns[sort]
	if !ok {
		col = pageSortColumns["path"]
	}
	dir := "DESC"
	if asc {
		dir = "ASC"
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count pages: %w", err)
	}
	q := `SELECT ` + pageCols + ` FROM pages ORDER BY ` + col + ` ` + dir + `, path COLLATE NOCASE ASC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("store: pages page: %w", err)
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// Page returns a single page by id.
func (s *Store) Page(id int64) (Page, error) {
	p, err := scanPage(s.db.QueryRow(`SELECT `+pageCols+` FROM pages WHERE id = ?`, id))
	if err != nil {
		return Page{}, fmt.Errorf("store: page %d: %w", id, err)
	}
	return p, nil
}

// LatestRevision returns a page's most recent revision (the current working
// content, draft or published), or ok=false if it has none yet.
func (s *Store) LatestRevision(pageID int64) (Revision, bool, error) {
	var (
		r                    Revision
		createdAt, published int64
	)
	err := s.db.QueryRow(
		`SELECT id, page_id, content_json, author, created_at, is_published
		 FROM revisions WHERE page_id = ? ORDER BY id DESC LIMIT 1`, pageID,
	).Scan(&r.ID, &r.PageID, &r.ContentJSON, &r.Author, &createdAt, &published)
	if err == sql.ErrNoRows {
		return Revision{}, false, nil
	}
	if err != nil {
		return Revision{}, false, fmt.Errorf("store: latest revision for page %d: %w", pageID, err)
	}
	r.CreatedAt = time.Unix(createdAt, 0)
	r.IsPublished = published != 0
	return r, true, nil
}

// UpdatePage updates a page's editable fields (not its status or live revision,
// which change via Publish).
func (s *Store) UpdatePage(p Page) error {
	typ := p.Type
	if typ == "" {
		typ = "page"
	}
	_, err := s.db.Exec(
		`UPDATE pages SET parent_id = ?, path = ?, slug = ?, title = ?, type = ?, updated_at = ? WHERE id = ?`,
		nullInt(p.ParentID), p.Path, p.Slug, p.Title, typ, s.now().Unix(), p.ID,
	)
	if err != nil {
		return fmt.Errorf("store: update page %d: %w", p.ID, err)
	}
	return nil
}

// DeletePage removes a page and (via ON DELETE CASCADE) its revisions.
func (s *Store) DeletePage(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM pages WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete page %d: %w", id, err)
	}
	return nil
}

// Asset is a stored media file's metadata (without its bytes). Its SHA256 — the
// hash of the cleaned bytes — is the content address.
type Asset struct {
	SHA256    string
	Filename  string
	Format    string
	MIME      string
	Size      int64
	CreatedAt time.Time
}

// AssetData is an Asset together with its cleaned bytes.
type AssetData struct {
	Asset
	Data []byte
}

// PutAsset stores a cleaned media file. It is content-addressed and idempotent:
// storing bytes whose hash already exists is a no-op (the first filename wins).
func (s *Store) PutAsset(a AssetData) error {
	_, err := s.db.Exec(
		`INSERT INTO assets (sha256, filename, format, mime, size, data, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(sha256) DO NOTHING`,
		a.SHA256, a.Filename, a.Format, a.MIME, int64(len(a.Data)), a.Data, s.now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: put asset %s: %w", a.SHA256, err)
	}
	return nil
}

// Assets lists media metadata (without bytes), newest first.
func (s *Store) Assets() ([]Asset, error) {
	rows, err := s.db.Query(
		`SELECT sha256, filename, format, mime, size, created_at FROM assets ORDER BY created_at DESC, sha256`)
	if err != nil {
		return nil, fmt.Errorf("store: assets: %w", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var (
			a         Asset
			createdAt int64
		)
		if err := rows.Scan(&a.SHA256, &a.Filename, &a.Format, &a.MIME, &a.Size, &createdAt); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// likeContains builds a case-insensitive "contains" LIKE pattern, escaping the
// LIKE wildcards in the user's term so they are matched literally.
func likeContains(q string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return "%" + r.Replace(q) + "%"
}

// CountAssets returns the total number of stored images (for library tab counts).
func (s *Store) CountAssets() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count assets: %w", err)
	}
	return n, nil
}

// AssetPage lists image metadata filtered by an optional search over the
// filename and the admin note, newest first, limited to a page. It returns the
// rows plus the total matching count (for pagination). A blank q matches
// everything. The note is LEFT JOINed (at most one per content address), so a
// row without a note is unaffected and the count never double-counts.
func (s *Store) AssetPage(q string, limit, offset int) ([]Asset, int, error) {
	from := ` FROM assets a LEFT JOIN media_notes n ON n.sha256 = a.sha256`
	where, args := "", []any{}
	if strings.TrimSpace(q) != "" {
		where = ` WHERE (a.filename LIKE ? ESCAPE '\' OR n.note LIKE ? ESCAPE '\')`
		like := likeContains(q)
		args = append(args, like, like)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count assets: %w", err)
	}
	rows, err := s.db.Query(
		`SELECT a.sha256, a.filename, a.format, a.mime, a.size, a.created_at`+from+where+
			` ORDER BY a.created_at DESC, a.sha256 LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: asset page: %w", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var (
			a         Asset
			createdAt int64
		)
		if err := rows.Scan(&a.SHA256, &a.Filename, &a.Format, &a.MIME, &a.Size, &createdAt); err != nil {
			return nil, 0, err
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// Asset returns a single stored media file (with bytes) by content address.
func (s *Store) Asset(sha string) (AssetData, error) {
	var (
		a         AssetData
		createdAt int64
	)
	err := s.db.QueryRow(
		`SELECT sha256, filename, format, mime, size, data, created_at FROM assets WHERE sha256 = ?`, sha,
	).Scan(&a.SHA256, &a.Filename, &a.Format, &a.MIME, &a.Size, &a.Data, &createdAt)
	if err == sql.ErrNoRows {
		return AssetData{}, sql.ErrNoRows
	}
	if err != nil {
		return AssetData{}, fmt.Errorf("store: asset %s: %w", sha, err)
	}
	a.CreatedAt = time.Unix(createdAt, 0)
	return a, nil
}

// DeleteAsset removes a stored media file (and its admin note) by content address.
func (s *Store) DeleteAsset(sha string) error {
	if _, err := s.db.Exec(`DELETE FROM assets WHERE sha256 = ?`, sha); err != nil {
		return fmt.Errorf("store: delete asset %s: %w", sha, err)
	}
	if err := s.deleteMediaTags(sha); err != nil {
		return err
	}
	return s.deleteMediaNote(sha)
}

// --- Filesystem-backed media (audio/video, SPEC §6.1) ---

// MediaFile is a filesystem-backed audio/video file's metadata. Its bytes live
// under the media root as <sha256>.<ext>; only this row lives in the DB.
type MediaFile struct {
	SHA256    string
	Filename  string
	Format    string // mp4 | mov | m4a | mp3
	MIME      string
	Kind      string // video | audio
	Size      int64
	CreatedAt time.Time
}

// mediaFilePath returns the on-disk path for a media file's content-addressed
// bytes, or an error if the store has no media root (e.g. an in-memory DB).
func (s *Store) mediaFilePath(sha, format string) (string, error) {
	if s.mediaRoot == "" {
		return "", fmt.Errorf("store: this database has no media directory (audio/video needs a file-backed -db)")
	}
	return filepath.Join(s.mediaRoot, sha+"."+mediaExt(format)), nil
}

// PutMedia writes cleaned media bytes to the media root and records their
// metadata. It is content-addressed and idempotent: re-storing identical bytes
// overwrites the file with the same content and keeps the first row. The bytes
// are written before the row, so a row never dangles without its file.
func (s *Store) PutMedia(m MediaFile, data []byte) error {
	full, err := s.mediaFilePath(m.SHA256, m.Format)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("store: media dir: %w", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return fmt.Errorf("store: write media %s: %w", m.SHA256, err)
	}
	_, err = s.db.Exec(
		`INSERT INTO media (sha256, filename, format, mime, kind, size, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(sha256) DO NOTHING`,
		m.SHA256, m.Filename, m.Format, m.MIME, m.Kind, int64(len(data)), s.now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: put media %s: %w", m.SHA256, err)
	}
	return nil
}

// MediaList lists filesystem-backed media metadata, newest first.
func (s *Store) MediaList() ([]MediaFile, error) {
	rows, err := s.db.Query(
		`SELECT sha256, filename, format, mime, kind, size, created_at FROM media ORDER BY created_at DESC, sha256`)
	if err != nil {
		return nil, fmt.Errorf("store: media list: %w", err)
	}
	defer rows.Close()
	var out []MediaFile
	for rows.Next() {
		var (
			m         MediaFile
			createdAt int64
		)
		if err := rows.Scan(&m.SHA256, &m.Filename, &m.Format, &m.MIME, &m.Kind, &m.Size, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountMedia returns the number of filesystem-backed media of a given kind
// ("video"/"audio") — for library tab counts.
func (s *Store) CountMedia(kind string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM media WHERE kind = ?`, kind).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count media: %w", err)
	}
	return n, nil
}

// MediaPage lists media of one kind ("video"/"audio") filtered by an optional
// search over the filename and the admin note, newest first, limited to a page.
// It returns the rows plus the total matching count (for pagination). A blank q
// matches everything. The note is LEFT JOINed (at most one per content address).
func (s *Store) MediaPage(kind, q string, limit, offset int) ([]MediaFile, int, error) {
	from := ` FROM media m LEFT JOIN media_notes n ON n.sha256 = m.sha256`
	where := ` WHERE m.kind = ?`
	args := []any{kind}
	if strings.TrimSpace(q) != "" {
		where += ` AND (m.filename LIKE ? ESCAPE '\' OR n.note LIKE ? ESCAPE '\')`
		like := likeContains(q)
		args = append(args, like, like)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*)`+from+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count media: %w", err)
	}
	rows, err := s.db.Query(
		`SELECT m.sha256, m.filename, m.format, m.mime, m.kind, m.size, m.created_at`+from+where+
			` ORDER BY m.created_at DESC, m.sha256 LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: media page: %w", err)
	}
	defer rows.Close()
	var out []MediaFile
	for rows.Next() {
		var (
			m         MediaFile
			createdAt int64
		)
		if err := rows.Scan(&m.SHA256, &m.Filename, &m.Format, &m.MIME, &m.Kind, &m.Size, &createdAt); err != nil {
			return nil, 0, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// Media returns a media file's metadata by content address (sql.ErrNoRows if
// absent).
func (s *Store) Media(sha string) (MediaFile, error) {
	var (
		m         MediaFile
		createdAt int64
	)
	err := s.db.QueryRow(
		`SELECT sha256, filename, format, mime, kind, size, created_at FROM media WHERE sha256 = ?`, sha,
	).Scan(&m.SHA256, &m.Filename, &m.Format, &m.MIME, &m.Kind, &m.Size, &createdAt)
	if err == sql.ErrNoRows {
		return MediaFile{}, sql.ErrNoRows
	}
	if err != nil {
		return MediaFile{}, fmt.Errorf("store: media %s: %w", sha, err)
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	return m, nil
}

// OpenMedia opens a media file's on-disk bytes for streaming (the caller closes
// the returned file). It returns the metadata alongside so the handler can set
// the content type.
func (s *Store) OpenMedia(sha string) (*os.File, MediaFile, error) {
	m, err := s.Media(sha)
	if err != nil {
		return nil, MediaFile{}, err
	}
	full, err := s.mediaFilePath(m.SHA256, m.Format)
	if err != nil {
		return nil, MediaFile{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, MediaFile{}, fmt.Errorf("store: open media %s: %w", sha, err)
	}
	return f, m, nil
}

// ReadMedia reads a media file's full on-disk bytes (used by the build to copy
// and re-verify it). Prefer OpenMedia for streaming to clients.
func (s *Store) ReadMedia(sha string) ([]byte, MediaFile, error) {
	f, m, err := s.OpenMedia(sha)
	if err != nil {
		return nil, MediaFile{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, MediaFile{}, fmt.Errorf("store: read media %s: %w", sha, err)
	}
	return data, m, nil
}

// DeleteMedia removes a media file's row and its on-disk bytes.
func (s *Store) DeleteMedia(sha string) error {
	m, err := s.Media(sha)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if full, perr := s.mediaFilePath(m.SHA256, m.Format); perr == nil {
			if rerr := os.Remove(full); rerr != nil && !os.IsNotExist(rerr) {
				return fmt.Errorf("store: remove media file %s: %w", sha, rerr)
			}
		}
	}
	if _, err := s.db.Exec(`DELETE FROM media WHERE sha256 = ?`, sha); err != nil {
		return fmt.Errorf("store: delete media %s: %w", sha, err)
	}
	if err := s.deleteMediaTags(sha); err != nil {
		return err
	}
	return s.deleteMediaNote(sha)
}

// MediaExists reports whether a content-addressed item is present in the store —
// an image (BLOB-stored) or an audio/video file (filesystem-backed). It is a
// cheap existence check (no BLOB or file read), used to flag broken local media
// references at save and build time.
func (s *Store) MediaExists(sha string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM assets WHERE sha256 = ?
		 UNION ALL
		 SELECT 1 FROM media WHERE sha256 = ?
		 LIMIT 1`, sha, sha,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: media exists %s: %w", sha, err)
	}
	return true, nil
}

// --- Media notes (admin context for a library item, SPEC §6.1) ---

// MaxMediaNote caps a media note's length (in runes). A note is short context
// ("hero image for the privacy page"), not a document.
const MaxMediaNote = 500

// deleteMediaNote removes any admin note for a content address. It runs when the
// annotated item is deleted, so a note never dangles after its file is gone.
func (s *Store) deleteMediaNote(sha string) error {
	if _, err := s.db.Exec(`DELETE FROM media_notes WHERE sha256 = ?`, sha); err != nil {
		return fmt.Errorf("store: delete media note %s: %w", sha, err)
	}
	return nil
}

// SetMediaNote upserts the admin note for a media item (image or audio/video),
// keyed by content address. An empty note deletes the row, so "no note" leaves
// nothing behind. The caller is expected to have trimmed and length-capped it.
func (s *Store) SetMediaNote(sha, note string) error {
	if note == "" {
		return s.deleteMediaNote(sha)
	}
	_, err := s.db.Exec(
		`INSERT INTO media_notes (sha256, note, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(sha256) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at`,
		sha, note, s.now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: set media note %s: %w", sha, err)
	}
	return nil
}

// MediaNote returns the admin note for a content address, or "" if none.
func (s *Store) MediaNote(sha string) (string, error) {
	var note string
	err := s.db.QueryRow(`SELECT note FROM media_notes WHERE sha256 = ?`, sha).Scan(&note)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: media note %s: %w", sha, err)
	}
	return note, nil
}

// MediaNotesFor returns the notes for the given content addresses as a sha→note
// map (addresses without a note are simply absent). It annotates a whole library
// page in one query instead of one per row.
func (s *Store) MediaNotesFor(shas []string) (map[string]string, error) {
	out := make(map[string]string, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(shas)), ",")
	args := make([]any, len(shas))
	for i, sha := range shas {
		args[i] = sha
	}
	rows, err := s.db.Query(`SELECT sha256, note FROM media_notes WHERE sha256 IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: media notes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sha, note string
		if err := rows.Scan(&sha, &note); err != nil {
			return nil, err
		}
		out[sha] = note
	}
	return out, rows.Err()
}

// MaxMediaTags caps how many tags one library item may carry; maxMediaTagLen caps a
// single tag's length (in runes). Tags are short organizational labels (§6.14).
const (
	MaxMediaTags   = 30
	maxMediaTagLen = 60
)

// NormalizeMediaTag canonicalizes a media tag: trimmed, internal whitespace
// collapsed, lower-cased, and length-capped — so "Privacy " and "privacy" are one
// tag and the gallery's by-tag match is a plain equality. Returns "" for a blank tag.
func NormalizeMediaTag(t string) string {
	t = strings.ToLower(strings.Join(strings.Fields(t), " "))
	if r := []rune(t); len(r) > maxMediaTagLen {
		t = strings.TrimSpace(string(r[:maxMediaTagLen]))
	}
	return t
}

// SetMediaTags replaces the tag set for a content address with the normalized, de-
// duplicated tags (capped at MaxMediaTags). An empty result clears the item's tags.
func (s *Store) SetMediaTags(sha string, tags []string) error {
	seen := map[string]bool{}
	var norm []string
	for _, t := range tags {
		n := NormalizeMediaTag(t)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		norm = append(norm, n)
		if len(norm) >= MaxMediaTags {
			break
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: set media tags %s: %w", sha, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM media_tags WHERE sha256 = ?`, sha); err != nil {
		return fmt.Errorf("store: clear media tags %s: %w", sha, err)
	}
	for _, t := range norm {
		if _, err := tx.Exec(`INSERT INTO media_tags (sha256, tag) VALUES (?, ?) ON CONFLICT DO NOTHING`, sha, t); err != nil {
			return fmt.Errorf("store: insert media tag %s/%s: %w", sha, t, err)
		}
	}
	return tx.Commit()
}

// deleteMediaTags removes all tags for a content address (called when the item is
// deleted, so a tag never dangles after its file is gone).
func (s *Store) deleteMediaTags(sha string) error {
	if _, err := s.db.Exec(`DELETE FROM media_tags WHERE sha256 = ?`, sha); err != nil {
		return fmt.Errorf("store: delete media tags %s: %w", sha, err)
	}
	return nil
}

// MediaTags returns a content address's tags, sorted for a stable display.
func (s *Store) MediaTags(sha string) ([]string, error) {
	rows, err := s.db.Query(`SELECT tag FROM media_tags WHERE sha256 = ? ORDER BY tag`, sha)
	if err != nil {
		return nil, fmt.Errorf("store: media tags %s: %w", sha, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MediaTagsFor returns the tags for the given content addresses as a sha→tags map,
// annotating a whole library page in one query.
func (s *Store) MediaTagsFor(shas []string) (map[string][]string, error) {
	out := make(map[string][]string, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(shas)), ",")
	args := make([]any, len(shas))
	for i, sha := range shas {
		args[i] = sha
	}
	rows, err := s.db.Query(`SELECT sha256, tag FROM media_tags WHERE sha256 IN (`+placeholders+`) ORDER BY sha256, tag`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: media tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sha, tag string
		if err := rows.Scan(&sha, &tag); err != nil {
			return nil, err
		}
		out[sha] = append(out[sha], tag)
	}
	return out, rows.Err()
}

// AssetsByTag returns the images (assets) carrying the given tag, newest first, for
// the gallery's by-tag mode (§6.14). The tag is normalized so matching is consistent
// with how tags are stored.
func (s *Store) AssetsByTag(tag string) ([]Asset, error) {
	t := NormalizeMediaTag(tag)
	if t == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT a.sha256, a.filename, a.format, a.mime, a.size, a.created_at
		 FROM assets a JOIN media_tags mt ON mt.sha256 = a.sha256
		 WHERE mt.tag = ? ORDER BY a.created_at DESC, a.sha256`, t)
	if err != nil {
		return nil, fmt.Errorf("store: assets by tag %q: %w", t, err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var (
			a         Asset
			createdAt int64
		)
		if err := rows.Scan(&a.SHA256, &a.Filename, &a.Format, &a.MIME, &a.Size, &createdAt); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

// RevealKeyLen is the length in bytes of a page's reveal key (AES-256 material).
const RevealKeyLen = 32

// newRevealKey returns RevealKeyLen cryptographically-random bytes.
func newRevealKey() ([]byte, error) {
	b := make([]byte, RevealKeyLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("store: generate page key: %w", err)
	}
	return b, nil
}

// PageKey returns the page's reveal-block key (SPEC §6.9), generating and
// persisting one on first use. It is idempotent: once created the same key is
// returned on every call, so the build stays reproducible. The editor calls it on
// save so a key always exists by build time; the build reads it per page.
func (s *Store) PageKey(pageID int64) ([]byte, error) {
	var b64 string
	err := s.db.QueryRow(`SELECT key FROM page_keys WHERE page_id = ?`, pageID).Scan(&b64)
	switch {
	case err == nil:
		key, derr := base64.StdEncoding.DecodeString(b64)
		if derr != nil {
			return nil, fmt.Errorf("store: decode page key %d: %w", pageID, derr)
		}
		return key, nil
	case err == sql.ErrNoRows:
		return s.RekeyPage(pageID)
	default:
		return nil, fmt.Errorf("store: page key %d: %w", pageID, err)
	}
}

// RekeyPage generates a fresh random key for the page and persists it, replacing
// any existing one. Reveal blocks re-encode under the new key on the next build,
// rotating the obfuscation layer (SPEC §6.9).
func (s *Store) RekeyPage(pageID int64) ([]byte, error) {
	key, err := newRevealKey()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(
		`INSERT INTO page_keys (page_id, key, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(page_id) DO UPDATE SET key = excluded.key, updated_at = excluded.updated_at`,
		pageID, base64.StdEncoding.EncodeToString(key), s.now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: rekey page %d: %w", pageID, err)
	}
	return key, nil
}

// KEKLen is the length in bytes of a key group's KEK (AES-256 material).
const KEKLen = 32

// KeyGroup is a named group whose KEK unlocks group-gated content blocks
// authorized for it (SPEC §6.10). KEK holds the decoded key bytes (server-only;
// it reaches a visitor only via a gate-link fragment). SplashPageID, when set,
// points at the authored page that welcomes the group and deposits the KEK.
type KeyGroup struct {
	ID           int64
	Alias        string
	KEK          []byte
	SplashPageID *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NormalizeAlias canonicalizes a key-group alias to a stable, matchable slug:
// lower-cased, spaces/underscores collapsed to single hyphens, and restricted to
// [a-z0-9-]. It is applied both when a group is created and when a block's authored
// group list is sanitized, so build-time matching is a plain string compare.
// Returns "" for input that has no usable characters.
func NormalizeAlias(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// newKEK returns KEKLen cryptographically-random bytes.
func newKEK() ([]byte, error) {
	b := make([]byte, KEKLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("store: generate KEK: %w", err)
	}
	return b, nil
}

// scanKeyGroup decodes a key_groups row into a KeyGroup.
func scanKeyGroup(sc interface{ Scan(...any) error }) (KeyGroup, error) {
	var (
		g       KeyGroup
		kek     string
		splash  sql.NullInt64
		created int64
		updated int64
	)
	if err := sc.Scan(&g.ID, &g.Alias, &kek, &splash, &created, &updated); err != nil {
		return KeyGroup{}, err
	}
	dk, err := base64.StdEncoding.DecodeString(kek)
	if err != nil {
		return KeyGroup{}, fmt.Errorf("store: decode KEK %q: %w", g.Alias, err)
	}
	g.KEK = dk
	if splash.Valid {
		id := splash.Int64
		g.SplashPageID = &id
	}
	g.CreatedAt = time.Unix(created, 0)
	g.UpdatedAt = time.Unix(updated, 0)
	return g, nil
}

// KeyGroups lists all key groups, ordered by alias.
func (s *Store) KeyGroups() ([]KeyGroup, error) {
	rows, err := s.db.Query(
		`SELECT id, alias, kek, splash_page_id, created_at, updated_at
		 FROM key_groups ORDER BY alias`)
	if err != nil {
		return nil, fmt.Errorf("store: key groups: %w", err)
	}
	defer rows.Close()
	var out []KeyGroup
	for rows.Next() {
		g, err := scanKeyGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// KeyGroup returns the group with the given (already-normalized) alias.
func (s *Store) KeyGroup(alias string) (KeyGroup, bool, error) {
	row := s.db.QueryRow(
		`SELECT id, alias, kek, splash_page_id, created_at, updated_at
		 FROM key_groups WHERE alias = ?`, alias)
	g, err := scanKeyGroup(row)
	if err == sql.ErrNoRows {
		return KeyGroup{}, false, nil
	}
	if err != nil {
		return KeyGroup{}, false, fmt.Errorf("store: key group %q: %w", alias, err)
	}
	return g, true, nil
}

// KEKsByAlias returns a map of alias → decoded KEK for every key group, so the
// build can resolve each block's authorized groups in one query.
func (s *Store) KEKsByAlias() (map[string][]byte, error) {
	groups, err := s.KeyGroups()
	if err != nil {
		return nil, err
	}
	m := make(map[string][]byte, len(groups))
	for _, g := range groups {
		m[g.Alias] = g.KEK
	}
	return m, nil
}

// CreateKeyGroup creates a new group with a fresh random KEK under the given
// alias (normalized by the caller). The alias must be non-empty and unique.
func (s *Store) CreateKeyGroup(alias string) (KeyGroup, error) {
	if alias == "" {
		return KeyGroup{}, fmt.Errorf("store: empty key-group alias")
	}
	kek, err := newKEK()
	if err != nil {
		return KeyGroup{}, err
	}
	now := s.now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO key_groups (alias, kek, splash_page_id, created_at, updated_at)
		 VALUES (?, ?, NULL, ?, ?)`,
		alias, base64.StdEncoding.EncodeToString(kek), now, now)
	if err != nil {
		return KeyGroup{}, fmt.Errorf("store: create key group %q: %w", alias, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return KeyGroup{}, fmt.Errorf("store: create key group %q: %w", alias, err)
	}
	return KeyGroup{ID: id, Alias: alias, KEK: kek,
		CreatedAt: time.Unix(now, 0), UpdatedAt: time.Unix(now, 0)}, nil
}

// RenameKeyGroup changes a group's alias (normalized by the caller). Existing
// gate links keep working — only the label changes — but blocks still referencing
// the old alias will no longer match, so the caller warns the operator.
func (s *Store) RenameKeyGroup(id int64, alias string) error {
	if alias == "" {
		return fmt.Errorf("store: empty key-group alias")
	}
	_, err := s.db.Exec(
		`UPDATE key_groups SET alias = ?, updated_at = ? WHERE id = ?`,
		alias, s.now().Unix(), id)
	if err != nil {
		return fmt.Errorf("store: rename key group %d: %w", id, err)
	}
	return nil
}

// DeleteKeyGroup removes a group. Blocks authorizing only this group become
// unreadable to everyone until re-authorized (their DEK was wrapped only under
// this KEK), so the caller confirms first.
func (s *Store) DeleteKeyGroup(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM key_groups WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete key group %d: %w", id, err)
	}
	return nil
}

// RotateKeyGroup replaces a group's KEK with fresh random material. The next build
// re-wraps every DEK authorized for the group; all outstanding gate links die and
// must be re-issued (SPEC §6.10). Returns the updated group.
func (s *Store) RotateKeyGroup(id int64) (KeyGroup, error) {
	kek, err := newKEK()
	if err != nil {
		return KeyGroup{}, err
	}
	if _, err := s.db.Exec(
		`UPDATE key_groups SET kek = ?, updated_at = ? WHERE id = ?`,
		base64.StdEncoding.EncodeToString(kek), s.now().Unix(), id); err != nil {
		return KeyGroup{}, fmt.Errorf("store: rotate key group %d: %w", id, err)
	}
	row := s.db.QueryRow(
		`SELECT id, alias, kek, splash_page_id, created_at, updated_at
		 FROM key_groups WHERE id = ?`, id)
	return scanKeyGroup(row)
}

// SetKeyGroupSplash associates (pageID non-nil) or clears (nil) the group's splash
// page — the authored welcome/landing page that deposits the KEK into the keyring.
func (s *Store) SetKeyGroupSplash(id int64, pageID *int64) error {
	_, err := s.db.Exec(
		`UPDATE key_groups SET splash_page_id = ?, updated_at = ? WHERE id = ?`,
		nullInt(pageID), s.now().Unix(), id)
	if err != nil {
		return fmt.Errorf("store: set key group %d splash: %w", id, err)
	}
	return nil
}

// SplashAliasForPage returns the alias of the key group whose splash page is the
// given page, if any — so the build can mark that page as a key-deposit point.
func (s *Store) SplashAliasForPage(pageID int64) (string, bool, error) {
	var alias string
	err := s.db.QueryRow(
		`SELECT alias FROM key_groups WHERE splash_page_id = ?`, pageID).Scan(&alias)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: splash alias for page %d: %w", pageID, err)
	}
	return alias, true, nil
}

// Favicon is one stored favicon/app-icon asset (SPEC §6.11): a canonical filename,
// its MIME type, and the cleaned bytes.
type Favicon struct {
	Name      string
	MIME      string
	Data      []byte
	UpdatedAt time.Time
}

// PutFavicon upserts a favicon asset under its canonical name (e.g. "favicon.svg").
func (s *Store) PutFavicon(name, mime string, data []byte) error {
	_, err := s.db.Exec(
		`INSERT INTO favicons (name, mime, data, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET mime = excluded.mime, data = excluded.data, updated_at = excluded.updated_at`,
		name, mime, data, s.now().Unix())
	if err != nil {
		return fmt.Errorf("store: put favicon %q: %w", name, err)
	}
	return nil
}

// Favicon returns the stored asset for name, and whether it was present.
func (s *Store) Favicon(name string) (Favicon, bool, error) {
	var f Favicon
	var updated int64
	err := s.db.QueryRow(`SELECT name, mime, data, updated_at FROM favicons WHERE name = ?`, name).
		Scan(&f.Name, &f.MIME, &f.Data, &updated)
	if err == sql.ErrNoRows {
		return Favicon{}, false, nil
	}
	if err != nil {
		return Favicon{}, false, fmt.Errorf("store: favicon %q: %w", name, err)
	}
	f.UpdatedAt = time.Unix(updated, 0)
	return f, true, nil
}

// FaviconNames returns the canonical names currently present, sorted — so the
// editor and build can see which slots are filled without loading the blobs.
func (s *Store) FaviconNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM favicons ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: favicon names: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteFavicon removes a favicon slot.
func (s *Store) DeleteFavicon(name string) error {
	if _, err := s.db.Exec(`DELETE FROM favicons WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: delete favicon %q: %w", name, err)
	}
	return nil
}

// mediaExt maps a stored media format to its file/URL extension.
func mediaExt(format string) string {
	switch format {
	case "mp4", "mov", "m4a", "mp3", "wav", "webm", "weba", "mkv", "mka", "oga", "ogv":
		return format
	default:
		return "bin"
	}
}

// SetSetting upserts a settings key/value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	return nil
}

// AllPaths returns every page's path — the set of live site paths, used to identify
// comments left orphaned by a deleted page (they are stored keyed by path in the runtime
// store, with no link to this content store).
func (s *Store) AllPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM pages`)
	if err != nil {
		return nil, fmt.Errorf("store: all paths: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Setting returns a settings value and whether it was present.
func (s *Store) Setting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// Runtime-store maintenance retention config (days; 0 disables that prune), persisted in
// the settings KV and edited in the admin Settings page. Read by both the Settings UI and
// the maintenance ticker.
const (
	KeyMaintInviteDays       = "maint.inviteRetentionDays"
	KeyMaintRejectedDays     = "maint.rejectedCommentRetentionDays"
	KeyMaintOrphanDays       = "maint.orphanCommentRetentionDays"
	KeyMaintVacuumDays       = "maint.vacuumIntervalDays"
	KeyMaintAliasReleaseDays = "maint.aliasReleaseInactiveDays"
	KeyMaintTombstoneDays    = "maint.tombstoneRetentionDays"
	keyMaintLastVacuum       = "maint.lastVacuumUnix"
)

// Baked-in defaults applied when a maintenance key is unset.
const (
	DefaultInviteRetentionDays    = 30
	DefaultRejectedRetentionDays  = 30
	DefaultOrphanRetentionDays    = 90
	DefaultVacuumIntervalDays     = 30
	DefaultAliasReleaseDays       = 90 // free a dormant member's display name after this long idle (§F3)
	DefaultTombstoneRetentionDays = 30 // reclaim a childless "[deleted]" root this long after deletion (§F4)
)

// MaintenanceDays is the operator's runtime-store retention config, in days. A zero value
// disables that particular prune (or, for VacuumDays, disables periodic vacuum).
type MaintenanceDays struct {
	InviteDays, RejectedDays, OrphanDays, VacuumDays int
	AliasReleaseDays, TombstoneDays                  int
}

// Maintenance reads the retention config, applying the baked-in defaults for unset or
// malformed keys.
func (s *Store) Maintenance() MaintenanceDays {
	return MaintenanceDays{
		InviteDays:       s.settingInt(KeyMaintInviteDays, DefaultInviteRetentionDays),
		RejectedDays:     s.settingInt(KeyMaintRejectedDays, DefaultRejectedRetentionDays),
		OrphanDays:       s.settingInt(KeyMaintOrphanDays, DefaultOrphanRetentionDays),
		VacuumDays:       s.settingInt(KeyMaintVacuumDays, DefaultVacuumIntervalDays),
		AliasReleaseDays: s.settingInt(KeyMaintAliasReleaseDays, DefaultAliasReleaseDays),
		TombstoneDays:    s.settingInt(KeyMaintTombstoneDays, DefaultTombstoneRetentionDays),
	}
}

// settingInt reads a non-negative integer setting, falling back to def when unset,
// malformed, or negative.
func (s *Store) settingInt(key string, def int) int {
	if v, ok, err := s.Setting(key); err == nil && ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// LastVacuum returns when the runtime store was last vacuumed (zero if never), tracked so
// the ticker vacuums on a schedule rather than every pass.
func (s *Store) LastVacuum() time.Time {
	if v, ok, err := s.Setting(keyMaintLastVacuum); err == nil && ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

// SetLastVacuum records the time of the most recent vacuum.
func (s *Store) SetLastVacuum(t time.Time) error {
	return s.SetSetting(keyMaintLastVacuum, strconv.FormatInt(t.Unix(), 10))
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func fromNull(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
