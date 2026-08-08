package creator

import (
	"html/template"
	"strings"
)

func adminTemplates() *template.Template {
	return template.Must(template.New("admin").Funcs(template.FuncMap{
		"join":       func(ss []string) string { return strings.Join(ss, ", ") },
		"gradeClass": gradeClass,
	}).Parse(adminTmpl))
}

const adminTmpl = `
{{define "top"}}<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<script src="/admin/assets/theme-toggle.js"></script>
<title>pbcssg editor</title>
<link rel="stylesheet" href="/admin/assets/admin.css">
</head><body>
<header class="admin-header row-between">
<a href="/" class="brand">pbcssg <span>editor</span></a>
<nav class="admin-nav"><a href="/">Pages</a> <a href="/admin/media">Media</a> <a href="/admin/classification">Classification</a> <a href="/admin/keygroups">Key groups</a>{{if .AuthEnabled}} <a href="/admin/moderation">Moderation</a> <a href="/admin/invites">Invites</a> <a href="/admin/passkeys">Passkeys</a>{{end}} <a href="/admin/favicon">Favicon</a> <a href="/admin/errorpages">Error pages</a> <a href="/admin/settings">Settings</a>{{if .ShowMetrics}} <a href="/admin/metrics">Metrics</a>{{end}} <button type="button" class="theme-toggle" data-pbcssg-theme-toggle hidden>◐ Auto</button>{{if .AuthEnabled}} <form method="post" action="/admin/logout" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="linklike">Sign out</button></form>{{end}}</nav>
</header>
<main>{{end}}

{{define "bottom"}}</main></body></html>{{end}}

{{define "dashboard"}}{{template "top" .}}
<div class="row-between">
<h1>Pages</h1>
<div class="toolbar">
<a class="btn" href="/pages/new">New page</a>
<form method="post" action="/build" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="btn">Generate site</button></form>
{{if .Publisher}}<form method="post" action="/admin/publish" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="btn" title="Build, repoint the current symlink, and reload the live site in-process (§7.9)">Publish live</button></form>{{end}}
<form method="post" action="/admin/release" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Package release</button></form>
</div>
</div>
{{if .ShowComments}}<p class="hint">Comments across the site: <strong>{{.CommentTotals.Total}}</strong> total — <a href="/admin/moderation">{{.CommentTotals.Pending}} pending</a>, <a href="/admin/moderation?status=approved">{{.CommentTotals.Approved}} approved</a>{{if .CommentTotals.Rejected}}, {{.CommentTotals.Rejected}} rejected{{end}}.</p>{{end}}
<table class="grid">
<thead><tr>
<th><a class="sort-link" href="{{.HTitle}}">Title{{.ATitle}}</a></th>
<th><a class="sort-link" href="{{.HPath}}">Path{{.APath}}</a></th>
<th><a class="sort-link" href="{{.HStatus}}">Status{{.AStatus}}</a></th>
<th><a class="sort-link" href="{{.HUpdated}}">Updated{{.AUpdated}}</a></th>
{{if .ShowComments}}<th title="Comments stored for this page path (all statuses). They persist even if the page or its comment block is removed.">Comments</th>{{end}}
<th></th></tr></thead>
<tbody>
{{range .Pages}}
<tr>
<td><a href="/pages/{{.ID}}">{{.Title}}</a></td>
<td><code>{{.Path}}</code></td>
<td><form method="post" action="/pages/{{.ID}}/{{if eq .Status "published"}}unpublish{{else}}publish{{end}}" class="inline">
<input type="hidden" name="csrf" value="{{$.CSRF}}">
<input type="hidden" name="return" value="{{$.ReturnURL}}">
<button type="submit" class="status {{.Status}}" title="{{if eq .Status "published"}}Unpublish this page{{else}}Publish this page{{end}}">{{.Status}}</button>
</form></td>
<td>{{.UpdatedAt.Format "2006-01-02 15:04"}}</td>
{{if $.ShowComments}}<td>{{$n := index $.CommentCounts .Path}}{{if $n}}<a href="/admin/moderation?q_page={{.Path}}&status=approved" title="Review this page's comments">{{$n}}</a>{{else}}0{{end}}</td>{{end}}
<td class="row-actions"><a href="/pages/{{.ID}}/preview" target="_blank" rel="noopener">preview</a>
<button type="button" class="btn btn-sm" data-copy="[{{.Title}}]({{.Path}})" title="Copy a Markdown link to this page">Link</button></td>
</tr>
{{else}}
<tr><td colspan="{{if .ShowComments}}6{{else}}5{{end}}">No pages yet. <a href="/pages/new">Create one</a>.</td></tr>
{{end}}
</tbody>
</table>
{{if gt .TotalPages 1}}
<nav class="pager" aria-label="Pagination">
{{if .PrevURL}}<a class="btn" href="{{.PrevURL}}">← Prev</a>{{else}}<span class="btn disabled" aria-disabled="true">← Prev</span>{{end}}
<span class="pager-status">Page {{.Page}} of {{.TotalPages}} · {{.Total}} page(s)</span>
{{if .NextURL}}<a class="btn" href="{{.NextURL}}">Next →</a>{{else}}<span class="btn disabled" aria-disabled="true">Next →</span>{{end}}
</nav>
{{end}}
<script src="/admin/assets/copy.js" defer></script>
{{template "bottom"}}{{end}}

{{define "edit"}}{{template "top" .}}
<h1>{{if .IsNew}}New page{{else}}Edit page{{end}}</h1>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Warnings}}<div class="alert warn"><strong>Saved.</strong> This page references media that is not in the Media library — upload it (or fix the reference) or the build will emit a broken link:<ul>{{range .Warnings}}<li><code>{{.}}</code></li>{{end}}</ul></div>{{end}}
<div class="editor-split">
<div class="editor-pane">
<form id="editor" method="post" action="{{if .IsNew}}/pages{{else}}/pages/{{.Page.ID}}{{end}}">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label>Title<input name="title" value="{{.Page.Title}}" required></label>
<label>Path <small class="hint">The page's URL, e.g. <code>/about</code> or <code>/blog/my-post</code>. Lowercase letters, numbers, and hyphens only, in <code>/</code>-separated segments — no spaces or uppercase. Use <code>/</code> for the home page.</small><input name="path" id="page-path" value="{{.Page.Path}}" placeholder="/blog/my-post"></label>
<div class="path-tools"><button type="button" class="btn btn-sm" id="path-random" title="Append a random, unguessable suffix — makes an unlisted page a capability URL (§6.16)">+ Random suffix</button></div>
<label>Tags <small class="hint">Comma-separated. Shown as tag links on the page (and their <code>/tags/</code> pages) and included in on-site search.</small><input name="tags" value="{{join .Content.Tags}}" placeholder="privacy, self-hosting"></label>
<label>Keywords <small class="hint">Comma-separated. Extra terms for on-site search only — not shown on the page.</small><input name="keywords" value="{{join .Content.Keywords}}" placeholder="gpc, tracking"></label>
<label>Summary <small class="hint">One sentence. Becomes the page's <code>meta description</code> (SEO) and its search snippet.</small><input name="summary" value="{{.Content.Summary}}"></label>
<label class="check"><input type="checkbox" name="isPost" value="1"{{if .Content.IsPost}} checked{{end}}> This is a post / article <small class="hint">— enables post-only features: reading time (if turned on in Settings) and Related-posts blocks</small></label>
<label class="check"><input type="checkbox" name="isIndex" value="1"{{if .Content.IsIndex}} checked{{end}}> This is an index page <small class="hint">— lets a Page-index block on this page render its list of child pages</small></label>
<label class="check"><input type="checkbox" name="listExclude" value="1"{{if .Content.ListExclude}} checked{{end}}> Exclude from page-index listings <small class="hint">— hides this page from Page-index blocks on other pages</small></label>
<label class="check"><input type="checkbox" name="noIndex" value="1"{{if .Content.NoIndex}} checked{{end}}> Hide from search engines (noindex) <small class="hint">— adds <code>&lt;meta name="robots" content="noindex"&gt;</code> and drops this page from sitemap.xml</small></label>
<label class="check"><input type="checkbox" name="unlisted" value="1"{{if .Content.Unlisted}} checked{{end}}> Unlisted (hidden page) <small class="hint">— a members page: removed from search, sitemap, page-index, related-posts, <strong>tags, feeds, and the published privacy manifest</strong> (implies noindex). Pair with an <strong>unguessable path</strong> (Random suffix, above) and gated blocks (§6.10). The page still shows its own External References section.</small></label>
<label>Social preview image <small class="hint">— optional <code>og:image</code> for link/social cards (a Media library path like <code>/media/…</code>); falls back to the Settings default. Needs a Base URL and “Emit Open Graph tags” on.</small><input type="text" name="ogImage" value="{{.Content.OGImage}}" placeholder="/media/…"></label>
<label><span id="body-label">Body (Markdown)</span> <small class="hint">Public page content. This is the text indexed for on-site search when “Index full body text” is enabled (Settings → Search) — keep members-only text in a gated block, never here.</small><textarea name="body" rows="14">{{.Content.Body}}</textarea></label>
<fieldset class="blocks">
<legend>Content blocks</legend>
<input type="hidden" id="blocks-field" name="blocks" value="{{.BlocksJSON}}">
<div id="blocks-editor"></div>
<div class="blocks-toolbar">
<button type="button" id="add-markdown" class="btn">+ Markdown block</button>
<button type="button" id="add-image" class="btn">+ Image</button>
<button type="button" id="add-media" class="btn">+ Video / Audio</button>
<button type="button" id="add-callout" class="btn">+ Callout</button>
<button type="button" id="add-citation" class="btn">+ Citation</button>
<button type="button" id="add-code" class="btn">+ Code</button>
<button type="button" id="add-details" class="btn">+ Details / FAQ</button>
<button type="button" id="add-toc" class="btn">+ Table of contents</button>
<button type="button" id="add-related" class="btn">+ Related posts</button>
<button type="button" id="add-gallery" class="btn">+ Gallery</button>
<button type="button" id="add-share" class="btn">+ Share</button>
<button type="button" id="add-comments" class="btn">+ Comments</button>
<button type="button" id="add-youtube" class="btn">+ YouTube video</button>
<button type="button" id="add-embed" class="btn">+ Embed</button>
<button type="button" id="add-index" class="btn">+ Page index</button>
<button type="button" id="add-reveal" class="btn">+ Hidden (reveal)</button>
</div>
</fieldset>
<button type="submit">Save</button>
</form>
{{if not .IsNew}}
<p class="hint page-flow"><strong>Publishing flow:</strong> <strong>Save</strong> stores your draft → <strong>Publish…</strong> makes the saved version live → <strong>Generate site</strong> (top of the page) rebuilds the static bundle from every published page. Draft edits — and newly published changes — don’t reach the built site until you Generate.</p>
<div class="page-actions">
<form method="post" action="/pages/{{.Page.ID}}/publish" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Publish…</button></form>
{{if eq .Page.Status "published"}}
<form method="post" action="/pages/{{.Page.ID}}/unpublish" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="btn">Unpublish</button></form>
{{end}}
<a class="btn" href="/pages/{{.Page.ID}}/preview" target="_blank" rel="noopener">Open preview</a>
<button type="button" class="btn" data-copy="[{{.Page.Title}}]({{.Page.Path}})" title="Copy a Markdown link to this page">Copy MD link</button>
<form method="post" action="/pages/{{.Page.ID}}/rekey" class="inline" onsubmit="return confirm('Rekey this page? Hidden (reveal) blocks re-encode on the next build. This refreshes the obfuscation key — it does NOT revoke any shared code.')"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="btn" title="Regenerate this page's reveal-block key">Rekey reveal blocks</button></form>
{{if and .ShowComments .CommentCount}}<span class="hint">This page has <a href="/admin/moderation?q_page={{.Page.Path}}&status=approved" title="Review this page's comments">{{.CommentCount}} comment(s)</a> — they stay in moderation under this path if you delete the page.</span>{{end}}
<form method="post" action="/pages/{{.Page.ID}}/delete" class="inline" onsubmit="return confirm('Delete this page and its revisions?{{if .CommentCount}} Its {{.CommentCount}} comment(s) will remain in moderation under this path.{{end}}')"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="danger">Delete</button></form>
</div>
{{end}}
</div>
<div class="preview-col">
<section class="preview-view">
<h2 class="preview-label">Desktop</h2>
<div class="preview-scale" id="preview-desktop-wrap">
<iframe id="preview-desktop" class="preview-desktop" title="Desktop preview"></iframe>
</div>
</section>
<section class="preview-view">
<h2 class="preview-label">Mobile</h2>
<iframe id="preview-mobile" class="preview-frame preview-mobile" title="Mobile preview"></iframe>
</section>
<section class="media-panel" id="media-panel" aria-live="polite" hidden>
<h2>⚠ Broken media</h2>
<div id="media-warnings"></div>
<p class="media-panel-hint">Referenced but not in the Media library. Upload the file (or fix the path) — the build will otherwise emit a broken link. The impacted body/blocks are flagged in the editor.</p>
</section>
<section class="link-panel" aria-live="polite">
<h2>External references</h2>
<div id="link-badges"></div>
</section>
</div>
</div>
<script src="/admin/assets/blocks.js" defer></script>
<script src="/admin/assets/admin.js" defer></script>
<script src="/admin/assets/copy.js" defer></script>
{{template "bottom"}}{{end}}

{{define "publish"}}{{template "top" .}}
<h1>Publish: {{.Page.Title}}</h1>
<p>This page references external domains that need your acknowledgement before publishing (SPEC §5.3):</p>
<ul class="badges flags">
{{range .Flags}}
<li><span class="grade {{gradeClass .Grade}}" title="Grade {{.Grade}}">{{.Grade}}</span> <strong>{{.GradeName}}</strong> — <code>{{.Domain}}</code>
{{range .Reasons}}<br><small class="reason">{{.}}</small>{{end}}</li>
{{end}}
</ul>
<form method="post" action="/pages/{{.Page.ID}}/publish" class="inline">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="ack" value="1">
<button type="submit">Acknowledge &amp; publish</button>
</form>
<a class="btn" href="/pages/{{.Page.ID}}">Cancel</a>
{{template "bottom"}}{{end}}

{{define "badges"}}
{{- if .Badges}}
<ul class="badges">
{{- range .Badges}}
<li><span class="grade {{.Class}}" title="Grade {{.Grade}}">{{.Grade}}</span> <code>{{.Domain}}</code> <small>{{.GradeName}}{{if gt .Count 1}} · {{.Count}} refs{{end}}</small>
{{- range .Reasons}}<br><small class="reason">{{.}}</small>{{end}}</li>
{{- end}}
</ul>
{{- else}}
<p class="badges-empty">No external references — fully self-hosted. ✓</p>
{{- end}}
{{end}}

{{/* mediawarnings is the #media-warnings fragment: the list of broken local media
     references (empty renders nothing, and the client hides the panel). */}}
{{define "mediawarnings"}}
{{- if .}}
<ul class="media-broken-list">
{{- range .}}
<li><code>{{.}}</code></li>
{{- end}}
</ul>
{{- end}}
{{end}}

{{define "media"}}{{template "top" .}}
<div class="row-between">
<h1>Media library</h1>
</div>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{range .Warnings}}<p class="alert warn">{{.}}</p>{{end}}
<form method="post" action="/admin/media" enctype="multipart/form-data" class="upload">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label>Upload media <small>(images: JPEG, PNG, SVG, WebP · audio/video: MP4, M4A, MOV, MP3, WebM, Ogg, WAV — metadata is stripped and SVGs are sanitized on ingest; audio/video is stored on disk, images in the database)</small>
<input type="file" name="file" accept="image/jpeg,image/png,image/svg+xml,image/webp,video/mp4,video/quicktime,video/webm,video/ogg,audio/mp4,audio/mpeg,audio/wav,audio/webm,audio/ogg,.mp4,.m4a,.mov,.mp3,.wav,.webm,.weba,.oga,.ogg,.ogv,.mkv,.mka" required></label>
<button type="submit">Upload</button>
</form>
<nav class="media-tabs" aria-label="Media types">
<a class="media-tab{{if eq .Type "image"}} active{{end}}" href="/admin/media?type=image">Images <span class="media-count">{{.ImgCount}}</span></a>
<a class="media-tab{{if eq .Type "video"}} active{{end}}" href="/admin/media?type=video">Video <span class="media-count">{{.VidCount}}</span></a>
<a class="media-tab{{if eq .Type "audio"}} active{{end}}" href="/admin/media?type=audio">Audio <span class="media-count">{{.AudCount}}</span></a>
</nav>
<form method="get" action="/admin/media" class="media-search" role="search">
<input type="hidden" name="type" value="{{.Type}}">
<input type="search" name="q" value="{{.Q}}" placeholder="Search filenames &amp; notes…" aria-label="Search media by filename or note">
<button type="submit" class="btn">Search</button>
{{if .Q}}<a class="btn" href="/admin/media?type={{.Type}}">Clear</a>{{end}}
</form>
<table class="grid">
<thead><tr><th>Preview</th><th>File</th><th>Format</th><th>Size</th><th>Uploaded</th><th>Note</th><th>Tags</th><th>Copy</th><th></th></tr></thead>
<tbody>
{{range .Items}}
<tr>
<td>{{if eq .Kind "image"}}<a class="thumb-link" href="{{.Ref}}" target="_blank" rel="noopener" aria-label="View full-size image: {{.Filename}}"><img class="thumb" src="{{.Ref}}" alt="" loading="lazy"></a>{{else}}<span class="thumb thumb-av">{{if eq .Kind "video"}}▶{{else}}♪{{end}}</span>{{end}}</td>
<td>{{.Filename}}<br><small>{{slice .SHA256 0 12}}…</small></td>
<td>{{.Format}}</td>
<td>{{.Size}} B</td>
<td><small>{{.Date}}</small></td>
<td><form method="post" action="/admin/media/{{.SHA256}}/note" class="media-note-form">
<input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="type" value="{{$.Type}}"><input type="hidden" name="q" value="{{$.Q}}"><input type="hidden" name="page" value="{{$.Page}}">
<label class="sr-only" for="note-{{.SHA256}}">Note for {{.Filename}}</label>
<textarea id="note-{{.SHA256}}" name="note" rows="2" maxlength="500" class="media-note-input" placeholder="What is this file for?">{{.Note}}</textarea>
<button type="submit" class="btn btn-sm">Save note</button>
</form></td>
<td><form method="post" action="/admin/media/{{.SHA256}}/tags" class="media-note-form">
<input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="type" value="{{$.Type}}"><input type="hidden" name="q" value="{{$.Q}}"><input type="hidden" name="page" value="{{$.Page}}">
<label class="sr-only" for="tags-{{.SHA256}}">Tags for {{.Filename}}</label>
<input id="tags-{{.SHA256}}" type="text" name="tags" value="{{.Tags}}" class="media-note-input" placeholder="comma, separated, tags">
<button type="submit" class="btn btn-sm">Save tags</button>
</form></td>
<td><div class="media-copy">
{{if .Markdown}}<button type="button" class="btn btn-sm" data-copy="{{.Markdown}}">Markdown</button>{{end}}
<button type="button" class="btn btn-sm" data-copy="{{.Ref}}">Path</button>
</div></td>
<td><form method="post" action="/admin/media/{{.SHA256}}/delete" class="inline" onsubmit="return confirm('Delete this item?')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="type" value="{{$.Type}}"><button type="submit" class="danger">Delete</button></form></td>
</tr>
{{else}}
<tr><td colspan="9">{{if .Q}}No {{.Type}} media matches “{{.Q}}”.{{else}}No {{.Type}} media yet — upload above.{{end}}</td></tr>
{{end}}
</tbody>
</table>
{{if gt .TotalPages 1}}
<nav class="pager" aria-label="Pagination">
{{if .PrevURL}}<a class="btn" href="{{.PrevURL}}">← Prev</a>{{else}}<span class="btn disabled" aria-disabled="true">← Prev</span>{{end}}
<span class="pager-status">Page {{.Page}} of {{.TotalPages}} · {{.Total}} item(s)</span>
{{if .NextURL}}<a class="btn" href="{{.NextURL}}">Next →</a>{{else}}<span class="btn disabled" aria-disabled="true">Next →</span>{{end}}
</nav>
{{end}}
<script src="/admin/assets/copy.js" defer></script>
{{template "bottom"}}{{end}}

{{define "previewpage"}}{{template "top" .}}
<div class="row-between">
<h1>Preview: {{.Page.Title}}</h1>
<a class="btn" href="/pages/{{.Page.ID}}">← Edit</a>
</div>
<p class="hint">This preview mirrors the built page, including the External references listing between the content and the footer.</p>
<iframe class="preview preview-tall" title="Page preview" src="/pages/{{.Page.ID}}/preview/raw"></iframe>
{{template "bottom"}}{{end}}

{{define "settings"}}{{template "top" .}}
<h1>Settings</h1>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
<form method="post" action="/admin/settings" class="settings-form">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<fieldset>
<legend>Site</legend>
<label>Site name<input name="siteName" value="{{.Cfg.SiteName}}"></label>
<label>Base URL<input name="baseURL" value="{{.Cfg.BaseURL}}" placeholder="https://example.com"></label>
<label>Local server test URL <small>(optional; loopback origin where you run <code>pbcssg server</code>, e.g. <code>http://127.0.0.1:8080</code>. Enables a “Local Test” gate link on the <a href="/admin/keygroups">Key groups</a> page so you can unlock members content against a local build. Editor-only — never used by the build.)</small><input name="localTestURL" value="{{.LocalTestURL}}" placeholder="http://127.0.0.1:8080"></label>
<label>First-party domains <small>(comma separated; the base host is implicit)</small><input name="firstParty" value="{{.FirstParty}}" placeholder="cdn.example.com, media.example.com"></label>
<label>Language<input name="lang" value="{{.Cfg.Lang}}" placeholder="en"></label>
</fieldset>
<fieldset>
<legend>Build &amp; deploy</legend>
<label>Version <small>(your site's major.minor, e.g. <code>1.0</code>; forms the deploy identity <code>version.release</code> in <code>/version</code> and the release tarball name)</small><input name="version" value="{{.Cfg.Version}}" placeholder="1.0"></label>
<p><small>The <strong>release</strong> number (the third component) auto-increments each time you Package release; it shows in the build report and <code>/version</code>, not here.</small></p>
<label>GPC lastUpdate <small class="hint">The date you last affirmed your GPC stance, published in <code>/.well-known/gpc.json</code>. Optional — leave blank to omit it (only <code>gpc: true</code> is required). Update it when your data-sharing practices change, not on every build.</small><input type="date" name="gpc" value="{{.Cfg.GPCLastUpdate}}"></label>
</fieldset>
<fieldset>
<legend>Search</legend>
<label class="check"><input type="checkbox" name="search" value="1"{{if .Cfg.Search}} checked{{end}}> Emit the client-side search index &amp; widget</label>
<label class="check"><input type="checkbox" name="searchFullText" value="1"{{if .Cfg.SearchFullText}} checked{{end}}> Index full body text (default: headings + summary)</label>
</fieldset>
<fieldset>
<legend>SEO</legend>
<label class="check"><input type="checkbox" name="openGraph" value="1"{{if .Cfg.OpenGraph}} checked{{end}}> Emit Open Graph tags <small>(og: title/description/url for link previews; static meta, no third-party requests)</small></label>
<label>Default social preview image <small>(optional; a Media library path like <code>/media/…</code> or an absolute URL, used for link/social cards when a page sets none; needs a Base URL to become an absolute <code>og:image</code>)</small><input type="text" name="ogImageDefault" value="{{.Cfg.OGImageDefault}}" placeholder="/media/…"></label>
<label class="check"><input type="checkbox" name="sitemap" value="1"{{if .Cfg.Sitemap}} checked{{end}}> Generate <code>sitemap.xml</code> + <code>robots.txt</code> <small>(helps search engines discover pages; needs a Base URL for absolute links; static files, no third-party requests)</small></label>
<label class="check" style="margin-inline-start:1.5rem"><input type="checkbox" name="sitemapListings" value="1"{{if .Cfg.SitemapListings}} checked{{end}}> Include generated listing pages in the sitemap <small>(tags, feeds index, classification; untick to list only your own content pages)</small></label>
</fieldset>
<fieldset>
<legend>Posts</legend>
<label class="check"><input type="checkbox" name="readingTime" value="1"{{if .Cfg.ShowReadingTime}} checked{{end}}> Show reading time on posts <small>(adds “~N min read” under the title of pages marked “This is a post”; estimated from word count, no third-party requests)</small></label>
</fieldset>
<fieldset>
<legend>Metrics (private dashboard)</legend>
<label class="check"><input type="checkbox" name="metrics" value="1"{{if .Cfg.Metrics}} checked{{end}}> Enable the private metrics dashboard <small>(opt-in, aggregate-only server metrics with a <code>/16</code> network heat map; shown on the <strong>Metrics</strong> admin page — served only on the admin origin (never the public site), and no client IP is stored. Takes effect after a rebuild + server restart. §7.7)</small></label>
<label>Trusted proxies <small>(CIDR allowlist of reverse proxies whose <code>X-Forwarded-For</code> is trusted for metrics client-IP resolution; one per line or comma-separated. Blank = loopback only.)</small><textarea name="trustedProxies" rows="2" placeholder="127.0.0.0/8&#10;::1/128">{{.TrustedProxies}}</textarea></label>
</fieldset>
<fieldset>
<legend>Releases</legend>
<label>Keep releases <small>(how many versioned release directories <strong>Publish live</strong> retains on the host; older ones are removed after a successful publish. The live release is never removed. <code>0</code> = keep all. §7.4)</small><input type="number" name="keepReleases" min="0" step="1" value="{{.KeepReleases}}"></label>
</fieldset>
<fieldset>
<legend>Maintenance</legend>
<p class="hint">Automatic runtime-store cleanup, run on the maintenance interval, so the accounts/comments database (<code>app.db</code>) doesn't grow without bound. Set any value to <code>0</code> to disable that prune. Nothing that still matters is removed — <strong>redeemed</strong> invites (kept as "invited by" provenance) and <strong>pending</strong>/<strong>approved</strong> comments are never touched. Requires the runtime store (<code>-app-db</code>).</p>
<label>Spent-invite retention <small>(days; delete <em>unredeemed</em> invites this long after they were revoked or expired. Default 30.)</small><input type="number" name="maintInviteDays" min="0" step="1" value="{{.Maint.InviteDays}}"></label>
<label>Rejected-comment retention <small>(days; delete rejected (hidden) comments this long after they were rejected. Default 30.)</small><input type="number" name="maintRejectedDays" min="0" step="1" value="{{.Maint.RejectedDays}}"></label>
<label>Orphaned-comment retention <small>(days; delete comments whose page no longer exists, this long after they were posted. Default 90.)</small><input type="number" name="maintOrphanDays" min="0" step="1" value="{{.Maint.OrphanDays}}"></label>
<label>Dormant-alias release <small>(days; free a member's display name after they've been idle this long, so it returns to the pool. Their old comments revert to Anonymous. Default 90.)</small><input type="number" name="maintAliasReleaseDays" min="0" step="1" value="{{.Maint.AliasReleaseDays}}"></label>
<label>Deleted-comment cleanup <small>(days; remove a “[deleted]” placeholder this long after deletion, once it has no replies left. Default 30.)</small><input type="number" name="maintTombstoneDays" min="0" step="1" value="{{.Maint.TombstoneDays}}"></label>
<label>Alias changes per day <small>(how many times a member/moderator may change their display name per day — anti-churn. 0 = unlimited. Default 3. Stored in the runtime store.)</small><input type="number" name="aliasDailyCap" min="0" step="1" value="{{.AliasDailyCap}}"></label>
<label>Reclaim disk (vacuum) <small>(days between VACUUM passes that shrink the runtime-store file after deletions. Default 30.)</small><input type="number" name="maintVacuumDays" min="0" step="1" value="{{.Maint.VacuumDays}}"></label>
</fieldset>
<fieldset>
<legend>security.txt (RFC 9116)</legend>
<label>Contact <small>(one per line; a <code>mailto:</code>, <code>tel:</code>, or <code>https://</code> URI. Required to publish <code>/.well-known/security.txt</code>)</small><textarea name="secContact" rows="2" placeholder="mailto:security@example.com">{{.SecContact}}</textarea></label>
<label>Expires <small>(a date <code>YYYY-MM-DD</code> or RFC 3339 timestamp; leave blank to default to one year out)</small><input type="text" name="secExpires" value="{{.Cfg.SecurityExpires}}" placeholder="2027-01-01"></label>
<label>Encryption <small>(optional PGP key URL)</small><input type="text" name="secEncryption" value="{{.Cfg.SecurityEncryption}}" placeholder="https://example.com/pgp-key.txt"></label>
<label>Policy <small>(optional security-policy URL)</small><input type="text" name="secPolicy" value="{{.Cfg.SecurityPolicy}}" placeholder="https://example.com/security-policy"></label>
<label>Acknowledgments <small>(optional hall-of-fame URL)</small><input type="text" name="secAck" value="{{.Cfg.SecurityAcknowledgments}}" placeholder="https://example.com/thanks"></label>
<label>Preferred-Languages <small>(optional, comma-separated, e.g. <code>en, de</code>)</small><input type="text" name="secLangs" value="{{.Cfg.SecurityLanguages}}" placeholder="en"></label>
</fieldset>
<fieldset>
<legend>Embeds</legend>
<label>Allowed embed hosts <small>(one per line or comma-separated; e.g. <code>peertube.example</code>). Generic embed blocks may only frame these hosts — the build refuses others and writes exactly these into the served-site CSP <code>frame-src</code> (§5.8).</small><textarea name="embedHosts" rows="4" placeholder="peertube.example&#10;player.vimeo.com">{{.EmbedHosts}}</textarea></label>
</fieldset>
<fieldset>
<legend>Navigation</legend>
<label>Header nav links <small>(one per line as <code>Label | /path</code>; rendered as the site header on every built page)</small><textarea name="nav" rows="4" placeholder="Home | /&#10;Blog | /blog/&#10;Privacy | /privacy/">{{.Nav}}</textarea></label>
<label>Footer nav links <small>(same format; rendered pipe-separated and centered in the footer's first row)</small><textarea name="footerNav" rows="3" placeholder="Privacy | /privacy/&#10;Contact | /contact/&#10;RSS | /feeds/blog.rss">{{.FooterNav}}</textarea></label>
</fieldset>
<fieldset>
<legend>Header brand</legend>
<p><small>Shown at the start of the header on every page, linking to the home page. Logos are self-hosted from your <a href="/admin/media">Media library</a> — no third-party requests.</small></p>
<label>Brand style <small class="hint"><strong>Text</strong> = a wordmark · <strong>Logo</strong> = an image · <strong>Logo + text</strong> = both · <strong>None</strong> = no brand.</small>
<select name="headerBrand">
<option value="text"{{if eq .HeaderBrand "text"}} selected{{end}}>Text (wordmark)</option>
<option value="logo"{{if eq .HeaderBrand "logo"}} selected{{end}}>Logo (image)</option>
<option value="logotext"{{if eq .HeaderBrand "logotext"}} selected{{end}}>Logo + text</option>
<option value="none"{{if eq .HeaderBrand "none"}} selected{{end}}>None</option>
</select></label>
<label>Alignment
<select name="headerAlign">
<option value="start"{{if eq .HeaderAlign "start"}} selected{{end}}>Start (brand left, nav right)</option>
<option value="center"{{if eq .HeaderAlign "center"}} selected{{end}}>Centered (brand above nav)</option>
</select></label>
<label>Brand text <small class="hint">Wordmark for the Text and Logo + text styles. Blank uses your site name.</small><input name="brandText" value="{{.BrandText}}" placeholder="{{.Cfg.SiteName}}"></label>
<label>Logo image <small class="hint">A Media-library path, e.g. <code>/media/&lt;sha&gt;.svg</code> (SVGs are sanitised on upload). Used by the Logo and Logo + text styles.</small><input name="logoSrc" value="{{.LogoSrc}}" placeholder="/media/…"></label>
<label>Dark-mode logo <small class="hint">Optional. A second Media-library logo shown when the page is in dark mode (OS preference or the footer theme toggle). Leave blank to use the logo above for both themes.</small><input name="logoSrcDark" value="{{.LogoSrcDark}}" placeholder="/media/…"></label>
<label>Logo alt text <small class="hint">Describes the logo for screen readers — required when the logo shows on its own (Logo style).</small><input name="logoAlt" value="{{.LogoAlt}}" placeholder="{{.Cfg.SiteName}}"></label>
<label>Logo height
<select name="logoHeight">
<option value="small"{{if eq .LogoHeight "small"}} selected{{end}}>Small (24px)</option>
<option value="medium"{{if eq .LogoHeight "medium"}} selected{{end}}>Medium (32px)</option>
<option value="large"{{if eq .LogoHeight "large"}} selected{{end}}>Large (44px)</option>
</select></label>
</fieldset>
<fieldset>
<legend>Feeds</legend>
<label>Syndication feeds <small>(one per line as <code>name | /glob/* | Optional Title | list</code>). Published pages whose path matches the glob (<code>/blog/*</code> = everything under <code>/blog/</code>) are emitted to <code>/feeds/&lt;name&gt;.rss</code> and <code>/feeds/&lt;name&gt;.atom</code>; matching pages get feed auto-discovery links. Add a trailing <code>list</code> to show the feed on the browsable <code>/feeds/</code> index (leave the title empty as <code>name | /glob/* | | list</code> to list it without a custom title). Needs a Base URL.</small><textarea name="feeds" rows="3" placeholder="blog | /blog/* | Blog | list&#10;news | /news/*">{{.Feeds}}</textarea></label>
</fieldset>
<fieldset>
<legend>Classification report</legend>
<p><small>A user-facing <code>/classification</code> page explains how external-link privacy grades are computed (the <a href="/admin/classification">pbc-classification</a> rating system) and carries a disclaimer — always published.</small></p>
<label class="check"><input type="checkbox" name="classifyReport" value="1"{{if .ClassifyReport}} checked{{end}}> Publish Classification Report Details <small>(also lists every domain in your dataset with its grade, and publishes <code>/.well-known/pbc-classification/domains.json</code>. Off = the report explains the system but exposes no dataset.)</small></label>
<label>Dataset repository URL <small>(optional; linked from the report, e.g. your <code>pbc-domain-list</code> fork)</small><input type="url" name="classifyDataRepo" value="{{.ClassifyDataRepo}}" placeholder="https://github.com/&lt;you&gt;/pbc-domain-list"></label>
</fieldset>
<fieldset>
<legend>Theme</legend>
<p><small>Overrides layer over the built-in theme, which stays the fallback. Leave a field blank to keep the default. External <code>url()</code> and <code>@import</code> are rejected to keep the site self-hosted (§6.4).</small></p>
<label>Body font <small class="hint">Curated <strong>system-font</strong> stacks — nothing is downloaded, so choosing one adds no third-party requests. Code stays monospace.</small>
<select name="font">
{{range .Fonts}}<option value="{{.ID}}"{{if eq $.Font .ID}} selected{{end}}>{{.Label}}</option>{{end}}
</select></label>
{{range .ThemeVars}}
<label>{{.Label}}<input name="var_{{.Field}}" value="{{.Value}}" placeholder="{{.Placeholder}}"></label>
{{end}}
<label>Custom CSS <small>(advanced; self-hosted only)</small><textarea name="customCSS" rows="6" placeholder=".pbcssg-consent-card { border-radius: 12px; }">{{.CustomCSS}}</textarea></label>
</fieldset>
<button type="submit">Save settings</button>
</form>
{{template "bottom"}}{{end}}

{{define "build"}}{{template "top" .}}
<h1>{{if .Published}}Published live{{else if .Release}}Release packaged{{else}}Site generated{{end}}</h1>
<p>Wrote {{len .Report.Files}} file(s) to <code>{{.OutDir}}</code>. Version <code>{{.Version}}</code> · release <code>{{.BuildNumber}}</code>.</p>
{{if .Published}}<p class="alert ok">Published live: <code>{{.Published}}</code> (release {{.BuildNumber}}). The <code>current</code> symlink was repointed and the public listener reloaded in-process — no restart (§7.9).</p>{{end}}
{{if .Pruned}}<p class="alert ok">Pruned {{len .Pruned}} old release director{{if eq (len .Pruned) 1}}y{{else}}ies{{end}}: <code>{{range $i, $n := .Pruned}}{{if $i}}, {{end}}{{$n}}{{end}}</code> (§7.4).</p>{{end}}
{{if .PruneWarn}}<div class="alert warn"><strong>Release pruning skipped:</strong> {{.PruneWarn}} (the publish itself succeeded).</div>{{end}}
{{if .Release}}<p class="alert ok">Release tarball: <code>{{.Release}}</code> (release {{.BuildNumber}}). Copy it to the host and swap the <code>current</code> symlink (§7.4).</p>{{end}}
{{if .Report.Warnings}}
<div class="alert warn"><strong>Warnings</strong><ul>{{range .Report.Warnings}}<li>{{.}}</li>{{end}}</ul></div>
{{end}}
<div class="toolbar">
<form method="post" action="/build" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="btn">Regenerate</button></form>
{{if .Publisher}}<form method="post" action="/admin/publish" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit" class="btn">Publish live ↑</button></form>{{end}}
<form method="post" action="/admin/release" class="inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Package release ↑</button></form>
</div>
<table class="grid">
<thead><tr><th>Page</th><th>Worst grade</th><th>External</th><th>Warnings</th></tr></thead>
<tbody>
{{range .Report.Pages}}
<tr><td>{{$id := index $.PageIDs .Path}}{{if $id}}<a href="/pages/{{$id}}" title="Edit this page"><code>{{.Path}}</code></a>{{else}}<code>{{.Path}}</code>{{end}}</td><td>{{if .WorstGrade}}<span class="grade {{gradeClass .WorstGrade}}" title="Worst grade {{.WorstGrade}}">{{.WorstGrade}}</span>{{else}}—{{end}}</td><td>{{.Externals}}</td>
<td>{{range .Warnings}}{{.}}<br>{{end}}</td></tr>
{{end}}
</tbody>
</table>
<p><a class="btn" href="/">Back to pages</a></p>
{{template "bottom"}}{{end}}

{{define "classification"}}{{template "top" .}}
<h1>Classification dataset</h1>
<p class="hint">Custom privacy classifications for external domains, <strong>merged over the built-in defaults</strong> (your entries add to or override them). Used by the live badges and the build, and published at <code>/.well-known/pbc-classification/domains.json</code> for transparency. Grades update live as you edit.</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
<div class="toolbar classify-io">
<a class="btn" href="/admin/classification/export" download="domains.json">Export domains.json</a>
<form method="post" action="/admin/classification/import" enctype="multipart/form-data" class="classify-import" onsubmit="return confirm('Import this file? It replaces the current classification dataset.')">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label class="classify-import-label">Import <input type="file" name="file" accept=".json,application/json" required></label>
<button type="submit" class="btn">Import</button>
</form>
</div>
<form method="post" action="/admin/classification" id="classify-form">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<div id="classify-editor" class="classify-editor" aria-live="polite"></div>
<div class="toolbar">
<button type="button" class="btn" id="classify-add">+ Add domain</button>
<span id="classify-status" class="classify-status" role="status"></span>
</div>
<details class="classify-raw-wrap"{{if .Error}} open{{end}}>
<summary>Raw JSON (advanced / no-JavaScript)</summary>
<p class="hint">One entry per domain: <code>trust</code> (unknown/imported/audited/own), a <code>verified</code> date (required for audited/imported), <code>signals</code>, and optional <code>evidence</code>/<code>note</code>. Saved in canonical form. <strong>Third-party ad cookies</strong> means cross-site advertising/tracking cookies — not benign first-party analytics. <strong>Honours GPC</strong> is a booster and only affects the grade for sites that sell/share or run ad-tracking (a site with nothing to opt out of has nothing to honour).</p>
<textarea name="dataset" id="classify-raw" rows="14" spellcheck="false" placeholder='{"tracker.example": {"trust": "audited", "verified": "2026-01-01", "signals": {"adTrackingCookies": "yes", "sellsSharesData": "yes", "honorsGPC": "no"}}}'>{{.Raw}}</textarea>
</details>
<div class="toolbar">
<button type="submit">Save dataset</button>
</div>
</form>
<script src="/admin/assets/classify.js" defer></script>
{{template "bottom"}}{{end}}

{{define "keygroups"}}{{template "top" .}}
<h1>Key groups</h1>
<p class="hint">Named groups whose key unlocks <strong>group-gated content blocks</strong> (§6.10). A visitor who opens a group's <em>gate link</em> once unlocks every block that group authorizes, across the whole site. This is <strong>group gating with a shared bearer key — not a per-user login</strong>: anyone who receives the link holds the key until you rotate it, and there is no identity, expiry, or audit. Keys are delivered only by link (never typed) and only in the URL fragment, so no server or log ever sees them.</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{if .LocalTestURL}}<p class="hint">Local testing against <code>{{.LocalTestURL}}</code> — each gate link below has a <strong>Local Test ↗</strong> button that opens it there. Build the site and run <code>pbcssg server</code> first.</p>{{else}}<p class="hint">Tip: set a <a href="/admin/settings">Local server test URL</a> to get a “Local Test” button on each gate link, for unlocking members content against a local build.</p>{{end}}

<form method="post" action="/admin/keygroups" class="keygroup-create">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label class="block-field">New group alias
<input type="text" name="alias" placeholder="members-a" required></label>
<button type="submit">Create key group</button>
</form>

{{if .Groups}}
<table class="grid keygroups">
<thead><tr><th>Alias</th><th>Splash page &amp; gate link</th><th>Actions</th></tr></thead>
<tbody>
{{range $g := .Groups}}
<tr>
<td>
<form method="post" action="/admin/keygroups/{{$g.ID}}/rename" class="inline keygroup-rename">
<input type="hidden" name="csrf" value="{{$.CSRF}}">
<input type="text" name="alias" value="{{$g.Alias}}" aria-label="Rename group">
<button type="submit" class="btn">Rename</button>
</form>
</td>
<td>
<form method="post" action="/admin/keygroups/{{$g.ID}}/splash" class="inline keygroup-splash">
<input type="hidden" name="csrf" value="{{$.CSRF}}">
<label class="sr-only" for="splash-{{$g.ID}}">Splash page</label>
<select id="splash-{{$g.ID}}" name="splash">
<option value="0"{{if eq $g.SplashID 0}} selected{{end}}>— none (generic confirmation) —</option>
{{range $.Pages}}<option value="{{.ID}}"{{if eq .ID $g.SplashID}} selected{{end}}>{{.Path}}</option>{{end}}
</select>
<button type="submit" class="btn">Set</button>
</form>
<p class="keygroup-link"><label class="sr-only" for="link-{{.ID}}">Gate link</label><input id="link-{{.ID}}" type="text" class="gate-link" value="{{.GateLink}}" readonly><button type="button" class="btn copy-link" data-copy="{{.GateLink}}">Copy</button>{{if .LocalLink}} <a class="btn local-test" href="{{.LocalLink}}" target="_blank" rel="noopener">Local Test ↗</a>{{end}}</p>
{{if .Generic}}<p class="hint">Using the built-in <code>/unlock/{{.Alias}}</code> confirmation page. Pick a splash page above to send members to your own welcome page instead.</p>{{end}}
</td>
<td class="keygroup-actions">
<form method="post" action="/admin/keygroups/{{.ID}}/rotate" class="inline" onsubmit="return confirm('Rotate this key? Every gate link you have shared for this group will stop working — you must rebuild and re-share the new link.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit" class="btn">Rotate key</button></form>
<form method="post" action="/admin/keygroups/{{.ID}}/delete" class="inline" onsubmit="return confirm('Delete this key group? Blocks that only this group could unlock become unreadable to everyone until re-authorized.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit" class="danger">Delete</button></form>
</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="hint">No key groups yet. Create one above, then list its alias in a block's “Members-only groups” field to gate that block.</p>
{{end}}
<script src="/admin/assets/copy.js" defer></script>
{{template "bottom"}}{{end}}

{{define "moderation"}}{{template "top" .}}
<h1>Moderation</h1>
<nav class="media-tabs" aria-label="Moderation sections">
<a class="media-tab active" href="/admin/moderation">Comments{{if .Count}} <span class="media-count">{{.Count}}</span>{{end}}</a>
<a class="media-tab" href="/admin/moderation/accounts">Accounts</a>
</nav>
<p class="hint">Comments members post arrive <strong>pending</strong> — nothing is public until you approve it (§7.3). The widget reads approved comments live, so an action takes effect immediately (no rebuild). Filter by status and search below; a <strong>pending</strong> row offers Approve / Reject / Delete, an <strong>approved</strong> row Unpublish / Delete, a <strong>rejected</strong> row Restore / Delete. Use <strong>Reply</strong> on a row to answer as the author (auto-approved). Deleting a comment that has replies removes those replies too (§7.3).</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{if .Disabled}}
<p class="hint">Comment moderation needs the runtime store. Start the editor with <code>-app-db</code> and an admin origin to enable member accounts and comments (§2.4).</p>
{{else}}
<details class="mod-compose">
<summary>Comment as the author</summary>
<p class="hint">Posted as the author and <strong>auto-approved</strong> (live immediately), shown with the <strong>Author</strong> badge{{if .CreatorAlias}} as <strong>{{.CreatorAlias}}</strong>{{else}} — set a display name below so you are not “Anonymous”{{end}}. To answer a specific comment, use its <strong>Reply</strong> action in the table.</p>
<form method="post" action="/admin/moderation/comment" class="stack">
<input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="ctx" value="{{.FilterCtx}}">
<label>Page path <input type="text" name="page" placeholder="/posts/hello" required></label>
<label>Comment <textarea name="body" rows="3" maxlength="4096" placeholder="Write a comment as the author…" required></textarea></label>
<button type="submit" class="btn">Post comment</button>
</form>
<form method="post" action="/admin/moderation/identity" class="inline">
<input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="ctx" value="{{.FilterCtx}}">
<label>Display name <input type="text" name="alias" value="{{.CreatorAlias}}" maxlength="64" placeholder="blank = Anonymous"></label>
<button type="submit" class="btn">Save name</button>
</form>
</details>
<form method="get" action="/admin/moderation" class="mod-filter">
<label>Status
<select name="status">
<option value="pending"{{if eq .Filter.Status "pending"}} selected{{end}}>Pending</option>
<option value="approved"{{if eq .Filter.Status "approved"}} selected{{end}}>Approved</option>
<option value="rejected"{{if eq .Filter.Status "rejected"}} selected{{end}}>Rejected</option>
</select></label>
<label>Page <input type="text" name="q_page" value="{{.Filter.Page}}" placeholder="path contains…"></label>
<label>Author <input type="text" name="q_author" value="{{.Filter.Author}}" placeholder="name contains…"></label>
<label>Comment <input type="text" name="q_body" value="{{.Filter.Body}}" placeholder="text contains…"></label>
<label>From <input type="date" name="from" value="{{.Filter.From}}"></label>
<label>To <input type="date" name="to" value="{{.Filter.To}}"></label>
<label>Sort
<select name="sort">
<option value="posted"{{if eq .Filter.Sort "posted"}} selected{{end}}>Posted date</option>
<option value="page"{{if eq .Filter.Sort "page"}} selected{{end}}>Page</option>
<option value="author"{{if eq .Filter.Sort "author"}} selected{{end}}>Author</option>
</select></label>
<label>Order
<select name="dir">
<option value="desc"{{if eq .Filter.Dir "desc"}} selected{{end}}>Descending</option>
<option value="asc"{{if eq .Filter.Dir "asc"}} selected{{end}}>Ascending</option>
</select></label>
<button type="submit" class="btn">Filter</button>
<a class="linklike" href="/admin/moderation">Reset</a>
</form>
{{if .Rows}}
<p class="hint">Showing {{.RangeStart}}–{{.RangeEnd}} of {{.Total}} {{.Filter.Status}} comment{{if ne .Total 1}}s{{end}}.</p>
<table class="grid moderation">
<thead><tr><th>Page</th><th>Author</th><th>Comment</th><th>Posted</th><th>Status</th><th>Actions</th></tr></thead>
<tbody>
{{range .Rows}}
<tr>
<td class="comment-page">{{if .PageURL}}<a href="{{.PageURL}}" target="_blank" rel="noopener"><code>{{.PagePath}}</code></a>{{else}}<code>{{.PagePath}}</code>{{end}}</td>
<td class="comment-alias">{{.Alias}}</td>
<td class="comment-body">{{if .IsReply}}<div class="reply-to hint">↳ in reply to {{.ReplyToAlias}}</div>{{end}}{{.Body}}</td>
<td class="comment-time">{{.Created}}</td>
<td class="comment-status">{{.Status}}</td>
<td class="comment-actions row-actions">
{{if eq .Status "pending"}}
<form method="post" action="/admin/moderation/comments/{{.ID}}/approve" class="inline"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Approve</button></form>
<form method="post" action="/admin/moderation/comments/{{.ID}}/reject" class="inline"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Reject</button></form>
<form method="post" action="/admin/moderation/comments/{{.ID}}/delete" class="inline" onsubmit="return confirm('{{if .HasReplies}}Delete this comment and its {{.ReplyCount}} repl{{if eq .ReplyCount 1}}y{{else}}ies{{end}}? The replies are removed with it (§7.3). {{end}}This cannot be undone.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Delete</button></form>
{{else if eq .Status "approved"}}
<form method="post" action="/admin/moderation/comments/{{.ID}}/reject" class="inline" onsubmit="return confirm('Unpublish this comment? It will be hidden from the page.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Unpublish</button></form>
<form method="post" action="/admin/moderation/comments/{{.ID}}/delete" class="inline" onsubmit="return confirm('{{if .HasReplies}}Delete this comment and its {{.ReplyCount}} repl{{if eq .ReplyCount 1}}y{{else}}ies{{end}}? The replies are removed with it (§7.3). {{end}}This cannot be undone.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Delete</button></form>
{{else}}
<form method="post" action="/admin/moderation/comments/{{.ID}}/approve" class="inline" onsubmit="return confirm('Restore this comment? It will be published on the page.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Restore</button></form>
<form method="post" action="/admin/moderation/comments/{{.ID}}/delete" class="inline" onsubmit="return confirm('{{if .HasReplies}}Delete this comment and its {{.ReplyCount}} repl{{if eq .ReplyCount 1}}y{{else}}ies{{end}}? The replies are removed with it (§7.3). {{end}}This cannot be undone.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Delete</button></form>
{{end}}
{{if and (not .IsReply) (not .Deleted)}}<details class="mod-reply"><summary>Reply</summary><form method="post" action="/admin/moderation/comments/{{.ID}}/reply" class="stack"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><textarea name="body" rows="2" maxlength="4096" placeholder="Reply as the author…" required></textarea><button type="submit" class="btn">Post reply</button></form></details>{{end}}
{{if .CanBan}}<form method="post" action="/admin/moderation/comments/{{.ID}}/ban-author" class="inline" onsubmit="return confirm('Ban {{if .Alias}}{{.Alias}}{{else}}the author{{end}}? Their sessions end and the creating invite is burned. Comments are kept (erase separately).')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger" title="Ban this comment's author">Ban author</button></form>{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
{{if gt .TotalPages 1}}
<nav class="pager" aria-label="Pagination">
{{if .PrevURL}}<a class="btn" href="{{.PrevURL}}" rel="prev">← Prev</a>{{else}}<span class="btn" aria-disabled="true">← Prev</span>{{end}}
<span>Page {{.PageNo}} of {{.TotalPages}}</span>
{{if .NextURL}}<a class="btn" href="{{.NextURL}}" rel="next">Next →</a>{{else}}<span class="btn" aria-disabled="true">Next →</span>{{end}}
</nav>
{{end}}
{{else}}
<p class="hint">No {{.Filter.Status}} comments match these filters.</p>
{{end}}
{{end}}
{{template "bottom"}}{{end}}

{{define "modaccounts"}}{{template "top" .}}
<h1>Moderation</h1>
<nav class="media-tabs" aria-label="Moderation sections">
<a class="media-tab" href="/admin/moderation">Comments</a>
<a class="media-tab active" href="/admin/moderation/accounts">Accounts</a>
</nav>
<p class="hint">Member and moderator accounts (§2.4). Identity is a passkey, so the store holds no personal data — only an opaque handle, role, status, and dates. The <strong>Permissions</strong> column sets what a <em>moderator</em> may do — mint member invites (<em>can invite</em>) and soft-ban members (<em>can ban</em>) — which is separate from banning the account itself. <strong>Ban</strong> flags the account, signs it out everywhere, and burns the invite that created it; <strong>Un-ban</strong> lets it sign in again (the old invite stays burned). <strong>Erase</strong> deletes the account entirely. Both Ban and Erase offer the same <em>also delete their comments</em> option — leave it unchecked to keep the comments (Erase anonymizes them); tick it to remove them. The difference between the two actions is the account's fate (flagged-banned vs. gone), not what happens to the comments. Creators are managed at sign-in, not here.</p>
<p class="hint">For a <strong>moderator</strong>: set a <em>label</em> so you can tell them apart, grant <em>invite</em> / <em>ban</em> permissions (default off — their base role is comment moderation only), or <em>revoke invites</em> to burn every invite they have outstanding. Members show <em>who invited them</em>, so you can trace a wave of accounts back to a moderator.</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{if .Disabled}}
<p class="hint">Account moderation needs the runtime store. Start the editor with <code>-app-db</code> and an admin origin to enable member accounts (§2.4).</p>
{{else if .Accounts}}
<table class="grid modaccounts">
<thead><tr><th>Account</th><th>Name</th><th>Role</th><th>Status</th><th>Comments</th><th>Created</th><th>Last seen</th><th>Permissions</th><th>Actions</th></tr></thead>
<tbody>
{{range .Accounts}}
<tr>
<td class="acct-handle"><code>{{.Handle}}</code> <span class="hint">#{{.ID}}</span>
{{if .IsModerator}}
<form method="post" action="/admin/moderation/accounts/{{.ID}}/label" class="inline acct-label"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="text" name="label" value="{{.Label}}" placeholder="label" maxlength="120"><button type="submit" class="btn btn-sm">Name</button></form>
{{else if .InvitedBy}}
<div class="hint">invited by {{.InvitedBy}}</div>
{{end}}
</td>
<td class="acct-alias">{{if .Alias}}{{.Alias}}{{else}}<span class="hint">(anonymous)</span>{{end}}</td>
<td>{{.Role}}</td>
<td>{{if .Banned}}<span class="status banned">banned</span>{{else}}<span class="status">active</span>{{end}}</td>
<td>{{.Comments}}</td>
<td>{{.Created}}</td>
<td>{{.LastSeen}}</td>
<td class="acct-perms">
{{if .IsModerator}}
<form method="post" action="/admin/moderation/accounts/{{.ID}}/capabilities" class="inline"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label class="check" title="May mint member invites"><input type="checkbox" name="can_invite" value="1"{{if .CanInvite}} checked{{end}}> can invite</label><label class="check" title="May soft-ban members"><input type="checkbox" name="can_ban" value="1"{{if .CanBan}} checked{{end}}> can ban</label><button type="submit" class="btn btn-sm">Save</button></form>
<form method="post" action="/admin/moderation/accounts/{{.ID}}/revoke-invites" class="inline" onsubmit="return confirm('Revoke all outstanding invites from this moderator?')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit" class="btn btn-sm">Revoke invites</button></form>
{{else}}<span class="hint">—</span>{{end}}
</td>
<td class="acct-actions">
{{if .Banned}}
<form method="post" action="/admin/moderation/accounts/{{.ID}}/unban" class="inline"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit" class="btn">Un-ban</button></form>
{{else}}
<form method="post" action="/admin/moderation/accounts/{{.ID}}/ban" class="inline" onsubmit="return confirm('Ban this account? Its sessions end now and the invite that created it is burned.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label class="check" title="Hard-delete this account's comments as part of the ban (leave unchecked to keep them)"><input type="checkbox" name="remove" value="1"> also delete their comments</label><button type="submit" class="btn">Ban</button></form>
{{end}}
<form method="post" action="/admin/moderation/accounts/{{.ID}}/erase" class="inline" onsubmit="return confirm('Erase this account permanently? This cannot be undone.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label class="check" title="Delete their comments too; unchecked keeps them, anonymized"><input type="checkbox" name="delete" value="1"> also delete their comments</label><button type="submit" class="danger">Erase</button></form>
</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="hint">No member or moderator accounts yet. They appear here once someone registers with an invite.</p>
{{end}}
{{template "bottom"}}{{end}}

{{define "passkeys"}}{{template "top" .}}
<h1>Passkeys</h1>
<p class="hint">Your account signs in with <strong>passkeys</strong> (WebAuthn) — no password. Keep <strong>at least two</strong>, on separate devices (§2.4): a lost passkey has no recovery, so a second key is your way back in. Register each on the device that holds it.</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{if .Disabled}}
<p class="hint">Passkey management needs the runtime store. Start the editor with <code>-app-db</code> and an admin origin (§2.4).</p>
{{else}}
<fieldset class="passkey-add">
<legend>Add a passkey</legend>
<input type="hidden" id="csrf" value="{{.CSRF}}">
<label for="passkey-label">Label (optional — which device this is)</label>
<input type="text" id="passkey-label" maxlength="120" placeholder="e.g. YubiKey 5, or laptop Touch ID">
<button type="button" id="add-passkey-btn" class="btn">Add a passkey</button>
<span id="add-status" class="hint"></span>
<p class="hint">Adding on a device that already holds a key for this account will be refused — register the new key on a <em>different</em> authenticator.</p>
</fieldset>
{{if .Passkeys}}
<table class="grid passkeys">
<thead><tr><th>Label</th><th>Transports</th><th>Added</th><th>Last used</th><th>Actions</th></tr></thead>
<tbody>
{{range .Passkeys}}
<tr>
<td class="pk-label"><form method="post" action="/admin/passkeys/{{.ID}}/label" class="inline pk-rename"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="text" name="label" value="{{.Label}}" maxlength="120" aria-label="Rename passkey"><button type="submit" class="btn">Rename</button></form></td>
<td>{{if .Transports}}{{.Transports}}{{else}}—{{end}}</td>
<td>{{.Created}}</td>
<td>{{.LastUsed}}</td>
<td class="pk-actions">{{if $.OnlyOne}}<span class="hint">Your only key</span>{{else}}<form method="post" action="/admin/passkeys/{{.ID}}/delete" class="inline" onsubmit="return confirm('Remove this passkey? Make sure you still have another you can sign in with.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit" class="danger">Remove</button></form>{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
{{if .OnlyOne}}<p class="hint"><strong>You have one passkey.</strong> Add a second on another device so a lost or broken authenticator doesn't lock you out.</p>{{end}}
{{else}}
<p class="hint">No passkeys on this account yet.</p>
{{end}}
{{end}}
<script src="/admin/assets/passkeys.js" defer></script>
{{template "bottom"}}{{end}}

{{define "invites"}}{{template "top" .}}
<h1>Invites</h1>
<p class="hint">Single-use registration codes (§2.4). Choose a <strong>role</strong> and expiry, then send the code to the person out-of-band. A code is <strong>shown once</strong> — only its hash is stored, so it cannot be shown again; if it is lost, revoke it and mint a new one. Give a moderator invite a <strong>label</strong> so you can tell them apart later.</p>
<p class="hint"><strong>Where each role registers:</strong> <em>members</em> and <em>moderators</em> register on the <strong>public site</strong> — members from a page's comment box, moderators at their sign-in page <a href="{{.ModerateURL}}"><code>{{.ModerateURL}}</code></a> (send this link with a moderator invite). Only <em>creators</em> register here on the admin origin. A moderator invite will not redeem in the comment box, and vice-versa.</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{if .Disabled}}
<p class="hint">Invites need the runtime store. Start the editor with <code>-app-db</code> and an admin origin (§2.4).</p>
{{else}}
{{if .MintedCode}}
<div class="invite-code">
<p><strong>New invite code</strong> — copy it now, it will not be shown again:</p>
<p><input type="text" class="minted-code" value="{{.MintedCode}}" readonly aria-label="New invite code"><button type="button" class="btn copy-link" data-copy="{{.MintedCode}}">Copy</button></p>
</div>
{{end}}
<form method="post" action="/admin/invites" class="invite-mint">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="invite-role">Role</label>
<select id="invite-role" name="role">{{range .Roles}}<option value="{{.}}">{{.}}</option>{{end}}</select>
<label for="invite-ttl">Expires</label>
<select id="invite-ttl" name="ttl">{{range .TTLs}}<option value="{{.Key}}">{{.Label}}</option>{{end}}</select>
<label for="invite-label">Label</label>
<input type="text" id="invite-label" name="label" maxlength="120" placeholder="e.g. Alice (moderators only)">
<button type="submit" class="btn">Mint invite</button>
</form>
{{if .Invites}}
<table class="grid invites">
<thead><tr><th>Role</th><th>Status</th><th>Created</th><th>Expires</th><th>Actions</th></tr></thead>
<tbody>
{{range .Invites}}
<tr>
<td>{{.Role}}</td>
<td><span class="status{{if eq .Status "revoked"}} banned{{end}}">{{.Status}}</span></td>
<td>{{.Created}}</td>
<td>{{.Expires}}</td>
<td class="invite-actions">{{if .Live}}<form method="post" action="/admin/invites/revoke" class="inline" onsubmit="return confirm('Revoke this invite? The code can no longer be redeemed.')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="lineage" value="{{.Lineage}}"><button type="submit" class="danger">Revoke</button></form>{{else}}—{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="hint">No invites yet. Mint one above to enroll a member, moderator, or another creator.</p>
{{end}}
{{end}}
<script src="/admin/assets/copy.js" defer></script>
{{template "bottom"}}{{end}}

{{define "errorpages"}}{{template "top" .}}
<h1>Error pages</h1>
<p class="hint">Themed <strong>404 / 403 / 429 / 50x</strong> pages emitted at the site root and served by your reverse proxy's <code>error_page</code> directive (§7.8). Each reuses the site theme and includes a link home. Write <strong>Markdown</strong>; leave a box <strong>blank to use the built-in default</strong>. Codes with no page body (1xx, 204, 205, 304) are intentionally not listed.</p>
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
<form method="post" action="/admin/errorpages">
<input type="hidden" name="csrf" value="{{.CSRF}}">
{{range .Pages}}
<fieldset>
<legend>{{.Title}} <small>(HTTP {{.Codes}} → <code>/{{.Name}}.html</code>)</small></legend>
<label>Message <small class="hint">Markdown; blank uses the default.</small><textarea name="msg_{{.Name}}" rows="6" spellcheck="true">{{.Message}}</textarea></label>
</fieldset>
{{end}}
<div class="toolbar"><button type="submit">Save error pages</button></div>
</form>
{{template "bottom"}}{{end}}

{{define "favicon"}}{{template "top" .}}
<h1>Favicon &amp; app icons</h1>
<p class="hint">Site icons served from the root and wired into every page's <code>&lt;head&gt;</code> — no manual link tags. Upload the files from your branding kit (<code>build-favicons.py</code>): each slot is optional, and a tag is emitted only for what you provide. SVG is sanitized and PNG metadata stripped on upload. The web manifest is generated for you from the PWA icons + site name + theme colour.</p>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}

<form id="fav-upload" method="post" action="/admin/favicon" enctype="multipart/form-data"><input type="hidden" name="csrf" value="{{.CSRF}}"></form>
<table class="grid">
<thead><tr><th>Icon</th><th>Current</th><th>Replace</th><th></th></tr></thead>
<tbody>
{{range .Slots}}
<tr>
<td><strong>{{.Label}}</strong><div class="hint">{{.Hint}}</div><code>/{{.Name}}</code></td>
<td>{{if .Present}}<img src="{{.SrcURL}}" alt="" style="height:48px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:6px;padding:4px">{{else}}<span class="hint">— none —</span>{{end}}</td>
<td><input type="file" name="{{.Field}}" accept="{{.Accept}}" form="fav-upload"></td>
<td>{{if .Present}}<form method="post" action="/admin/favicon/{{.Name}}/delete" class="inline" onsubmit="return confirm('Remove {{.Name}}?')"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button type="submit" class="danger">Remove</button></form>{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
<div class="toolbar" style="margin-top:1rem">
<label>Theme colour <small>(optional; <code>&lt;meta name="theme-color"&gt;</code> + manifest colour, e.g. <code>#0d9488</code>)</small><input type="text" name="themeColor" value="{{.ThemeColor}}" placeholder="#0d9488" form="fav-upload"></label>
</div>
<div class="toolbar">
<button type="submit" form="fav-upload">Save favicons</button>
</div>
<p class="hint">Emitted per uploaded asset: <code>&lt;link rel="icon" href="/favicon.ico" sizes="any"&gt;</code>, <code>&lt;link rel="icon" href="/favicon.svg" type="image/svg+xml"&gt;</code>, <code>&lt;link rel="apple-touch-icon" href="/apple-touch-icon.png"&gt;</code>{{if .HasManifest}}, <code>&lt;link rel="manifest" href="/site.webmanifest"&gt;</code>{{end}}. Rebuild the site to publish changes.</p>
{{template "bottom"}}{{end}}

{{define "metrics"}}{{template "top" .}}
<h1>Metrics</h1>
<p class="hint">Aggregate counters over the last {{.M.Window}}. No client IP is stored; nothing here identifies a visitor. In-memory only — resets when the server restarts. Operator-only view, served on the admin origin (§7.7).</p>
<section class="metric-cards" aria-label="Summary">
  <div class="metric-card"><div class="k">Requests</div><div class="v">{{.M.Hits}}</div></div>
  <div class="metric-card"><div class="k">Bytes sent</div><div class="v">{{.M.BytesHuman}}</div></div>
  <div class="metric-card"><div class="k">Cache hits</div><div class="v">{{.M.CacheRatio}}%</div></div>
  <div class="metric-card"><div class="k">On-grid /16s</div><div class="v">{{.M.GridActive}}</div></div>
  <div class="metric-card"><div class="k">IPv6</div><div class="v">{{.M.IPv6Pct}}%</div></div>
  <div class="metric-card"><div class="k">Private/unknown</div><div class="v">{{.M.PrivatePct}}%</div></div>
</section>
<figure class="heat-figure">
  <img class="heat" src="/admin/metrics/heatmap.png" width="256" height="256"
    alt="Heat map of inbound requests by /16 network over the last {{.M.Window}}: y axis is the first address octet, x axis the second; brighter cells are busier networks; the grey band along the bottom is reserved / non-routable space.">
  <div class="heat-legend"><span>less</span><span class="heat-ramp" role="img" aria-label="intensity ramp, dark to bright"></span><span>more</span>
    &nbsp;&nbsp;<span class="heat-sw"></span><span>reserved / non-routable</span></div>
  <figcaption>Public IPv4 space at /16 granularity ({{.M.OnGrid}} on-grid requests). IPv6 and private/unknown sources are tallied in the cards above, not shown on the grid.</figcaption>
</figure>
<div class="metric-grid2">
  <table class="metrics"><caption>Status classes</caption>
    <thead><tr><th scope="col">Class</th><th scope="col" class="n">Count</th><th scope="col" class="p">%</th></tr></thead>
    <tbody>{{range .M.Status}}<tr><td>{{.Label}}</td><td class="n">{{.Count}}</td><td class="p">{{.Pct}}</td></tr>{{end}}</tbody>
  </table>
  <table class="metrics"><caption>Methods</caption>
    <thead><tr><th scope="col">Method</th><th scope="col" class="n">Count</th><th scope="col" class="p">%</th></tr></thead>
    <tbody>{{range .M.Method}}<tr><td>{{.Label}}</td><td class="n">{{.Count}}</td><td class="p">{{.Pct}}</td></tr>{{end}}</tbody>
  </table>
  <table class="metrics"><caption>Client class</caption>
    <thead><tr><th scope="col">Class</th><th scope="col" class="n">Count</th><th scope="col" class="p">%</th></tr></thead>
    <tbody>{{range .M.UAClass}}<tr><td>{{.Label}}</td><td class="n">{{.Count}}</td><td class="p">{{.Pct}}</td></tr>{{end}}</tbody>
  </table>
</div>
<div class="metric-grid2">
  <table class="metrics"><caption>Top pages</caption>
    <thead><tr><th scope="col">Path</th><th scope="col" class="n">Hits</th></tr></thead>
    <tbody>{{range .M.TopPaths}}<tr><td><code>{{.Path}}</code></td><td class="n">{{.Count}}</td></tr>{{else}}<tr><td colspan="2">no data yet</td></tr>{{end}}</tbody>
  </table>
  <table class="metrics"><caption>Top /16 networks</caption>
    <thead><tr><th scope="col">Network</th><th scope="col" class="n">Hits</th></tr></thead>
    <tbody>{{range .M.TopNets}}<tr><td><code>{{.Net}}</code></td><td class="n">{{.Count}}</td></tr>{{else}}<tr><td colspan="2">no on-grid traffic yet</td></tr>{{end}}</tbody>
  </table>
</div>
<p class="hint">Raw data: <a href="/admin/metrics/metrics.json">/admin/metrics/metrics.json</a> · counters, not events · admin origin only (§7.7).</p>
{{template "bottom"}}{{end}}

{{define "register"}}<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<script src="/admin/assets/theme-toggle.js"></script>
<title>pbcssg — register creator</title>
<link rel="stylesheet" href="/admin/assets/admin.css">
</head><body>
<header class="admin-header row-between">
<span class="brand">pbcssg <span>editor</span></span>
<nav class="admin-nav"><button type="button" class="theme-toggle" data-pbcssg-theme-toggle hidden>◐ Auto</button></nav>
</header>
<main>
<h1>Register a creator passkey</h1>
<p>Redeem your one-time <strong>creator invite</strong> (from <code>pbcssg admin bootstrap</code>) and register a passkey for this admin. You'll be asked to verify with your authenticator.</p>
<form id="register-form" autocomplete="off">
<input type="hidden" id="csrf" value="{{.CSRF}}">
<p><label for="invite">Invite code</label><br>
<input type="text" id="invite" name="invite" required autocomplete="off" spellcheck="false" size="44"></p>
<p><button type="submit" id="register-btn" class="btn">Register passkey</button></p>
</form>
<p id="status" role="status" aria-live="polite"></p>
<script src="/admin/assets/register.js"></script>
</main>
</body></html>{{end}}

{{define "login"}}<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<script src="/admin/assets/theme-toggle.js"></script>
<title>pbcssg — sign in</title>
<link rel="stylesheet" href="/admin/assets/admin.css">
</head><body>
<header class="admin-header row-between">
<span class="brand">pbcssg <span>editor</span></span>
<nav class="admin-nav"><button type="button" class="theme-toggle" data-pbcssg-theme-toggle hidden>◐ Auto</button></nav>
</header>
<main>
<h1>Sign in</h1>
<p>Sign in to the editor with your creator passkey.</p>
<input type="hidden" id="csrf" value="{{.CSRF}}">
<p><button type="button" id="login-btn" class="btn">Sign in with passkey</button></p>
<p id="status" role="status" aria-live="polite"></p>
<p class="hint">No passkey yet? <a href="/admin/register">Register with a creator invite</a>.</p>
<script src="/admin/assets/login.js"></script>
</main>
</body></html>{{end}}
`
