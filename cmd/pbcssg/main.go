// Command pbcssg is the pbcssg CLI. v1 provides two subcommands:
//
//	pbcssg build  -db content.db -out site/ -base https://example.com -build 1
//	pbcssg server -content site/ -addr 127.0.0.1:8083
//
// The creator (editor) subcommand is a later addition. `server` binds loopback
// by default and expects a TLS-terminating reverse proxy in front (SPEC §7.5).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/creator"
	"go.privatebychoice.com/pbcssg/internal/metrics"
	"go.privatebychoice.com/pbcssg/internal/publicapi"
	"go.privatebychoice.com/pbcssg/internal/server"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "build":
		cmdBuild(os.Args[2:])
	case "server":
		cmdServer(os.Args[2:])
	case "creator":
		cmdCreator(os.Args[2:])
	case "admin":
		cmdAdmin(os.Args[2:])
	case "version", "-v", "--version":
		cmdVersion()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "pbcssg: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	db := fs.String("db", "", "path to the content database (required)")
	out := fs.String("out", "", "output bundle directory (required)")
	base := fs.String("base", "", "site base URL, e.g. https://example.com (required)")
	site := fs.String("site", "", "site name (shown in title/footer)")
	version := fs.String("version", "1.0", "semantic version")
	buildNo := fs.String("build", "", "build number (incremented per deploy)")
	gpc := fs.String("gpc", "", "GPC lastUpdate date (YYYY-MM-DD)")
	searchOn := fs.Bool("search", false, "emit the client-side search index + widget")
	searchFull := fs.Bool("search-fulltext", false, "index full body text (default: headings + summary)")
	openGraph := fs.Bool("opengraph", true, "emit Open Graph tags on pages")
	fs.Parse(args)

	if *db == "" || *out == "" || *base == "" {
		log.Fatal("pbcssg build: -db, -out and -base are required")
	}
	if err := build.ValidateGPCDate(*gpc); err != nil {
		log.Fatalf("pbcssg build: -gpc: %v", err)
	}
	s, err := store.Open(*db)
	if err != nil {
		log.Fatalf("pbcssg build: %v", err)
	}
	defer s.Close()

	// Overlay settings saved in the editor onto the CLI flags (which act as
	// seed/first-run defaults), so a headless build matches the editor's Build
	// button — same embed-host allowlist, first-party domains, theme override, and
	// SEO/search toggles.
	cfg := creator.LoadBuildConfig(s, build.Config{
		SiteName: *site, BaseURL: *base, Version: *version,
		BuildNumber: *buildNo, GPCLastUpdate: *gpc,
		Search: *searchOn, SearchFullText: *searchFull, OpenGraph: *openGraph,
	})
	cfg.Year = time.Now().Year() // stamp the copyright year (kept out of the build pkg for determinism)
	rep, err := build.Run(s, cfg, *out)
	if err != nil {
		log.Fatalf("pbcssg build: %v", err)
	}

	log.Printf("INFO built %d page(s), %d file(s) -> %s", len(rep.Pages), len(rep.Files), *out)
	// Build-wide warnings (e.g. broken media references, skipped feeds) — these are
	// not tied to a single page, so print them before the per-page detail.
	for _, w := range rep.Warnings {
		log.Printf("WARN %s", w)
	}
	for _, p := range rep.Pages {
		if len(p.Warnings) > 0 || p.WorstGrade == "?" || p.WorstGrade == "D" || p.WorstGrade == "F" {
			log.Printf("WARN %s worst=%s external=%d warnings=%v", p.Path, p.WorstGrade, p.Externals, p.Warnings)
		}
	}
}

func cmdServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	content := fs.String("content", "", "path to a built bundle (required)")
	addr := fs.String("addr", "127.0.0.1:8083", "public listen address (bind loopback; put a TLS reverse proxy in front)")
	adminAddr := fs.String("admin-addr", "",
		"loopback address for the admin listener (editor + metrics dashboard) on its own port; requires -db, empty disables. Front it with the TLS proxy on a dedicated admin origin, IP-allowlisted/firewalled (§7.9)")
	db := fs.String("db", "", "content database for the admin listener (required with -admin-addr)")
	out := fs.String("out", "site", "admin listener: build output directory")
	releases := fs.String("releases", "releases", "admin listener: directory for packaged release tarballs")
	base := fs.String("base", "", "admin listener: seed site base URL (stored settings win)")
	site := fs.String("site", "", "admin listener: seed site name (stored settings win)")
	appDB := fs.String("app-db", "", "runtime store (app.db) enabling creator passkey auth (§2.4); requires -admin-addr and -admin-origin")
	adminOrigin := fs.String("admin-origin", "", "exact admin origin the TLS proxy serves, e.g. https://admin.example.com (required with -app-db; RP ID is derived from it)")
	publicOrigin := fs.String("public-origin", "", "public origin for Community Member passkey auth, e.g. https://example.com; enables member register/login under /_pbc/auth (requires -app-db)")
	maintInterval := fs.Duration("maintenance-interval", 6*time.Hour, "how often the runtime store runs maintenance (prune expired sessions, optional inactivity purge); 0 disables. Requires -app-db")
	inactiveAfter := fs.Duration("inactive-after", 0, "purge member accounts idle longer than this — the lost-passkey backstop, §2.4 (e.g. 4320h ≈ 180d); 0 disables. Requires -app-db")
	purgeDeleteComments := fs.Bool("purge-delete-comments", false, "when purging inactive members, delete their comments instead of anonymizing them (kept but unlinked)")
	fs.Parse(args)

	if *content == "" {
		log.Fatal("pbcssg server: -content is required")
	}
	if *adminAddr != "" && *db == "" {
		log.Fatal("pbcssg server: -admin-addr requires -db")
	}
	if *appDB != "" && (*adminAddr == "" || *adminOrigin == "") {
		log.Fatal("pbcssg server: -app-db (creator passkey auth) requires -admin-addr and -admin-origin")
	}
	if *publicOrigin != "" && *appDB == "" {
		log.Fatal("pbcssg server: -public-origin (member passkey auth) requires -app-db")
	}
	if *inactiveAfter > 0 && *appDB == "" {
		log.Fatal("pbcssg server: -inactive-after (inactivity purge) requires -app-db")
	}
	// The runtime store (app.db) is opened once and shared: it backs the public dynamic
	// endpoints (§7.3, mounted under the reserved prefix on the public listener) and the
	// creator passkey auth on the admin listener. It is opened before server.New so the
	// public listener can mount the dynamic layer; the static serving path never touches
	// it (§7.1).
	var app *appstore.Store
	if *appDB != "" {
		a, err := appstore.Open(*appDB)
		if err != nil {
			// The runtime store may sit on a sealed data volume that has not been unlocked
			// yet (§7.9). Degrade instead of refusing to serve: the immutable public bundle
			// stays up; only the dynamic layer (comments + member/creator passkey auth) is
			// off until app.db is available and the server is restarted. The WARN is loud so
			// a genuine misconfiguration (a wrong path) is not mistaken for a sealed volume.
			log.Printf("WARN pbcssg server: cannot open -app-db %q: %v", *appDB, err)
			log.Printf("WARN pbcssg server: serving the static bundle only — comments and passkey auth are DISABLED until app.db is available and the server is restarted (§7.9)")
		} else {
			app = a
			defer app.Close()
		}
	}
	var dynamic http.Handler
	if app != nil {
		dyn, err := publicapi.New(app, publicapi.Options{MemberOrigin: *publicOrigin})
		if err != nil {
			log.Fatalf("pbcssg server: public dynamic layer: %v", err)
		}
		dynamic = dyn
	}

	srv, err := server.New(server.Config{ContentDir: *content, Dynamic: dynamic})
	if err != nil {
		log.Fatalf("pbcssg server: %v", err)
	}
	if app != nil {
		log.Printf("INFO pbcssg public dynamic endpoints enabled under %s (runtime store)", server.ReservedPrefix)
		if *publicOrigin != "" {
			log.Printf("INFO pbcssg member passkey auth enabled on public origin %s (register/login under %sauth)", *publicOrigin, server.ReservedPrefix)
			log.Printf("INFO pbcssg moderator surface enabled on public origin %s (sign in at %smoderate)", *publicOrigin, server.ReservedPrefix)
		}
	}

	// Runtime-store housekeeping (§2.4): prune expired sessions and, when
	// enabled, purge inactive members, on a background ticker. A no-op without -app-db.
	startMaintenance(app, *maintInterval, *inactiveAfter, *purgeDeleteComments)

	// §7.7 metrics are shown on the editor's Metrics admin page, so they need the admin
	// listener. A bundle with metrics on but no -admin-addr would collect nothing — warn.
	if srv.MetricsEnabled() && *adminAddr == "" {
		log.Printf("WARN pbcssg server: bundle has metrics enabled but -admin-addr is not set — the metrics dashboard is an admin page, so nothing is collected. Add -admin-addr -db.")
	}

	// The admin listener (editor + metrics dashboard) runs on a second loopback listener,
	// fronted by the TLS proxy on the admin origin — never the public origin (§7.9). It
	// runs in its own goroutine so a bind/handler fault there can't take the public site
	// down (§7.1/§7.9), and it alone opens the editing DB — the public listener never does.
	var handler http.Handler = srv
	if *adminAddr != "" {
		st, err := store.Open(*db)
		if err != nil {
			// content.db lives on the same writable data volume as app.db, so a sealed
			// volume takes the admin editor down too. Degrade rather than refuse to serve:
			// skip the admin listener and keep serving the public bundle (§7.9). Unlock the
			// volume and restart to bring the editor back.
			log.Printf("WARN pbcssg server: cannot open -db %q: %v", *db, err)
			log.Printf("WARN pbcssg server: admin editor + metrics DISABLED (its content.db is unavailable — e.g. a sealed volume, §7.9); the public site is unaffected")
		} else {
			if n, err := creator.SeedDefaults(st); err != nil {
				log.Printf("WARN pbcssg server: seed defaults: %v", err)
			} else if n > 0 {
				log.Printf("INFO pbcssg server: seeded %d starter page(s) as drafts + default navigation", n)
			}

			// Content-aware runtime-store retention (§7.3): with both stores open, prune spent
			// invites, old rejected comments, and comments orphaned by deleted pages, and
			// reclaim disk. Retention lives in Settings; interval reuses -maintenance-interval.
			startContentMaintenance(app, st, *maintInterval)

			// When the bundle opts into metrics, wrap the public handler to record aggregate
			// counters (no client IP retained) and hand the registry to the editor to render.
			var reg *metrics.Registry
			if srv.MetricsEnabled() {
				resolver, err := server.NewResolver(creator.LoadTrustedProxies(st))
				if err != nil {
					log.Fatalf("pbcssg server: trusted proxies (Settings): %v", err)
				}
				reg = metrics.New()
				handler = srv.Instrument(reg, resolver.Classify)
				log.Printf("INFO pbcssg metrics enabled (aggregate only, no client IP retained)")
			}

			// Creator passkey auth (§2.4): the shared runtime store (opened above) enables the
			// WebAuthn ceremony endpoints on the admin origin. The RP ID is derived from
			// -admin-origin.
			cr, err := creator.New(st, creator.Config{
				OutDir:     *out,
				ReleaseDir: *releases,
				Build:      build.Config{SiteName: *site, BaseURL: *base, Version: "1.0", OpenGraph: true},
				// Unified Publish (§7.9): build a versioned release dir, repoint -content, reload.
				Publisher:   srv,
				ContentLink: *content,
				Metrics:     reg, // nil unless metrics enabled → editor shows the Metrics page + nav link
				AppStore:    app, // nil unless -app-db → enables creator passkey auth ceremony endpoints
				AdminOrigin: *adminOrigin,
			})
			if err != nil {
				log.Fatalf("pbcssg server: building editor: %v", err)
			}
			if app != nil {
				log.Printf("INFO pbcssg creator passkey auth enabled on admin origin %s (register at /admin/register)", *adminOrigin)
			}
			where := "editor"
			if reg != nil {
				where = "editor + metrics"
			}
			adminSrv := &http.Server{Addr: *adminAddr, Handler: cr, ReadHeaderTimeout: 10 * time.Second}
			go func() {
				log.Printf("INFO pbcssg admin listener (%s) on http://%s (loopback; front with the TLS proxy on the admin origin — §7.9)", where, *adminAddr)
				if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("WARN pbcssg admin listener failed on %s: %v (site serving unaffected)", *adminAddr, err)
				}
			}()
		}
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("INFO pbcssg serving %s on http://%s", *content, *addr)
	log.Fatal(httpSrv.ListenAndServe())
}

func cmdCreator(args []string) {
	fs := flag.NewFlagSet("creator", flag.ExitOnError)
	db := fs.String("db", "", "content database path (created if missing) (required)")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address (loopback only)")
	out := fs.String("out", "site", "build output directory")
	releases := fs.String("releases", "releases", "directory for packaged release tarballs")
	base := fs.String("base", "http://localhost", "site base URL")
	site := fs.String("site", "", "site name")
	buildNo := fs.String("build", "1", "build number")
	gpc := fs.String("gpc", "", "GPC lastUpdate date (YYYY-MM-DD)")
	searchOn := fs.Bool("search", false, "enable client-side search on the built site")
	openGraph := fs.Bool("opengraph", true, "emit Open Graph tags (seed default; editable in Settings)")
	fs.Parse(args)

	if *db == "" {
		log.Fatal("pbcssg creator: -db is required")
	}
	if err := build.ValidateGPCDate(*gpc); err != nil {
		log.Fatalf("pbcssg creator: -gpc: %v", err)
	}
	s, err := store.Open(*db)
	if err != nil {
		log.Fatalf("pbcssg creator: %v", err)
	}
	defer s.Close()

	// First-run only: seed the starter pages (/, /about, /privacy) as drafts and
	// the default primary/footer navigation. A settings marker makes this a no-op
	// on later launches; a failure is non-fatal so the editor still starts. Run it
	// before New so the seeded settings load into the runtime on the first launch.
	if n, err := creator.SeedDefaults(s); err != nil {
		log.Printf("WARN pbcssg creator: seed defaults: %v", err)
	} else if n > 0 {
		log.Printf("INFO pbcssg creator: seeded %d starter page(s) as drafts + default navigation", n)
	}

	cr, err := creator.New(s, creator.Config{
		OutDir:     *out,
		ReleaseDir: *releases,
		Build: build.Config{
			SiteName: *site, BaseURL: *base, Version: "1.0",
			BuildNumber: *buildNo, GPCLastUpdate: *gpc, Search: *searchOn,
			OpenGraph: *openGraph,
		},
	})
	if err != nil {
		log.Fatalf("pbcssg creator: %v", err)
	}
	httpSrv := &http.Server{Addr: *addr, Handler: cr, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("INFO pbcssg editor on http://%s (db %s)", *addr, *db)
	log.Fatal(httpSrv.ListenAndServe())
}

// cmdAdmin dispatches the operator-only `admin` subcommands, run on the host itself
// (host/SSH access is the ops path; host access can always bootstrap admin — SPEC §7.9).
func cmdAdmin(args []string) {
	if len(args) < 1 {
		adminUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "bootstrap":
		cmdAdminBootstrap(args[1:])
	case "-h", "--help", "help":
		adminUsage()
	default:
		fmt.Fprintf(os.Stderr, "pbcssg admin: unknown command %q\n\n", args[0])
		adminUsage()
		os.Exit(2)
	}
}

// cmdAdminBootstrap mints the first creator invite (SPEC §2.4). This is the break
// in the invite bootstrap problem: it is authorized purely by host access to the
// runtime store, so no existing account is required. The single-use code is written
// to stdout (never the log — §9); the operator redeems it on the admin origin and
// registers the first passkey. It stays usable after a creator exists — the
// break-glass / add-a-second-admin path — but warns when one already does.
func cmdAdminBootstrap(args []string) {
	fs := flag.NewFlagSet("admin bootstrap", flag.ExitOnError)
	db := fs.String("db", "", "runtime database path (app.db, created if missing) (required)")
	ttl := fs.Duration("ttl", time.Hour, "how long the invite stays redeemable (0 = no expiry)")
	fs.Parse(args)

	if *db == "" {
		log.Fatal("pbcssg admin bootstrap: -db is required")
	}
	st, err := appstore.Open(*db)
	if err != nil {
		log.Fatalf("pbcssg admin bootstrap: %v", err)
	}
	defer st.Close()

	if n, err := st.CountAccountsByRole(appstore.RoleCreator); err != nil {
		log.Fatalf("pbcssg admin bootstrap: %v", err)
	} else if n > 0 {
		log.Printf("WARN pbcssg admin bootstrap: %d creator account(s) already exist; minting another creator invite (break-glass / second admin)", n)
	}

	code, _, err := st.MintInvite(appstore.MintParams{Role: appstore.RoleCreator, TTL: *ttl})
	if err != nil {
		log.Fatalf("pbcssg admin bootstrap: %v", err)
	}

	// The code is a secret: write it to stdout only, kept off the (stderr) log.
	fmt.Printf("%s\n", code)
	validity := "no expiry"
	if *ttl > 0 {
		validity = "valid for " + ttl.String()
	}
	log.Printf("INFO pbcssg admin bootstrap: creator invite minted (%s). Redeem it on the admin origin to register the first passkey.", validity)
}

func adminUsage() {
	fmt.Fprint(os.Stderr, `pbcssg admin — operator-only commands (run on the host)

Usage:
  pbcssg admin bootstrap -db <app.db> [-ttl 1h]

  bootstrap  Mint the first creator invite. The single-use code is printed to
             stdout; redeem it on the admin origin to register the first passkey.
`)
}

func usage() {
	fmt.Fprint(os.Stderr, `pbcssg — privacy-first static site generator

Usage:
  pbcssg creator -db <content.db> [-addr host:port] [-out dir] [-base url] [-site name]
  pbcssg build   -db <content.db> -out <dir> -base <url> [-site name] [-build n] [-gpc YYYY-MM-DD] [-search]
  pbcssg server  -content <dir> [-addr host:port] [-admin-addr host:port -db content.db]
                 [-app-db app.db -admin-origin https://admin.example.com]
                 [-public-origin https://example.com] [-inactive-after 4320h]
  pbcssg admin   bootstrap -db <app.db> [-ttl 1h]
  pbcssg version

Run "pbcssg <subcommand> -h" for flags.
`)
}

// cmdVersion prints the tool's release version (the git tag this binary was built
// from) plus commit and Go version. It reads the build info the toolchain embeds:
// `go install …@vX.Y.Z` reports that tag; a local `go build` reports "(devel)"
// with the VCS commit. This is the *tool* version, distinct from a built site's
// own /version + build number.
func cmdVersion() {
	version := "(devel)"
	var revision, vcsTime string
	dirty := false
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			version = info.Main.Version
		}
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.time":
				vcsTime = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	fmt.Printf("pbcssg %s\n", version)
	if revision != "" {
		short := revision
		if len(short) > 12 {
			short = short[:12]
		}
		if dirty {
			short += "-dirty"
		}
		if vcsTime != "" {
			fmt.Printf("  commit %s (%s)\n", short, vcsTime)
		} else {
			fmt.Printf("  commit %s\n", short)
		}
	}
	fmt.Printf("  %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
