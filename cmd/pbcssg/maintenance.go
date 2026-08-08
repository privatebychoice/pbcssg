package main

import (
	"log"
	"time"

	"go.privatebychoice.com/pbcssg/internal/appstore"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// startMaintenance launches the runtime-store housekeeping loop (§2.4): it
// prunes expired sessions on every tick and, when inactiveAfter > 0, purges member
// accounts idle longer than that (the lost-passkey backstop). It runs once at startup
// and then on the interval. It is a no-op without a store or with a non-positive
// interval. The loop lives for the life of the process — the server has no graceful-
// shutdown phase — so the ticker simply stops when the process exits.
func startMaintenance(app *appstore.Store, interval, inactiveAfter time.Duration, deleteComments bool) {
	if app == nil {
		return
	}
	if interval <= 0 {
		log.Printf("INFO pbcssg maintenance disabled (-maintenance-interval 0); expired sessions accumulate until restart")
		return
	}
	if inactiveAfter > 0 {
		mode := "anonymizing their comments"
		if deleteComments {
			mode = "deleting their comments"
		}
		log.Printf("INFO pbcssg maintenance every %s: prune expired sessions + purge members idle > %s (%s)", interval, inactiveAfter, mode)
	} else {
		log.Printf("INFO pbcssg maintenance every %s: prune expired sessions (inactivity purge off; enable with -inactive-after)", interval)
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		runMaintenance(app, inactiveAfter, deleteComments) // once at startup
		for range t.C {
			runMaintenance(app, inactiveAfter, deleteComments)
		}
	}()
}

// runMaintenance performs one housekeeping pass: prune expired sessions, then (when
// enabled) purge inactive members. Failures are logged and do not stop the loop — the
// next tick retries.
func runMaintenance(app *appstore.Store, inactiveAfter time.Duration, deleteComments bool) {
	if n, err := app.PruneExpiredSessions(); err != nil {
		log.Printf("WARN pbcssg maintenance: prune sessions: %v", err)
	} else if n > 0 {
		log.Printf("INFO pbcssg maintenance: pruned %d expired session(s)", n)
	}
	if inactiveAfter <= 0 {
		return
	}
	cutoff := time.Now().Add(-inactiveAfter)
	if n, err := app.PurgeInactiveMembers(cutoff, !deleteComments); err != nil {
		log.Printf("WARN pbcssg maintenance: purge inactive members: %v", err)
	} else if n > 0 {
		log.Printf("INFO pbcssg maintenance: purged %d inactive member account(s) (idle since before %s)", n, cutoff.Format("2006-01-02"))
	}
}

// startContentMaintenance launches the retention loop that keeps the runtime store from
// growing without bound: prune spent invites, old rejected comments, and comments
// orphaned by deleted pages, then reclaim disk on a schedule. Retention is read from the
// content store's Settings each pass, so changes take effect without a restart. It needs
// BOTH stores — the app store to prune, the content store for the settings and the live
// page list — so it runs where both are open (the admin-enabled server). A no-op without
// either store or a non-positive interval.
func startContentMaintenance(app *appstore.Store, content *store.Store, interval time.Duration) {
	if app == nil || content == nil || interval <= 0 {
		return
	}
	log.Printf("INFO pbcssg maintenance every %s: prune spent invites + old rejected/orphaned comments + release dormant aliases + prune empty tombstones + periodic vacuum (retention in Settings)", interval)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		runContentMaintenance(app, content)
		for range t.C {
			runContentMaintenance(app, content)
		}
	}()
}

// runContentMaintenance performs one retention pass. Each prune is independent and gated
// on its retention being > 0 (0 disables it); failures are logged and never stop the loop.
func runContentMaintenance(app *appstore.Store, content *store.Store) {
	cfg := content.Maintenance()
	now := time.Now()
	const day = 24 * time.Hour

	if cfg.InviteDays > 0 {
		if n, err := app.PruneSpentInvites(now.Add(-time.Duration(cfg.InviteDays) * day)); err != nil {
			log.Printf("WARN pbcssg maintenance: prune invites: %v", err)
		} else if n > 0 {
			log.Printf("INFO pbcssg maintenance: pruned %d spent invite(s)", n)
		}
	}
	if cfg.RejectedDays > 0 {
		if n, err := app.PruneRejectedComments(now.Add(-time.Duration(cfg.RejectedDays) * day)); err != nil {
			log.Printf("WARN pbcssg maintenance: prune rejected comments: %v", err)
		} else if n > 0 {
			log.Printf("INFO pbcssg maintenance: pruned %d rejected comment(s)", n)
		}
	}
	if cfg.OrphanDays > 0 {
		paths, err := content.AllPaths()
		switch {
		case err != nil:
			log.Printf("WARN pbcssg maintenance: read page paths (orphan prune skipped): %v", err)
		case len(paths) == 0:
			// No pages listed — skip rather than treat every comment as orphaned (safety).
		default:
			live := make(map[string]bool, len(paths))
			for _, p := range paths {
				live[p] = true
			}
			if n, err := app.PruneOrphanedComments(live, now.Add(-time.Duration(cfg.OrphanDays)*day)); err != nil {
				log.Printf("WARN pbcssg maintenance: prune orphaned comments: %v", err)
			} else if n > 0 {
				log.Printf("INFO pbcssg maintenance: pruned %d orphaned comment(s) (their pages no longer exist)", n)
			}
		}
	}
	if cfg.AliasReleaseDays > 0 {
		if n, err := app.ReleaseInactiveAliases(now.Add(-time.Duration(cfg.AliasReleaseDays) * day)); err != nil {
			log.Printf("WARN pbcssg maintenance: release inactive aliases: %v", err)
		} else if n > 0 {
			log.Printf("INFO pbcssg maintenance: released %d dormant member display name(s)", n)
		}
	}
	if cfg.TombstoneDays > 0 {
		if n, err := app.PruneChildlessTombstones(now.Add(-time.Duration(cfg.TombstoneDays) * day)); err != nil {
			log.Printf("WARN pbcssg maintenance: prune tombstones: %v", err)
		} else if n > 0 {
			log.Printf("INFO pbcssg maintenance: pruned %d empty deleted-comment placeholder(s)", n)
		}
	}
	// Reclaim disk on a schedule — VACUUM rewrites the file, so do it at most every
	// VacuumDays, not every pass.
	if cfg.VacuumDays > 0 && now.Sub(content.LastVacuum()) >= time.Duration(cfg.VacuumDays)*day {
		if err := app.Vacuum(); err != nil {
			log.Printf("WARN pbcssg maintenance: vacuum: %v", err)
		} else {
			if err := content.SetLastVacuum(now); err != nil {
				log.Printf("WARN pbcssg maintenance: record vacuum time: %v", err)
			}
			log.Printf("INFO pbcssg maintenance: vacuumed the runtime store (reclaimed free space)")
		}
	}
}
