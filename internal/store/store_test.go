package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNormalizeAlias(t *testing.T) {
	cases := map[string]string{
		"Members A":   "members-a",
		"  members_a": "members-a",
		"Members--A":  "members-a",
		"UPPER":       "upper",
		"a b  c":      "a-b-c",
		"trailing-":   "trailing",
		"!!!":         "",
		"":            "",
		"pátio":       "ptio", // non-ASCII stripped (aliases are slugs)
	}
	for in, want := range cases {
		if got := NormalizeAlias(in); got != want {
			t.Errorf("NormalizeAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeyGroupsCRUD(t *testing.T) {
	s := openTemp(t)

	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatalf("CreateKeyGroup: %v", err)
	}
	if len(g.KEK) != KEKLen {
		t.Fatalf("KEK length = %d, want %d", len(g.KEK), KEKLen)
	}

	// Alias must be unique.
	if _, err := s.CreateKeyGroup("members-a"); err == nil {
		t.Error("duplicate alias should be rejected")
	}

	// Lookup by alias returns the same KEK; a miss reports ok=false.
	got, ok, err := s.KeyGroup("members-a")
	if err != nil || !ok {
		t.Fatalf("KeyGroup lookup: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got.KEK, g.KEK) {
		t.Error("KeyGroup returned a different KEK than CreateKeyGroup")
	}
	if _, ok, _ := s.KeyGroup("nope"); ok {
		t.Error("unknown alias should report ok=false")
	}

	// KEKsByAlias maps every group.
	s.CreateKeyGroup("members-b")
	m, err := s.KEKsByAlias()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || !bytes.Equal(m["members-a"], g.KEK) {
		t.Errorf("KEKsByAlias wrong: %d entries", len(m))
	}

	// Rotate replaces the KEK but keeps the alias.
	rot, err := s.RotateKeyGroup(g.ID)
	if err != nil {
		t.Fatalf("RotateKeyGroup: %v", err)
	}
	if bytes.Equal(rot.KEK, g.KEK) {
		t.Error("rotation did not change the KEK")
	}
	if rot.Alias != "members-a" {
		t.Errorf("rotation changed the alias to %q", rot.Alias)
	}

	// Rename changes the alias.
	if err := s.RenameKeyGroup(g.ID, "members-a2"); err != nil {
		t.Fatalf("RenameKeyGroup: %v", err)
	}
	if _, ok, _ := s.KeyGroup("members-a2"); !ok {
		t.Error("renamed group not found under new alias")
	}

	// Delete removes it.
	if err := s.DeleteKeyGroup(g.ID); err != nil {
		t.Fatalf("DeleteKeyGroup: %v", err)
	}
	if _, ok, _ := s.KeyGroup("members-a2"); ok {
		t.Error("deleted group still present")
	}
}

func TestKeyGroupSplashAssociation(t *testing.T) {
	s := openTemp(t)
	pid, err := s.CreatePage(Page{Path: "/members-a", Slug: "members-a", Title: "Members A"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateKeyGroup("members-a")
	if err != nil {
		t.Fatal(err)
	}

	// No splash by default; SplashAliasForPage finds nothing.
	if _, ok, _ := s.SplashAliasForPage(pid); ok {
		t.Error("page should not be a splash before association")
	}

	// Associate, then the reverse lookup resolves the alias.
	if err := s.SetKeyGroupSplash(g.ID, &pid); err != nil {
		t.Fatalf("SetKeyGroupSplash: %v", err)
	}
	alias, ok, err := s.SplashAliasForPage(pid)
	if err != nil || !ok || alias != "members-a" {
		t.Fatalf("SplashAliasForPage = %q ok=%v err=%v", alias, ok, err)
	}

	// Deleting the splash page nulls the association (ON DELETE SET NULL), leaving
	// the group intact so it falls back to the generic confirmation page.
	if err := s.DeletePage(pid); err != nil {
		t.Fatal(err)
	}
	after, ok, err := s.KeyGroup("members-a")
	if err != nil || !ok {
		t.Fatalf("group vanished with its splash page: ok=%v err=%v", ok, err)
	}
	if after.SplashPageID != nil {
		t.Errorf("splash association survived page delete: %v", *after.SplashPageID)
	}

	// Clearing an association with a nil page id also works.
	s.SetKeyGroupSplash(g.ID, nil)
	if g2, _, _ := s.KeyGroup("members-a"); g2.SplashPageID != nil {
		t.Error("SetKeyGroupSplash(nil) did not clear the association")
	}
}

func TestMediaTagsCRUD(t *testing.T) {
	s := openTemp(t)
	// A monotonic clock so img2 is stored strictly after img1 (for the newest-first
	// by-tag order assertion below).
	var tick int64
	s.now = func() time.Time { tick++; return time.Unix(1_700_000_000+tick, 0) }
	put := func(sha, name string) {
		if err := s.PutAsset(AssetData{Asset: Asset{SHA256: sha, Filename: name, Format: "png", MIME: "image/png"}, Data: []byte(sha)}); err != nil {
			t.Fatal(err)
		}
	}
	put("img1", "a.png")
	put("img2", "b.png")

	// Set tags with mixed case, whitespace, blanks, and a duplicate → normalized set.
	if err := s.SetMediaTags("img1", []string{" Nature ", "nature", "  ", "Sunset"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaTags("img2", []string{"Sunset", "city"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.MediaTags("img1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "nature,sunset" { // normalized, deduped, sorted
		t.Errorf("img1 tags = %v, want [nature sunset]", got)
	}

	// AssetsByTag returns matching images newest-first, normalized query.
	byTag, err := s.AssetsByTag("  SUNSET ")
	if err != nil {
		t.Fatal(err)
	}
	if len(byTag) != 2 {
		t.Fatalf("expected 2 images tagged sunset, got %d", len(byTag))
	}
	// img2 is newer (inserted second), so it comes first.
	if byTag[0].SHA256 != "img2" || byTag[1].SHA256 != "img1" {
		t.Errorf("by-tag order wrong: %s, %s", byTag[0].SHA256, byTag[1].SHA256)
	}

	// MediaTagsFor annotates several items at once.
	m, err := s.MediaTagsFor([]string{"img1", "img2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m["img2"], ",") != "city,sunset" {
		t.Errorf("img2 tags = %v", m["img2"])
	}

	// Re-setting replaces the whole set; empty clears it.
	if err := s.SetMediaTags("img1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MediaTags("img1"); len(got) != 0 {
		t.Errorf("img1 tags should be cleared, got %v", got)
	}

	// Deleting the asset removes its tags (no dangling rows).
	if err := s.DeleteAsset("img2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MediaTags("img2"); len(got) != 0 {
		t.Errorf("deleted asset's tags should be gone, got %v", got)
	}
	if byTag, _ := s.AssetsByTag("sunset"); len(byTag) != 0 {
		t.Errorf("no images should remain tagged sunset, got %d", len(byTag))
	}
}

func TestFaviconsCRUD(t *testing.T) {
	s := openTemp(t)

	if names, _ := s.FaviconNames(); len(names) != 0 {
		t.Fatalf("expected no favicons, got %v", names)
	}
	if err := s.PutFavicon("favicon.svg", "image/svg+xml", []byte("<svg/>")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFavicon("favicon.ico", "image/x-icon", []byte("ico")); err != nil {
		t.Fatal(err)
	}

	f, ok, err := s.Favicon("favicon.svg")
	if err != nil || !ok || f.MIME != "image/svg+xml" || !bytes.Equal(f.Data, []byte("<svg/>")) {
		t.Fatalf("Favicon get wrong: %+v ok=%v err=%v", f, ok, err)
	}
	if _, ok, _ := s.Favicon("missing.png"); ok {
		t.Error("missing favicon should report ok=false")
	}

	// Upsert replaces the bytes.
	if err := s.PutFavicon("favicon.svg", "image/svg+xml", []byte("<svg x/>")); err != nil {
		t.Fatal(err)
	}
	if f, _, _ := s.Favicon("favicon.svg"); !bytes.Equal(f.Data, []byte("<svg x/>")) {
		t.Errorf("upsert did not replace bytes: %q", f.Data)
	}

	if names, _ := s.FaviconNames(); len(names) != 2 || names[0] != "favicon.ico" || names[1] != "favicon.svg" {
		t.Errorf("FaviconNames = %v, want sorted [favicon.ico favicon.svg]", names)
	}
	if err := s.DeleteFavicon("favicon.svg"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Favicon("favicon.svg"); ok {
		t.Error("favicon.svg not deleted")
	}
}

func TestPageKeyAndRekey(t *testing.T) {
	s := openTemp(t)
	pid, err := s.CreatePage(Page{Path: "/p", Slug: "p", Title: "P"})
	if err != nil {
		t.Fatal(err)
	}

	// First access generates a key of the right length.
	k1, err := s.PageKey(pid)
	if err != nil {
		t.Fatalf("PageKey: %v", err)
	}
	if len(k1) != RevealKeyLen {
		t.Fatalf("key length = %d, want %d", len(k1), RevealKeyLen)
	}

	// Idempotent: the same key comes back on the next call (build reproducibility).
	k2, err := s.PageKey(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1, k2) {
		t.Error("PageKey not stable across calls")
	}

	// Rekey rotates it.
	k3, err := s.RekeyPage(pid)
	if err != nil {
		t.Fatalf("RekeyPage: %v", err)
	}
	if bytes.Equal(k1, k3) {
		t.Error("RekeyPage returned the same key")
	}
	if got, _ := s.PageKey(pid); !bytes.Equal(got, k3) {
		t.Error("PageKey did not return the rekeyed value")
	}

	// Deleting the page cascades away its key.
	if err := s.DeletePage(pid); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM page_keys WHERE page_id = ?`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("page_keys row survived page delete: count=%d", n)
	}
}

func TestCreateSavePublishAndQuery(t *testing.T) {
	s := openTemp(t)

	pid, err := s.CreatePage(Page{Path: "/blog/post", Slug: "post", Title: "A Post"})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	rid, err := s.SaveRevision(pid, `{"body":"hello"}`, "editor")
	if err != nil {
		t.Fatalf("save revision: %v", err)
	}

	// Before publishing, nothing is published.
	if pubs, err := s.Published(); err != nil || len(pubs) != 0 {
		t.Fatalf("expected 0 published, got %d (err=%v)", len(pubs), err)
	}

	if err := s.Publish(pid, rid); err != nil {
		t.Fatalf("publish: %v", err)
	}

	pubs, err := s.Published()
	if err != nil {
		t.Fatalf("published: %v", err)
	}
	if len(pubs) != 1 {
		t.Fatalf("expected 1 published, got %d", len(pubs))
	}
	p := pubs[0]
	if p.Path != "/blog/post" || p.Title != "A Post" || p.Status != StatusPublished {
		t.Errorf("unexpected page: %+v", p)
	}
	if p.ContentJSON != `{"body":"hello"}` {
		t.Errorf("content = %q", p.ContentJSON)
	}
	if p.LiveRevisionID == nil || *p.LiveRevisionID != rid {
		t.Errorf("live revision id = %v, want %d", p.LiveRevisionID, rid)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Errorf("timestamps should be set: %+v", p)
	}
}

func TestPublishOtherPagesRevisionRejected(t *testing.T) {
	s := openTemp(t)
	p1, _ := s.CreatePage(Page{Path: "/a", Slug: "a", Title: "A"})
	p2, _ := s.CreatePage(Page{Path: "/b", Slug: "b", Title: "B"})
	r2, _ := s.SaveRevision(p2, `{}`, "")

	if err := s.Publish(p1, r2); err == nil {
		t.Errorf("publishing page 2's revision under page 1 should fail")
	}
}

func TestDuplicatePathRejected(t *testing.T) {
	s := openTemp(t)
	if _, err := s.CreatePage(Page{Path: "/dup", Slug: "dup", Title: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePage(Page{Path: "/dup", Slug: "dup", Title: "Two"}); err == nil {
		t.Errorf("duplicate path should be rejected by UNIQUE constraint")
	}
}

func TestRevisionRoundTrip(t *testing.T) {
	s := openTemp(t)
	pid, _ := s.CreatePage(Page{Path: "/p", Slug: "p", Title: "P"})
	rid, _ := s.SaveRevision(pid, `{"k":1}`, "author")

	r, err := s.Revision(rid)
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	if r.PageID != pid || r.ContentJSON != `{"k":1}` || r.Author != "author" || r.IsPublished {
		t.Errorf("unexpected revision: %+v", r)
	}

	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	r2, _ := s.Revision(rid)
	if !r2.IsPublished {
		t.Errorf("revision should be marked published after Publish")
	}
}

func TestEditorCRUD(t *testing.T) {
	s := openTemp(t)

	// A published page and a draft-only page.
	p1, _ := s.CreatePage(Page{Path: "/b", Slug: "b", Title: "B"})
	r1, _ := s.SaveRevision(p1, `{"body":"one"}`, "j")
	s.SaveRevision(p1, `{"body":"two"}`, "j") // newer draft
	if err := s.Publish(p1, r1); err != nil {
		t.Fatal(err)
	}
	p2, _ := s.CreatePage(Page{Path: "/a", Slug: "a", Title: "A"})
	s.SaveRevision(p2, `{"body":"draft"}`, "j")

	// Pages(): both, ordered by path.
	pages, err := s.Pages()
	if err != nil || len(pages) != 2 {
		t.Fatalf("Pages() = %d (err=%v), want 2", len(pages), err)
	}
	if pages[0].Path != "/a" || pages[1].Path != "/b" {
		t.Errorf("pages not ordered by path: %v, %v", pages[0].Path, pages[1].Path)
	}

	// Page(id).
	got, err := s.Page(p1)
	if err != nil || got.Title != "B" || got.Status != StatusPublished {
		t.Errorf("Page(%d) = %+v (err=%v)", p1, got, err)
	}

	// LatestRevision returns the newest revision (the "two" draft, not the published "one").
	rev, ok, err := s.LatestRevision(p1)
	if err != nil || !ok || rev.ContentJSON != `{"body":"two"}` {
		t.Errorf("LatestRevision = %q ok=%v err=%v, want the newest draft", rev.ContentJSON, ok, err)
	}
	// A page with no revisions.
	p3, _ := s.CreatePage(Page{Path: "/c", Slug: "c", Title: "C"})
	if _, ok, _ := s.LatestRevision(p3); ok {
		t.Errorf("LatestRevision for a page with no revisions should be ok=false")
	}

	// UpdatePage.
	got.Title = "B renamed"
	got.Path = "/b2"
	if err := s.UpdatePage(got); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.Page(p1); after.Title != "B renamed" || after.Path != "/b2" {
		t.Errorf("UpdatePage did not persist: %+v", after)
	}

	// DeletePage cascades revisions.
	if err := s.DeletePage(p1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Page(p1); err == nil {
		t.Errorf("deleted page should not be found")
	}
	if _, err := s.Revision(r1); err == nil {
		t.Errorf("revisions of a deleted page should be gone (cascade)")
	}
}

func TestUnpublish(t *testing.T) {
	s := openTemp(t)
	pid, _ := s.CreatePage(Page{Path: "/p", Slug: "p", Title: "P"})
	rid, _ := s.SaveRevision(pid, `{"body":"hi"}`, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	if pubs, _ := s.Published(); len(pubs) != 1 {
		t.Fatalf("want 1 published, got %d", len(pubs))
	}

	if err := s.Unpublish(pid); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if pubs, _ := s.Published(); len(pubs) != 0 {
		t.Errorf("unpublished page should drop out of the build, got %d published", len(pubs))
	}
	p, _ := s.Page(pid)
	if p.Status != StatusDraft || p.LiveRevisionID != nil {
		t.Errorf("page should be a draft with no live revision: status=%s live=%v", p.Status, p.LiveRevisionID)
	}
	// The working content is preserved.
	if rev, ok, _ := s.LatestRevision(pid); !ok || rev.ContentJSON != `{"body":"hi"}` {
		t.Errorf("latest revision should survive unpublish")
	}
}

func TestAssets(t *testing.T) {
	s := openTemp(t)

	if as, err := s.Assets(); err != nil || len(as) != 0 {
		t.Fatalf("empty library: got %d (err=%v)", len(as), err)
	}
	if _, err := s.Asset("deadbeef"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("missing asset should be ErrNoRows, got %v", err)
	}

	data := []byte("\xff\xd8\xffclean-jpeg-bytes")
	a := AssetData{Asset: Asset{SHA256: "abc123", Filename: "photo.jpg", Format: "jpeg", MIME: "image/jpeg"}, Data: data}
	if err := s.PutAsset(a); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Content-addressed + idempotent: same hash, different filename is a no-op.
	if err := s.PutAsset(AssetData{Asset: Asset{SHA256: "abc123", Filename: "other.jpg", Format: "jpeg", MIME: "image/jpeg"}, Data: data}); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	as, err := s.Assets()
	if err != nil || len(as) != 1 {
		t.Fatalf("library should have 1 row, got %d (err=%v)", len(as), err)
	}
	if as[0].Filename != "photo.jpg" || as[0].Size != int64(len(data)) {
		t.Errorf("metadata wrong: %+v (first filename should win; size from bytes)", as[0])
	}

	got, err := s.Asset("abc123")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got.Data, data) {
		t.Errorf("round-trip bytes mismatch")
	}

	if err := s.DeleteAsset("abc123"); err != nil {
		t.Fatal(err)
	}
	if as, _ := s.Assets(); len(as) != 0 {
		t.Errorf("after delete, library should be empty, got %d", len(as))
	}
}

func TestSettings(t *testing.T) {
	s := openTemp(t)

	if _, ok, err := s.Setting("missing"); err != nil || ok {
		t.Errorf("missing setting should be ok=false (err=%v)", err)
	}
	if err := s.SetSetting("siteName", "The Untracked Life"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("siteName", "PBC"); err != nil { // upsert
		t.Fatal(err)
	}
	v, ok, err := s.Setting("siteName")
	if err != nil || !ok || v != "PBC" {
		t.Errorf("setting = %q, ok=%v, err=%v; want PBC/true/nil", v, ok, err)
	}
}

func TestMediaFilesystemRoundTrip(t *testing.T) {
	s := openTemp(t)
	data := []byte("FAKE-MP4-BYTES-\x00\x01\x02")
	m := MediaFile{
		SHA256:   "abc123",
		Filename: "clip.mp4",
		Format:   "mp4",
		MIME:     "video/mp4",
		Kind:     "video",
	}
	if err := s.PutMedia(m, data); err != nil {
		t.Fatalf("PutMedia: %v", err)
	}

	// The bytes live on disk under the media root, not in the DB.
	full := filepath.Join(s.mediaRoot, "abc123.mp4")
	if b, err := os.ReadFile(full); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("media file not written correctly: err=%v", err)
	}

	got, err := s.Media("abc123")
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if got.Kind != "video" || got.Format != "mp4" || got.Size != int64(len(data)) {
		t.Errorf("metadata wrong: %+v", got)
	}

	if list, _ := s.MediaList(); len(list) != 1 {
		t.Errorf("MediaList len = %d, want 1", len(list))
	}

	rb, _, err := s.ReadMedia("abc123")
	if err != nil || !bytes.Equal(rb, data) {
		t.Errorf("ReadMedia mismatch: err=%v", err)
	}

	// Delete removes both the row and the on-disk file.
	if err := s.DeleteMedia("abc123"); err != nil {
		t.Fatalf("DeleteMedia: %v", err)
	}
	if _, err := s.Media("abc123"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("row should be gone, got %v", err)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Errorf("media file should be deleted")
	}
}

func TestMediaRejectedWithoutRoot(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PutMedia(MediaFile{SHA256: "x", Format: "mp3", Kind: "audio"}, []byte("a")); err == nil {
		t.Errorf("in-memory DB should reject filesystem-backed media")
	}
}

func TestAssetPageAndCount(t *testing.T) {
	s := openTemp(t)
	put := func(sha, name string) {
		if err := s.PutAsset(AssetData{Asset: Asset{SHA256: sha, Filename: name, Format: "png", MIME: "image/png"}, Data: []byte(sha)}); err != nil {
			t.Fatal(err)
		}
	}
	put("a1", "cat.png")
	put("a2", "cat-two.png")
	put("a3", "dog.png")

	if n, _ := s.CountAssets(); n != 3 {
		t.Errorf("CountAssets = %d, want 3", n)
	}
	// Filename search is a case-insensitive contains match.
	rows, total, err := s.AssetPage("cat", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 {
		t.Errorf("search 'cat' total=%d rows=%d, want 2/2", total, len(rows))
	}
	// Pagination: page size 2 over 3 rows.
	p1, total, _ := s.AssetPage("", 2, 0)
	p2, _, _ := s.AssetPage("", 2, 2)
	if total != 3 || len(p1) != 2 || len(p2) != 1 {
		t.Errorf("paging: total=%d p1=%d p2=%d, want 3/2/1", total, len(p1), len(p2))
	}

	// Search also matches the admin note, not just the filename.
	if err := s.SetMediaNote("a3", "privacy hero banner"); err != nil {
		t.Fatal(err)
	}
	if rows, total, _ := s.AssetPage("hero", 10, 0); total != 1 || len(rows) != 1 || rows[0].SHA256 != "a3" {
		t.Errorf("note search 'hero' = %+v (total %d), want the dog row", rows, total)
	}
	// A row with no note still matches by filename.
	if _, total, _ := s.AssetPage("dog", 10, 0); total != 1 {
		t.Errorf("filename search 'dog' total=%d, want 1", total)
	}
	// The LEFT JOIN must not inflate the unfiltered count.
	if _, total, _ := s.AssetPage("", 10, 0); total != 3 {
		t.Errorf("blank search total=%d, want 3", total)
	}
}

func TestMediaPageAndCount(t *testing.T) {
	s := openTemp(t)
	put := func(sha, name, kind, format string) {
		if err := s.PutMedia(MediaFile{SHA256: sha, Filename: name, Format: format, MIME: "x", Kind: kind}, []byte(sha)); err != nil {
			t.Fatal(err)
		}
	}
	put("v1", "talk.mp4", "video", "mp4")
	put("v2", "talk-two.mp4", "video", "mp4")
	put("a1", "song.mp3", "audio", "mp3")

	if n, _ := s.CountMedia("video"); n != 2 {
		t.Errorf("CountMedia(video) = %d, want 2", n)
	}
	if n, _ := s.CountMedia("audio"); n != 1 {
		t.Errorf("CountMedia(audio) = %d, want 1", n)
	}
	// A search within a kind, and cross-kind isolation.
	rows, total, _ := s.MediaPage("video", "two", 10, 0)
	if total != 1 || len(rows) != 1 || rows[0].SHA256 != "v2" {
		t.Errorf("video search 'two' = %+v (total %d)", rows, total)
	}
	if _, total, _ := s.MediaPage("audio", "talk", 10, 0); total != 0 {
		t.Errorf("audio search 'talk' should match nothing, got %d", total)
	}

	// Search matches the admin note within a kind.
	if err := s.SetMediaNote("a1", "intro theme music"); err != nil {
		t.Fatal(err)
	}
	if rows, total, _ := s.MediaPage("audio", "theme", 10, 0); total != 1 || len(rows) != 1 || rows[0].SHA256 != "a1" {
		t.Errorf("audio note search 'theme' = %+v (total %d)", rows, total)
	}
	// The kind filter still holds: a video's note isn't matched under the audio tab.
	if err := s.SetMediaNote("v1", "audio-heavy talk"); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.MediaPage("audio", "audio-heavy", 10, 0); total != 0 {
		t.Errorf("cross-kind note must not match, got %d", total)
	}
}

func TestMediaExists(t *testing.T) {
	s := openTemp(t)
	imgSHA := strings.Repeat("a", 64)
	vidSHA := strings.Repeat("b", 64)

	// Unknown addresses do not exist (and it is not an error).
	if ok, err := s.MediaExists(imgSHA); err != nil || ok {
		t.Fatalf("unknown image: ok=%v err=%v, want false/nil", ok, err)
	}

	// An image (BLOB store) exists.
	if err := s.PutAsset(AssetData{Asset: Asset{SHA256: imgSHA, Filename: "a.png", Format: "png", MIME: "image/png"}, Data: []byte("img")}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MediaExists(imgSHA); err != nil || !ok {
		t.Errorf("image should exist: ok=%v err=%v", ok, err)
	}

	// A filesystem-backed audio/video file exists too.
	if err := s.PutMedia(MediaFile{SHA256: vidSHA, Filename: "c.mp4", Format: "mp4", MIME: "video/mp4", Kind: "video"}, []byte("vid")); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MediaExists(vidSHA); err != nil || !ok {
		t.Errorf("media file should exist: ok=%v err=%v", ok, err)
	}

	// After deletion it no longer exists.
	if err := s.DeleteMedia(vidSHA); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.MediaExists(vidSHA); ok {
		t.Errorf("deleted media should not exist")
	}
}

func TestMediaNotes(t *testing.T) {
	s := openTemp(t)

	img := AssetData{Asset: Asset{SHA256: "img1", Filename: "hero.png", Format: "png", MIME: "image/png"}, Data: []byte("i")}
	if err := s.PutAsset(img); err != nil {
		t.Fatalf("PutAsset: %v", err)
	}
	aud := MediaFile{SHA256: "aud1", Filename: "clip.mp3", Format: "mp3", MIME: "audio/mpeg", Kind: "audio"}
	if err := s.PutMedia(aud, []byte("a")); err != nil {
		t.Fatalf("PutMedia: %v", err)
	}

	// Absent by default.
	if n, err := s.MediaNote("img1"); err != nil || n != "" {
		t.Fatalf("default note: got %q, err %v", n, err)
	}

	// Set, then update (upsert).
	if err := s.SetMediaNote("img1", "hero image for the privacy page"); err != nil {
		t.Fatalf("set note: %v", err)
	}
	if n, _ := s.MediaNote("img1"); n != "hero image for the privacy page" {
		t.Errorf("note after set = %q", n)
	}
	if err := s.SetMediaNote("img1", "banner"); err != nil {
		t.Fatalf("update note: %v", err)
	}
	if n, _ := s.MediaNote("img1"); n != "banner" {
		t.Errorf("note after update = %q", n)
	}

	// Notes work for audio/video too, and batch fetch returns only what is set.
	if err := s.SetMediaNote("aud1", "theme music"); err != nil {
		t.Fatalf("set audio note: %v", err)
	}
	notes, err := s.MediaNotesFor([]string{"img1", "aud1", "missing"})
	if err != nil {
		t.Fatalf("MediaNotesFor: %v", err)
	}
	if notes["img1"] != "banner" || notes["aud1"] != "theme music" {
		t.Errorf("batch notes wrong: %v", notes)
	}
	if _, ok := notes["missing"]; ok {
		t.Errorf("unset address should be absent from the map")
	}

	// An empty note clears the row.
	if err := s.SetMediaNote("img1", ""); err != nil {
		t.Fatalf("clear note: %v", err)
	}
	if n, _ := s.MediaNote("img1"); n != "" {
		t.Errorf("note after clear = %q", n)
	}

	// Deleting the item removes its note (no orphan).
	if err := s.SetMediaNote("aud1", "keep for now"); err != nil {
		t.Fatalf("re-set audio note: %v", err)
	}
	if err := s.DeleteMedia("aud1"); err != nil {
		t.Fatalf("DeleteMedia: %v", err)
	}
	if n, _ := s.MediaNote("aud1"); n != "" {
		t.Errorf("note survived item deletion: %q", n)
	}
}

func TestMaintenanceSettings(t *testing.T) {
	s := openTemp(t)
	// Unset keys fall back to the baked-in defaults.
	m := s.Maintenance()
	if m.InviteDays != DefaultInviteRetentionDays || m.RejectedDays != DefaultRejectedRetentionDays ||
		m.OrphanDays != DefaultOrphanRetentionDays || m.VacuumDays != DefaultVacuumIntervalDays {
		t.Fatalf("defaults = %+v, want 30/30/90/30", m)
	}
	// Explicit values (including 0 = disabled) round-trip.
	s.SetSetting(KeyMaintInviteDays, "7")
	s.SetSetting(KeyMaintOrphanDays, "0")
	m = s.Maintenance()
	if m.InviteDays != 7 || m.OrphanDays != 0 {
		t.Errorf("override = %+v, want InviteDays 7 / OrphanDays 0", m)
	}
	// A malformed value falls back to the default rather than breaking the ticker.
	s.SetSetting(KeyMaintRejectedDays, "not-a-number")
	if s.Maintenance().RejectedDays != DefaultRejectedRetentionDays {
		t.Error("malformed retention should fall back to the default")
	}
	// Last-vacuum time round-trips.
	when := time.Unix(1_700_000_000, 0)
	if err := s.SetLastVacuum(when); err != nil {
		t.Fatal(err)
	}
	if got := s.LastVacuum(); !got.Equal(when) {
		t.Errorf("LastVacuum = %v, want %v", got, when)
	}
}

func TestAllPaths(t *testing.T) {
	s := openTemp(t)
	if _, err := s.CreatePage(Page{Path: "/a", Slug: "a", Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePage(Page{Path: "/b", Slug: "b", Title: "B"}); err != nil {
		t.Fatal(err)
	}
	paths, err := s.AllPaths()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	if !got["/a"] || !got["/b"] || len(got) != 2 {
		t.Errorf("AllPaths = %v, want /a and /b", paths)
	}
}
