package creator

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/build"
)

// handleRelease packages a versioned release tarball of the built bundle (§7.4).
// Copy-to-host stays manual — the editor never touches production.
func (c *Creator) handleRelease(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	bc, report, tarPath, err := c.packageRelease()
	if err != nil {
		http.Error(w, "release: "+err.Error(), http.StatusInternalServerError)
		return
	}
	c.render(w, "build", map[string]any{
		"CSRF": c.csrf, "Report": report, "OutDir": c.cfg.OutDir,
		"Release": tarPath, "BuildNumber": bc.BuildNumber, "Version": bc.Version,
		"PageIDs": c.pageIDsByPath(), "Publisher": c.cfg.Publisher != nil,
	})
}

// handleSitePublish is the unified in-process Publish (§7.9): it builds a new
// versioned release directory, atomically repoints the `current` symlink to it, and
// reloads the public listener in-process — the site goes live with no restart. It is
// available only in a unified launch (a Publisher configured); standalone
// `pbcssg creator` has no running public site, so it directs the operator to Release.
func (c *Creator) handleSitePublish(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if c.cfg.Publisher == nil {
		http.Error(w, "publish needs a unified launch (pbcssg server -admin-addr); use Package release for a standalone editor", http.StatusBadRequest)
		return
	}
	bc, report, releaseDir, err := c.publishSite()
	if err != nil {
		http.Error(w, "publish: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"CSRF": c.csrf, "Report": report, "OutDir": releaseDir,
		"Published": releaseDir, "BuildNumber": bc.BuildNumber, "Version": bc.Version,
		"PageIDs": c.pageIDsByPath(), "Publisher": true,
	}
	// Prune old release directories per the Settings retention count — best-effort
	// cleanup after a successful publish; a failure here never fails the publish, and
	// the live release (what `current` points at) is always protected.
	root := c.cfg.ReleaseDir
	if root == "" {
		root = "releases"
	}
	if removed, err := pruneReleases(root, c.keepReleases(), c.cfg.ContentLink); err != nil {
		data["PruneWarn"] = err.Error()
	} else if len(removed) > 0 {
		data["Pruned"] = removed
	}
	c.render(w, "build", data)
}

// pruneReleases keeps the newest `keep` versioned release directories under root
// (those named v<version>-build<n>) and removes the rest, so the host doesn't
// accumulate old releases (§7.4). keep <= 0 means keep all (no pruning). The
// directory `current` (protectLink) resolves to is NEVER removed, whatever its age —
// the live release is protected even if the count would otherwise trim it. Only
// pbcssg's own release directories are considered; other files/dirs (tarballs, etc.)
// are left untouched.
func pruneReleases(root string, keep int, protectLink string) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// The live release: whatever the `current` symlink resolves to. Never delete it.
	var protect string
	if resolved, err := filepath.EvalSymlinks(protectLink); err == nil {
		protect = resolved
	}

	type rel struct {
		name string
		num  int
	}
	var rels []rel
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, ok := parseReleaseBuild(e.Name()); ok {
			rels = append(rels, rel{e.Name(), n})
		}
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i].num > rels[j].num }) // newest first

	var removed []string
	for i, r := range rels {
		if i < keep {
			continue
		}
		dir := filepath.Join(root, r.name)
		// Compare canonical paths (EvalSymlinks on both sides) so the guard holds even
		// when temp/mount symlinks differ (e.g. macOS /var -> /private/var).
		if protect != "" {
			if canon, err := filepath.EvalSymlinks(dir); err == nil && canon == protect {
				continue // the live release — protected regardless of age
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return removed, err
		}
		removed = append(removed, r.name)
	}
	return removed, nil
}

// parseReleaseBuild extracts the build number from a release directory name of the
// form v<version>-build<n> (as written by publishSite), so releases sort newest-first
// by that monotonic number. Names not in that form return ok=false and are ignored.
func parseReleaseBuild(name string) (int, bool) {
	const marker = "-build"
	if !strings.HasPrefix(name, "v") {
		return 0, false
	}
	i := strings.LastIndex(name, marker)
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(name[i+len(marker):])
	if err != nil {
		return 0, false
	}
	return n, true
}

// publishSite increments the build number, builds an immutable versioned release
// directory, atomically repoints the `current` symlink to it, and cuts the public
// listener over in-process. On any failure before the reload the current bundle keeps
// serving unchanged (a failed publish never goes live).
func (c *Creator) publishSite() (build.Config, *build.Report, string, error) {
	link := c.cfg.ContentLink
	if link == "" {
		return build.Config{}, nil, "", fmt.Errorf("no content symlink configured")
	}
	// The public listener must serve through a `current`-style symlink for an
	// in-process publish to repoint it (§7.4/§7.9); refuse to clobber a real directory.
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		return build.Config{}, nil, "", fmt.Errorf("content path %q is not a symlink; unified publish needs -content to be a `current` symlink", link)
	}

	bc := c.buildConfig()
	bc.BuildNumber = incBuildNumber(bc.BuildNumber) // §Build Numbering: the third component

	releaseRoot := c.cfg.ReleaseDir
	if releaseRoot == "" {
		releaseRoot = "releases"
	}
	releaseDir, err := filepath.Abs(filepath.Join(releaseRoot, fmt.Sprintf("v%s-build%s", safeSeg(bc.Version), safeSeg(bc.BuildNumber))))
	if err != nil {
		return bc, nil, "", err
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return bc, nil, releaseDir, err
	}
	report, err := build.Run(c.store, bc, releaseDir)
	if err != nil {
		return bc, nil, releaseDir, err
	}

	// Atomically repoint `current` → the new release, then reload the public listener,
	// which re-opens its configured content path (the symlink) and swaps in-process.
	if err := repointSymlink(link, releaseDir); err != nil {
		return bc, report, releaseDir, fmt.Errorf("repoint %s: %w", link, err)
	}
	if err := c.cfg.Publisher.Reload(""); err != nil {
		return bc, report, releaseDir, fmt.Errorf("reload public listener: %w", err)
	}

	// Persist + apply the incremented build number for the next publish.
	if err := c.applyConfig(bc); err != nil {
		return bc, report, releaseDir, err
	}
	if err := c.saveBuildConfig(bc); err != nil {
		return bc, report, releaseDir, err
	}
	return bc, report, releaseDir, nil
}

// repointSymlink atomically points link at target: create a uniquely-named sibling
// symlink then rename it over link (rename is atomic on the same filesystem, so a
// concurrent reader never sees a missing link).
func repointSymlink(link, target string) error {
	tmp := link + ".tmp-" + randToken()
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// packageRelease auto-increments the build number, builds the bundle, and writes
// a versioned .tar.gz of it to the release directory, then persists the new
// build number so the next release continues from it.
func (c *Creator) packageRelease() (build.Config, *build.Report, string, error) {
	bc := c.buildConfig()
	bc.BuildNumber = incBuildNumber(bc.BuildNumber) // §Build Numbering: the third component

	report, err := build.Run(c.store, bc, c.cfg.OutDir)
	if err != nil {
		return bc, nil, "", err
	}

	releaseDir := c.cfg.ReleaseDir
	if releaseDir == "" {
		releaseDir = "releases"
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return bc, report, "", err
	}
	tarPath := filepath.Join(releaseDir, fmt.Sprintf("pbcssg-v%s-build%s.tar.gz", safeSeg(bc.Version), safeSeg(bc.BuildNumber)))
	if err := tarGz(c.cfg.OutDir, tarPath); err != nil {
		return bc, report, "", err
	}

	// Persist + apply the incremented build number.
	if err := c.applyConfig(bc); err != nil {
		return bc, report, tarPath, err
	}
	if err := c.saveBuildConfig(bc); err != nil {
		return bc, report, tarPath, err
	}
	return bc, report, tarPath, nil
}

// incBuildNumber returns the next build number; a non-numeric current value
// restarts at 1.
func incBuildNumber(s string) string {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return "1"
	}
	return strconv.Itoa(n + 1)
}

// safeSeg keeps a filesystem-safe path segment for the tarball name.
func safeSeg(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}

// tarGz writes a deterministic gzipped tarball of srcDir's contents to dst
// (forward-slash relative paths, sorted, no modtimes → reproducible).
func tarGz(srcDir, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	var files []string
	err = filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, p := range files {
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: filepath.ToSlash(rel),
			Mode: 0o644,
			Size: int64(len(data)),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}
