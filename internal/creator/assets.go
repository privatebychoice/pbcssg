package creator

import "go.privatebychoice.com/pbcssg/internal/theme"

// themeCSS returns the site theme, served by the editor so previews look like the
// built site.
func themeCSS() string { return theme.CSS }

// adminCSS styles the editor admin (self-hosted, no third-party resources).
const adminCSS = `:root { color-scheme: light dark; --bg:#fff; --fg:#1a1a1a; --muted:#5a5a5a; --accent:#0b5cad; --border:#d8d8d8; --card:#f6f7f9; --danger:#b00020; }
@media (prefers-color-scheme: dark){ :root{ --bg:#14161a; --fg:#e7e9ec; --muted:#a2a8b0; --accent:#6cb2f0; --border:#2b2f36; --card:#1c1f25; --danger:#ff6b6b; } }
/* Explicit Auto/Light/Dark choice from the nav toggle (mirrors the built site). */
:root[data-theme="light"]{ color-scheme:light; --bg:#fff; --fg:#1a1a1a; --muted:#5a5a5a; --accent:#0b5cad; --border:#d8d8d8; --card:#f6f7f9; --danger:#b00020; }
:root[data-theme="dark"]{ color-scheme:dark; --bg:#14161a; --fg:#e7e9ec; --muted:#a2a8b0; --accent:#6cb2f0; --border:#2b2f36; --card:#1c1f25; --danger:#ff6b6b; }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; line-height:1.5; }
.admin-header { border-bottom:1px solid var(--border); padding:0.75rem 1.25rem; }
.brand { font-weight:700; text-decoration:none; color:var(--fg); }
.brand span { color:var(--muted); font-weight:400; }
main { max-width:70rem; margin-inline:auto; padding:1.25rem; }
h1 { font-size:1.5rem; }
a { color:var(--accent); }
code { background:var(--card); padding:0.1em 0.35em; border-radius:4px; }
.row-between { display:flex; align-items:center; justify-content:space-between; gap:1rem; }
.toolbar { display:flex; gap:0.5rem; align-items:center; }
.inline { display:inline; margin:0; }
button, .btn { display:inline-block; font:inherit; cursor:pointer; padding:0.45rem 0.85rem; border-radius:8px; border:1px solid var(--border); background:var(--accent); color:#fff; text-decoration:none; }
.btn { background:var(--card); color:var(--fg); }
button.danger { background:var(--danger); border-color:var(--danger); }
table.grid { width:100%; border-collapse:collapse; margin-top:1rem; }
.grid th, .grid td { text-align:left; padding:0.5rem 0.6rem; border-bottom:1px solid var(--border); vertical-align:top; }
/* Keep the actions cell a normal top-aligned table cell (matching the other
   columns when Title/Path wrap to multiple lines); the link + button are still
   centred relative to each other on their single line. */
.row-actions { white-space:nowrap; }
.row-actions a, .row-actions button { vertical-align:middle; }
.row-actions button { margin-left:0.4rem; }
.grid th a.sort-link { color:var(--fg); text-decoration:none; display:inline-block; }
.grid th a.sort-link:hover { color:var(--accent); text-decoration:underline; }
.moderation .comment-body { white-space:pre-wrap; overflow-wrap:break-word; max-width:42rem; }
.moderation .comment-time { white-space:nowrap; color:var(--muted); }
.modaccounts .acct-actions form, .modaccounts .acct-perms form { display:flex; align-items:center; gap:0.4rem; margin-bottom:0.35rem; flex-wrap:wrap; }
.modaccounts .acct-actions form:last-child, .modaccounts .acct-perms form:last-child { margin-bottom:0; }
.modaccounts .acct-actions .check, .modaccounts .acct-perms .check { font-weight:400; font-size:0.85rem; white-space:nowrap; margin:0; }
.modaccounts .acct-handle .hint { font-size:0.8rem; }
.status.banned { background:var(--danger); color:#fff; border-color:var(--danger); }
.passkey-add { border:1px solid var(--border); border-radius:8px; padding:0.75rem 1rem; margin:1rem 0; }
.passkey-add legend { font-weight:600; padding:0 0.4rem; }
.passkey-add label { display:block; margin-bottom:0.25rem; font-weight:600; }
.passkey-add input[type=text] { padding:0.4rem 0.5rem; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font:inherit; min-width:20rem; max-width:100%; margin-right:0.5rem; }
.passkeys .pk-rename { display:flex; gap:0.4rem; align-items:center; flex-wrap:wrap; }
.passkeys .pk-rename input[type=text] { padding:0.35rem 0.5rem; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font:inherit; min-width:11rem; }
.invite-mint { display:flex; gap:0.6rem; align-items:center; flex-wrap:wrap; margin:1rem 0; }
.invite-mint label { font-weight:600; }
.invite-mint select { padding:0.35rem 0.5rem; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font:inherit; }
.invite-code { border:1px solid var(--accent); border-radius:8px; padding:0.75rem 1rem; margin:1rem 0; background:var(--card); }
.invite-code .minted-code { width:24rem; max-width:100%; padding:0.4rem 0.5rem; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font:ui-monospace,monospace; margin-right:0.5rem; }
.status { font-size:0.8rem; padding:0.1em 0.5em; border-radius:999px; border:1px solid var(--border); background:transparent; color:inherit; font-family:inherit; }
button.status { cursor:pointer; }
button.status:hover, button.status:focus-visible { border-color:var(--accent); }
.status.published { background:var(--accent); color:#fff; border-color:var(--accent); }
.editor-split { display:grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr); gap:1.25rem; align-items:start; }
@media (max-width:52rem){ .editor-split { grid-template-columns:1fr; } }
.editor-pane form label:not(.check):not(.block-field-check) { display:block; margin-bottom:0.75rem; font-weight:600; }
.editor-pane input, .editor-pane textarea, .editor-pane select { display:block; width:100%; margin-top:0.25rem; padding:0.5rem; font:inherit; font-weight:400; color:var(--fg); background:var(--bg); border:1px solid var(--border); border-radius:6px; }
.hint { display:block; font-weight:400; color:var(--muted); font-size:0.85rem; margin-top:0.15rem; }
.hint code { background:var(--bg); }
.editor-pane textarea { font-family: ui-monospace, Menlo, Consolas, monospace; }
.page-actions { display:flex; gap:0.5rem; align-items:center; margin-top:1rem; flex-wrap:wrap; }
.page-flow { margin:1rem 0 0; padding:0.5rem 0.75rem; border:1px solid var(--border); border-left-width:4px; border-radius:6px; background:var(--card); }
.preview { width:100%; height:36rem; border:1px solid var(--border); border-radius:8px; background:var(--bg); }
.flags { line-height:1.7; }
.admin-nav a { margin-left:1rem; text-decoration:none; }
.admin-nav .theme-toggle { margin-left:1rem; font:inherit; font-size:0.85rem; color:var(--muted); background:var(--card); border:1px solid var(--border); border-radius:999px; padding:0.2rem 0.7rem; cursor:pointer; vertical-align:baseline; }
.admin-nav .theme-toggle:hover, .admin-nav .theme-toggle:focus-visible { color:var(--fg); border-color:var(--accent); }
.admin-nav .theme-toggle[hidden] { display:none; }
.upload { display:flex; gap:0.75rem; align-items:flex-end; flex-wrap:wrap; margin:1rem 0; padding:1rem; background:var(--card); border:1px solid var(--border); border-radius:8px; }
.upload label { display:block; font-weight:600; }
.upload small { font-weight:400; color:var(--muted); }
.upload input[type=file] { display:block; margin-top:0.35rem; font:inherit; }
.thumb { width:72px; height:72px; object-fit:contain; background:var(--card); border:1px solid var(--border); border-radius:6px; display:block; }
/* The preview links to the full-size file (opens in a new tab) so a small, hard-to-read
   thumbnail can be inspected at native resolution. A real anchor keeps it keyboard-
   accessible; its aria-label names the target since the <img> alt is decorative. */
.thumb-link { display:inline-block; border-radius:6px; cursor:zoom-in; }
.thumb-link:hover .thumb { border-color:var(--accent); }
/* focus ring comes from the global :focus-visible rule (outline: var(--accent)). */
.thumb-av { display:flex; align-items:center; justify-content:center; font-size:1.5rem; color:var(--muted); }
/* Per-item admin note: an inline, no-JS form (textarea + Save) so each library
   row can carry context for what the file is for. */
.media-note-form { display:flex; flex-direction:column; gap:0.3rem; align-items:flex-start; }
.media-note-input { width:15rem; max-width:100%; font:inherit; font-size:0.85rem; padding:0.35rem 0.5rem; color:var(--fg); background:var(--bg); border:1px solid var(--border); border-radius:6px; resize:vertical; }
/* Visually-hidden label kept in the accessibility tree for screen readers. */
.sr-only { position:absolute; width:1px; height:1px; padding:0; margin:-1px; overflow:hidden; clip:rect(0,0,0,0); white-space:nowrap; border:0; }
.media-tabs { display:flex; gap:0.25rem; margin:1rem 0 0.5rem; border-bottom:1px solid var(--border); flex-wrap:wrap; }
.media-tab { padding:0.5rem 0.9rem; text-decoration:none; color:var(--fg); border:1px solid transparent; border-bottom:none; border-radius:8px 8px 0 0; }
.media-tab:hover { background:var(--card); }
.media-tab.active { background:var(--card); border-color:var(--border); font-weight:600; }
.media-count { display:inline-block; min-width:1.4em; text-align:center; font-size:0.8rem; color:var(--muted); background:var(--bg); border:1px solid var(--border); border-radius:999px; padding:0 0.35em; margin-left:0.2em; }
.media-search { display:flex; gap:0.5rem; align-items:center; margin:0.75rem 0; flex-wrap:wrap; }
.media-search input[type=search] { flex:1; min-width:12rem; padding:0.5rem; font:inherit; color:var(--fg); background:var(--bg); border:1px solid var(--border); border-radius:6px; }
.media-copy { display:flex; gap:0.35rem; flex-wrap:wrap; }
.btn-sm { padding:0.25rem 0.55rem; font-size:0.85rem; }
.pager { display:flex; gap:1rem; align-items:center; justify-content:center; margin:1rem 0; }
.pager-status { color:var(--muted); font-size:0.9rem; }
.btn.disabled, .btn[aria-disabled=true] { opacity:0.5; pointer-events:none; }
.mod-filter { display:flex; flex-wrap:wrap; gap:0.6rem 0.9rem; align-items:end; margin:1rem 0; padding:0.75rem; border:1px solid var(--border); border-radius:6px; background:var(--card); }
.mod-filter label { display:flex; flex-direction:column; gap:0.2rem; font-size:0.78rem; color:var(--muted); }
.mod-filter input[type=text], .mod-filter input[type=date], .mod-filter select { padding:0.35rem 0.4rem; font:inherit; font-size:0.9rem; color:var(--fg); background:var(--bg); border:1px solid var(--border); border-radius:6px; }
.mod-filter .btn, .mod-filter .linklike { align-self:end; }
.comment-status { text-transform:capitalize; color:var(--muted); font-size:0.85em; }
fieldset.blocks { border:1px solid var(--border); border-radius:8px; padding:0.75rem; margin:0 0 1rem; }
fieldset.blocks > legend { font-weight:600; padding:0 0.4rem; }
.blocks-toolbar { display:flex; gap:0.5rem; margin-top:0.5rem; flex-wrap:wrap; }
fieldset.block { border:1px solid var(--border); border-radius:8px; padding:0.6rem 0.75rem; margin-bottom:0.75rem; background:var(--card); }
fieldset.block > legend { font-size:0.85rem; color:var(--muted); padding:0 0.35rem; }
.block-field { display:block; margin-bottom:0.6rem; font-weight:600; font-size:0.9rem; }
.block-field-check { display:flex; align-items:center; gap:0.4rem; font-weight:400; }
.block-field-check input { display:inline-block; width:auto; margin:0; }
.block-actions { display:flex; gap:0.4rem; }
.block-actions button { padding:0.3rem 0.6rem; }
.preview-col { display:flex; flex-direction:column; gap:1rem; }
.preview-tall { height:80vh; position:static; }
/* Edit-page dual live preview: a scaled-down desktop mirror + a real phone width. */
.preview-view { margin:0; }
.preview-label { margin:0 0 0.35rem; font-size:0.8rem; font-weight:600; text-transform:uppercase; letter-spacing:0.05em; color:var(--muted); }
.preview-frame { border:1px solid var(--border); border-radius:8px; background:var(--bg); display:block; }
.preview-scale { overflow:hidden; border:1px solid var(--border); border-radius:8px; background:var(--bg); }
.preview-desktop { width:1100px; height:900px; border:0; display:block; transform-origin:top left; }
.preview-mobile { width:390px; max-width:100%; height:720px; margin-inline:auto; }
.link-panel { border:1px solid var(--border); border-radius:8px; padding:0.75rem 1rem; background:var(--card); }
.link-panel h2 { font-size:1rem; margin:0 0 0.5rem; }
.badges { list-style:none; padding:0; margin:0; display:flex; flex-direction:column; gap:0.4rem; }
.badges code { background:transparent; padding:0; }
.badges .reason { color:var(--muted); }
.badges-empty { color:var(--muted); margin:0; }
.media-panel { border:1px solid #c05621; background:rgba(192,86,33,0.08); border-radius:8px; padding:0.75rem 1rem; margin:0 0 1rem; }
.media-panel h2 { font-size:1rem; margin:0 0 0.5rem; color:#c05621; }
.media-panel-hint { margin:0.5rem 0 0; font-size:0.85rem; color:var(--muted); }
.media-broken-list { list-style:none; padding:0; margin:0; display:flex; flex-direction:column; gap:0.3rem; }
.media-broken-list code { background:transparent; padding:0; }
.broken-flag { color:#c05621; font-weight:700; }
.grade { display:inline-block; min-width:1.5em; text-align:center; font-weight:700; border-radius:5px; padding:0.05em 0.4em; color:#fff; }
.grade-a { background:#1a7f37; } .grade-b { background:#4a8f1a; } .grade-c { background:#9a7d0a; }
.grade-d { background:#c05621; } .grade-e { background:#b23b3b; } .grade-f { background:#b00020; }
.grade-unknown { background:#555; }
.settings-form { max-width:44rem; }
.settings-form fieldset { border:1px solid var(--border); border-radius:8px; padding:0.75rem 1rem; margin:0 0 1rem; }
.settings-form legend { font-weight:600; padding:0 0.4rem; }
.settings-form label { display:block; margin-bottom:0.75rem; font-weight:600; }
.settings-form label small { font-weight:400; color:var(--muted); }
.settings-form input[type=text], .settings-form input:not([type]), .settings-form input[type=url], .settings-form textarea { display:block; width:100%; margin-top:0.25rem; padding:0.5rem; font:inherit; font-weight:400; color:var(--fg); background:var(--bg); border:1px solid var(--border); border-radius:6px; }
label.check { font-weight:400; display:flex; gap:0.5rem; align-items:center; margin-bottom:0.75rem; }
label.check input { width:auto; margin:0; }
.alert { padding:0.6rem 0.85rem; border-radius:8px; border:1px solid var(--border); }
.alert.ok { background:color-mix(in srgb, var(--accent) 12%, transparent); }
.alert.warn { background:color-mix(in srgb, #b8860b 18%, transparent); }
.alert.danger { background:color-mix(in srgb, var(--danger) 15%, transparent); border-color:var(--danger); }
:focus-visible { outline:3px solid var(--accent); outline-offset:2px; }
/* Classification dataset editor (§6.8) */
.classify-editor { display:flex; flex-direction:column; gap:1rem; margin:1rem 0; }
.classify-empty { color:var(--muted); }
.classify-card { border:1px solid var(--border); border-radius:8px; background:var(--card); padding:0.85rem 1rem; }
.classify-head { display:flex; gap:0.6rem; align-items:center; margin-bottom:0.6rem; }
.classify-domain { flex:1; min-width:8rem; padding:0.4rem 0.5rem; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font-family:ui-monospace,SFMono-Regular,Menlo,monospace; }
.classify-grade { white-space:nowrap; }
.classify-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(11rem,1fr)); gap:0.5rem 0.75rem; }
.classify-field { display:flex; flex-direction:column; gap:0.2rem; font-size:0.85rem; }
.classify-flabel { color:var(--muted); }
.classify-field select, .classify-field input, .classify-card textarea { padding:0.35rem 0.5rem; border:1px solid var(--border); border-radius:6px; background:var(--bg); color:var(--fg); font:inherit; }
.classify-card textarea { width:100%; resize:vertical; }
.classify-status { font-size:0.85rem; }
.classify-status.ok { color:#1a7f37; }
.classify-status.bad { color:var(--danger); }
.classify-raw-wrap { margin:1rem 0; }
.classify-raw-wrap textarea { width:100%; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:0.85rem; }
.classify-io { align-items:center; margin:0.75rem 0 1rem; padding-bottom:1rem; border-bottom:1px solid var(--border); }
.classify-import { display:flex; gap:0.5rem; align-items:center; flex-wrap:wrap; }
.classify-import-label { display:flex; gap:0.4rem; align-items:center; font-size:0.9rem; }
/* Metrics dashboard (§7.7) — aggregate cards, /16 heat map, class tables. */
.metric-cards { display:flex; flex-wrap:wrap; gap:0.75rem; margin:1rem 0 1.5rem; }
.metric-card { background:var(--card); border:1px solid var(--border); border-radius:8px; padding:0.6rem 0.9rem; min-width:8.5rem; }
.metric-card .k { color:var(--muted); font-size:0.72rem; text-transform:uppercase; letter-spacing:0.04em; }
.metric-card .v { font-size:1.3rem; font-weight:600; margin-top:0.1rem; }
.metric-grid2 { display:grid; grid-template-columns:repeat(auto-fit,minmax(260px,1fr)); gap:1.25rem; align-items:start; }
.metric-grid2 > table.metrics { min-width:0; }
table.metrics td code { overflow-wrap:anywhere; }
img.heat { image-rendering:pixelated; width:256px; height:256px; max-width:100%; border:1px solid var(--border); border-radius:4px; background:var(--card); }
.heat-figure { margin:0 0 1.5rem; }
.heat-legend { display:flex; align-items:center; gap:0.5rem; margin-top:0.5rem; font-size:0.78rem; color:var(--muted); }
.heat-ramp { width:120px; height:10px; border-radius:2px; background:linear-gradient(90deg,#260000,#b30000,#f00,#ff0,#fff); }
.heat-sw { width:12px; height:12px; border-radius:2px; background:rgb(64,64,72); display:inline-block; }
.heat-figure figcaption { color:var(--muted); font-size:0.82rem; margin-top:0.5rem; max-width:44ch; }
table.metrics { width:100%; border-collapse:collapse; margin-bottom:1.25rem; }
table.metrics caption { text-align:left; font-weight:600; margin-bottom:0.4rem; }
table.metrics th, table.metrics td { text-align:left; padding:0.3rem 0.5rem; border-bottom:1px solid var(--border); }
table.metrics th[scope=col] { color:var(--muted); font-size:0.75rem; text-transform:uppercase; letter-spacing:0.04em; }
table.metrics td.n, table.metrics td.p { text-align:right; font-variant-numeric:tabular-nums; }
table.metrics td.p { color:var(--muted); }
`

// blocksJS is the self-hosted content-block editor. It manages the repeatable
// block list (markdown blocks + the youtube consent fieldblock, SPEC §5.8) in a
// hidden JSON field, so the same render pipeline drives both live preview and
// the build. No inline handlers; everything is wired with addEventListener.
const blocksJS = `(function () {
  var form = document.getElementById('editor');
  var host = document.getElementById('blocks-editor');
  var hidden = document.getElementById('blocks-field');
  if (!form || !host || !hidden) return;

  var blocks = [];
  try { blocks = JSON.parse(hidden.value || '[]') || []; } catch (e) { blocks = []; }

  function sync() {
    hidden.value = JSON.stringify(blocks);
    form.dispatchEvent(new Event('input', { bubbles: true }));
  }
  function splitLines(s) { return s.split('\n').map(function (x) { return x.trim(); }).filter(Boolean); }
  function parseGalleryLines(s) {
    return s.split('\n').map(function (ln) {
      var p = ln.split('|');
      var src = (p[0] || '').trim();
      if (!src) return null;
      return { src: src, alt: (p[1] || '').trim(), caption: (p[2] || '').trim() };
    }).filter(Boolean);
  }
  function galleryLines(items) {
    return (items || []).map(function (it) { return [it.src, it.alt || '', it.caption || ''].join(' | '); }).join('\n');
  }
  function splitCommas(s) { return s.split(',').map(function (x) { return x.trim(); }).filter(Boolean); }

  function field(label, value, kind, oninput, maxlen) {
    var wrap = document.createElement('label');
    wrap.className = 'block-field';
    wrap.appendChild(document.createTextNode(label));
    var input = document.createElement(kind === 'textarea' ? 'textarea' : 'input');
    if (kind !== 'textarea') input.type = 'text';
    if (kind === 'textarea') input.rows = 4;
    if (maxlen) input.maxLength = maxlen;
    input.value = value || '';
    input.addEventListener('input', function () { oninput(input.value); });
    wrap.appendChild(input);
    return wrap;
  }
  function selectField(label, value, options, oninput) {
    var wrap = document.createElement('label');
    wrap.className = 'block-field';
    wrap.appendChild(document.createTextNode(label));
    var sel = document.createElement('select');
    options.forEach(function (o) {
      var opt = document.createElement('option');
      opt.value = o; opt.textContent = o;
      if ((value || options[0]) === o) opt.selected = true;
      sel.appendChild(opt);
    });
    sel.addEventListener('change', function () { oninput(sel.value); });
    wrap.appendChild(sel);
    return wrap;
  }
  function checkField(label, checked, onchange) {
    var wrap = document.createElement('label');
    wrap.className = 'block-field block-field-check';
    var input = document.createElement('input');
    input.type = 'checkbox';
    input.checked = !!checked;
    input.addEventListener('change', function () { onchange(input.checked); });
    wrap.appendChild(input);
    wrap.appendChild(document.createTextNode(' ' + label));
    return wrap;
  }
  function btn(label, cls, fn) {
    var b = document.createElement('button');
    b.type = 'button';
    b.textContent = label;
    if (cls) b.className = cls;
    b.addEventListener('click', fn);
    return b;
  }
  function move(i, d) {
    var j = i + d;
    if (j < 0 || j >= blocks.length) return;
    var t = blocks[i]; blocks[i] = blocks[j]; blocks[j] = t;
    render(); sync();
  }

  function render() {
    host.textContent = '';
    blocks.forEach(function (b, i) {
      var fs = document.createElement('fieldset');
      fs.className = 'block';
      var legend = document.createElement('legend');
      var kind = b.type === 'youtube' ? 'YouTube video' : b.type === 'embed' ? 'Embed' : b.type === 'image' ? 'Image' : b.type === 'media' ? 'Video / audio' : b.type === 'callout' ? 'Callout' : b.type === 'citation' ? 'Citation' : b.type === 'code' ? 'Code' : b.type === 'details' ? 'Details / FAQ' : b.type === 'toc' ? 'Table of contents' : b.type === 'related' ? 'Related posts' : b.type === 'gallery' ? 'Gallery' : b.type === 'share' ? 'Share' : b.type === 'index' ? 'Page index' : b.type === 'reveal' ? 'Hidden (reveal)' : b.type === 'comments' ? 'Comments' : 'Markdown';
      legend.textContent = kind + ' block ' + (i + 1);
      fs.appendChild(legend);

      if (b.type === 'image') {
        if (!b.image) b.image = {};
        var im = b.image;
        fs.appendChild(field('Image path (from the Media library, e.g. /media/…)', im.src, 'text', function (v) { im.src = v; sync(); }));
        fs.appendChild(field('Alt text (describe the image for screen readers)', im.alt, 'text', function (v) { im.alt = v; sync(); }));
        fs.appendChild(field('Caption (optional)', im.caption, 'text', function (v) { im.caption = v; sync(); }));
        fs.appendChild(selectField('Layout (text wraps beside a float)', im.align || 'full', ['full', 'left', 'right'], function (v) { im.align = (v === 'full' ? '' : v); sync(); }));
        fs.appendChild(selectField('Max width', im.maxWidth || 'default', ['default', 'small', 'medium', 'large'], function (v) { im.maxWidth = (v === 'default' ? '' : v); sync(); }));
      } else if (b.type === 'media') {
        if (!b.media) b.media = {};
        var md = b.media;
        fs.appendChild(selectField('Kind', md.kind, ['video', 'audio'], function (v) { md.kind = v; sync(); }));
        fs.appendChild(field('Media path (from the Media library, e.g. /media/…)', md.src, 'text', function (v) { md.src = v; sync(); }));
        fs.appendChild(field('Poster image path (optional, video only)', md.poster, 'text', function (v) { md.poster = v; sync(); }));
        fs.appendChild(field('Caption (optional)', md.caption, 'text', function (v) { md.caption = v; sync(); }));
      } else if (b.type === 'index') {
        if (!b.index) b.index = {};
        var ix = b.index;
        fs.appendChild(field('Base route (blank = this page’s path)', ix.base, 'text', function (v) { ix.base = v; sync(); }));
        fs.appendChild(selectField('Depth', ix.depth === undefined ? '1' : String(ix.depth), ['1', '2', '3', '0'], function (v) { ix.depth = parseInt(v, 10) || 0; sync(); }));
        fs.appendChild(selectField('Sort', ix.sort, ['date-desc', 'date-asc', 'path', 'title'], function (v) { ix.sort = v; sync(); }));
        fs.appendChild(selectField('Style', ix.style, ['titles', 'detailed'], function (v) { ix.style = v; sync(); }));
        fs.appendChild(field('Title (optional heading)', ix.title, 'text', function (v) { ix.title = v; sync(); }));
        fs.appendChild(field('Max items (0 = default)', ix.limit ? String(ix.limit) : '', 'text', function (v) { ix.limit = parseInt(v, 10) || 0; sync(); }));
        var note = document.createElement('p');
        note.className = 'hint';
        note.textContent = 'Depth 1 = direct children, 0 = all. Renders only when this page is checked as an index page (top of the form).';
        fs.appendChild(note);
      } else if (b.type === 'callout') {
        if (!b.callout) b.callout = {};
        var co = b.callout;
        fs.appendChild(selectField('Style', co.variant, ['note', 'tip', 'warning', 'info'], function (v) { co.variant = v; sync(); }));
        fs.appendChild(field('Title (optional)', co.title, 'text', function (v) { co.title = v; sync(); }));
        fs.appendChild(field('Body (markdown)', co.markdown, 'textarea', function (v) { co.markdown = v; sync(); }));
      } else if (b.type === 'citation') {
        if (!b.citation) b.citation = {};
        var ci = b.citation;
        fs.appendChild(field('Quote (markdown)', ci.quote, 'textarea', function (v) { ci.quote = v; sync(); }));
        fs.appendChild(field('Source (optional, e.g. author or work)', ci.source, 'text', function (v) { ci.source = v; sync(); }));
        fs.appendChild(field('Source URL (optional)', ci.url, 'text', function (v) { ci.url = v; sync(); }));
      } else if (b.type === 'code') {
        if (!b.code) b.code = {};
        var cd = b.code;
        fs.appendChild(field('Filename (optional caption bar, e.g. main.go)', cd.filename, 'text', function (v) { cd.filename = v; sync(); }));
        fs.appendChild(field('Language label (optional — display only, no syntax highlighting)', cd.language, 'text', function (v) { cd.language = v; sync(); }));
        fs.appendChild(field('Code (verbatim — shown exactly, never interpreted)', cd.text, 'textarea', function (v) { cd.text = v; sync(); }));
        fs.appendChild(field('Comment (optional note under the code)', cd.comment, 'text', function (v) { cd.comment = v; sync(); }));
        fs.appendChild(checkField('Show line numbers', cd.lineNumbers, function (v) { cd.lineNumbers = v; sync(); }));
      } else if (b.type === 'details') {
        if (!b.details) b.details = {};
        var dt = b.details;
        fs.appendChild(field('Summary (the clickable question/label)', dt.summary, 'text', function (v) { dt.summary = v; sync(); }));
        fs.appendChild(field('Body (markdown — shown when expanded)', dt.markdown, 'textarea', function (v) { dt.markdown = v; sync(); }));
        fs.appendChild(checkField('Expanded by default', dt.open, function (v) { dt.open = v; sync(); }));
        var dnote = document.createElement('p');
        dnote.className = 'hint';
        dnote.textContent = 'Visible-but-collapsed: the body is in the page source and indexable (unlike a Hidden/reveal block). No JavaScript; works with the keyboard by default.';
        fs.appendChild(dnote);
      } else if (b.type === 'toc') {
        if (!b.toc) b.toc = {};
        var tc = b.toc;
        fs.appendChild(field('Title (optional heading above the list)', tc.title, 'text', function (v) { tc.title = v; sync(); }));
        fs.appendChild(selectField('Depth (heading levels: 1 = H2 only … 3 = H2–H4)', tc.depth ? String(tc.depth) : '3', ['3', '2', '1'], function (v) { tc.depth = parseInt(v, 10) || 3; sync(); }));
        var tnote = document.createElement('p');
        tnote.className = 'hint';
        tnote.textContent = 'Auto-generated at build from this page’s H2–H4 headings (markdown and blocks). Headings also get permalink anchors. Renders empty in the raw preview; the list is filled in the full/standalone preview and the build.';
        fs.appendChild(tnote);
      } else if (b.type === 'related') {
        if (!b.related) b.related = {};
        var rl = b.related;
        fs.appendChild(field('Title (optional heading; default “Related posts”)', rl.title, 'text', function (v) { rl.title = v; sync(); }));
        fs.appendChild(selectField('How many', String(rl.count || 5), ['3', '5', '8', '10'], function (v) { rl.count = parseInt(v, 10) || 5; sync(); }));
        var rlnote = document.createElement('p');
        rlnote.className = 'hint';
        rlnote.textContent = 'Auto-generated at build: other pages marked “This is a post” that share the most tags with this page (then most recent). Needs tags on this page and on the others. Renders nothing (and shows empty here) when no other tagged posts match yet.';
        fs.appendChild(rlnote);
      } else if (b.type === 'gallery') {
        if (!b.gallery) b.gallery = { mode: 'manual', columns: 3 };
        var gl = b.gallery;
        fs.appendChild(selectField('Mode', gl.mode || 'manual', ['manual', 'tag'], function (v) { gl.mode = v; render(); sync(); }));
        fs.appendChild(selectField('Columns', String(gl.columns || 3), ['3', '2', '4'], function (v) { gl.columns = parseInt(v, 10) || 3; sync(); }));
        if (gl.mode === 'tag') {
          fs.appendChild(field('Media tag (gathers every image carrying this tag)', gl.tag, 'text', function (v) { gl.tag = v; sync(); }));
          fs.appendChild(selectField('Order', gl.sort || 'newest', ['newest', 'oldest', 'name'], function (v) { gl.sort = v; sync(); }));
          var gtnote = document.createElement('p');
          gtnote.className = 'hint';
          gtnote.textContent = 'Auto-filled from the media library at build: every image tagged with this tag. Each image’s alt text comes from its Note in the library. Tag images under Media.';
          fs.appendChild(gtnote);
        } else {
          fs.appendChild(field('Images — one per line: /media/…  |  alt text  |  optional caption', galleryLines(gl.items), 'textarea', function (v) { gl.items = parseGalleryLines(v); sync(); }));
          var gmnote = document.createElement('p');
          gmnote.className = 'hint';
          gmnote.textContent = 'One image per line: the media path, then alt text, then an optional caption, separated by “|”. Copy paths from the Media library.';
          fs.appendChild(gmnote);
        }
      } else if (b.type === 'share') {
        if (!b.share) b.share = { copyLink: true, email: true, mastodon: true };
        var sh = b.share;
        fs.appendChild(field('Title (optional heading; default “Share”)', sh.title, 'text', function (v) { sh.title = v; sync(); }));
        fs.appendChild(checkField('Copy link button', sh.copyLink, function (v) { sh.copyLink = v; sync(); }));
        fs.appendChild(checkField('Email (mailto) link', sh.email, function (v) { sh.email = v; sync(); }));
        fs.appendChild(checkField('Mastodon share (visitor enters their instance)', sh.mastodon, function (v) { sh.mastodon = v; sync(); }));
        fs.appendChild(field('RSS / feed URL (optional pointer)', sh.rss, 'text', function (v) { sh.rss = v; sync(); }));
        var shnote = document.createElement('p');
        shnote.className = 'hint';
        shnote.textContent = 'Privacy-preserving: no third-party buttons or pixels. Copy-link and Mastodon read the live page URL on click (self-hosted script); email is a plain mailto. Nothing loads on page view.';
        fs.appendChild(shnote);
      } else if (b.type === 'reveal') {
        if (!b.reveal) b.reveal = {};
        var rv = b.reveal;
        fs.appendChild(field('Hidden content (kept out of the page source until revealed; Markdown when kind is "markdown")', rv.content, 'textarea', function (v) { rv.content = v; sync(); }));
        fs.appendChild(field('Button label (what the reader clicks — required)', rv.label, 'text', function (v) { rv.label = v; sync(); }));
        fs.appendChild(selectField('Kind (text · email → mailto link · markdown → rendered)', rv.kind, ['text', 'email', 'markdown'], function (v) { rv.kind = v; sync(); }));
        fs.appendChild(field('Access code (optional — leave blank to just hide; set to gate behind a code)', rv.code, 'text', function (v) { rv.code = v; sync(); }, 128)); // maxlength mirrors render.MaxRevealCode
        fs.appendChild(field('Members-only groups (comma-separated aliases; blank = not members-only)', (b.groups || []).join(', '), 'text', function (v) { b.groups = splitCommas(v); sync(); }));
        fs.appendChild(field('No-JavaScript fallback text (optional)', rv.noscript, 'text', function (v) { rv.noscript = v; sync(); }));
        var rnote = document.createElement('p');
        rnote.className = 'hint';
        rnote.textContent = (b.groups && b.groups.length)
          ? 'Members-only (Mode C): revealed only to readers holding a listed group key (any-of), unlocked from their keyring — a real key, not obfuscation. Takes precedence over any Access code above. Create/manage aliases under Key groups; needs a secure context (https).'
          : (rv.code && rv.code.trim())
          ? 'Code gate (soft): the reader must type this code to reveal. It is only as strong as the code and how private it stays — a public/short code is a speed bump, not a login, and once shared it is out (rebuild with a new code to revoke). Needs a secure context (https) to reveal.'
          : 'Obfuscation, not security: this hides the text from view-source, search, and scrapers until a click, but the decode key ships in the page — anyone running the JavaScript can read it. Needs a secure context (https) to reveal.';
        fs.appendChild(rnote);
      } else if (b.type === 'embed') {
        if (!b.embed) b.embed = {};
        var em = b.embed;
        fs.appendChild(field('Provider (label & URL slug, e.g. peertube)', em.provider, 'text', function (v) { em.provider = v; sync(); }));
        fs.appendChild(field('Embed URL (https iframe src; host must be allowlisted in Settings)', em.embedUrl, 'text', function (v) { em.embedUrl = v; sync(); }));
        fs.appendChild(field('URL name (slug)', em.name, 'text', function (v) { em.name = v; sync(); }));
        fs.appendChild(field('Title', em.title, 'text', function (v) { em.title = v; sync(); }));
        fs.appendChild(field('Poster (media path, optional)', em.poster, 'text', function (v) { em.poster = v; sync(); }));
        fs.appendChild(field('Notes / description (markdown)', em.transcript, 'textarea', function (v) { em.transcript = v; sync(); }));
        fs.appendChild(field('Description links (one per line — URL, optionally with a label: "Docs https://example.com")', (em.descriptionLinks || []).join('\n'), 'textarea', function (v) { em.descriptionLinks = splitLines(v); sync(); }));
        fs.appendChild(field('Keywords (comma separated)', (em.keywords || []).join(', '), 'text', function (v) { em.keywords = splitCommas(v); sync(); }));
      } else if (b.type === 'youtube') {
        if (!b.youtube) b.youtube = {};
        var y = b.youtube;
        fs.appendChild(field('Video ID', y.videoId, 'text', function (v) { y.videoId = v; sync(); }));
        fs.appendChild(field('URL name (slug)', y.name, 'text', function (v) { y.name = v; sync(); }));
        fs.appendChild(field('Title', y.title, 'text', function (v) { y.title = v; sync(); }));
        fs.appendChild(field('Poster (media path, optional)', y.poster, 'text', function (v) { y.poster = v; sync(); }));
        fs.appendChild(field('Transcript (markdown)', y.transcript, 'textarea', function (v) { y.transcript = v; sync(); }));
        fs.appendChild(field('Description links (one per line — URL, optionally with a label: "Docs https://example.com")', (y.descriptionLinks || []).join('\n'), 'textarea', function (v) { y.descriptionLinks = splitLines(v); sync(); }));
        fs.appendChild(field('Keywords (comma separated)', (y.keywords || []).join(', '), 'text', function (v) { y.keywords = splitCommas(v); sync(); }));
      } else if (b.type === 'comments') {
        var cmnote = document.createElement('p');
        cmnote.className = 'hint';
        cmnote.textContent = 'Reader comments for this page. There is nothing to configure: members sign in with a passkey to post, and comments appear only after moderation. Loaded by the self-hosted widget on the published site — no third-party scripts, no tracking. Add at most one per page.';
        fs.appendChild(cmnote);
      } else {
        fs.appendChild(field('Markdown', b.markdown, 'textarea', function (v) { b.markdown = v; sync(); }));
      }

      // Group-gating (§6.10): the text-shaped blocks plus image/media may be hidden
      // behind a members keyring. Empty = public.
      if (b.type === '' || b.type === 'markdown' || b.type === 'callout' || b.type === 'citation' || b.type === 'image' || b.type === 'media' || b.type === 'code' || b.type === 'details' || b.type === 'gallery' || b.type === 'index') {
        fs.appendChild(field('Members-only groups (comma-separated aliases; blank = public)', (b.groups || []).join(', '), 'text', function (v) { b.groups = splitCommas(v); sync(); }));
        var gnote = document.createElement('p');
        gnote.className = 'hint';
        gnote.textContent = (b.groups && b.groups.length)
          ? ((b.type === 'image' || b.type === 'media' || b.type === 'gallery')
              ? 'Group-gated: shown only to readers holding a listed group key (any-of). Caveat: the file(s) at /media/… stay publicly fetchable — gating hides placement on the page, not the bytes. Any third-party image URL here still has its domain (FQDN only) listed in the page’s external references. Create/manage aliases under Key groups.'
              : (b.type === 'code')
              ? 'Group-gated: the code is shown only to readers holding a listed group key (any-of). Caveat: the Copy button will NOT work on a gated code block — the content is injected after the copy script has already run. Line numbers still work. Create/manage aliases under Key groups.'
              : (b.type === 'index')
              ? 'Group-gated: the page list is shown only to readers holding a listed group key (any-of), but the pages it links to remain public. Create/manage aliases under Key groups.'
              : 'Group-gated: kept out of the page source and shown only to readers holding a listed group key (any-of). A shared bearer key, not a per-user login. Any third-party link in this content still has its domain (FQDN only) listed in the page’s external references — self-host the resource if that metadata must stay private. Create/manage aliases under Key groups.')
          : 'Leave blank for public content. Enter one or more Key group aliases to hide this block behind a members keyring.';
        fs.appendChild(gnote);
      }

      var bar = document.createElement('div');
      bar.className = 'block-actions';
      bar.appendChild(btn('↑', 'btn', function () { move(i, -1); }));
      bar.appendChild(btn('↓', 'btn', function () { move(i, 1); }));
      bar.appendChild(btn('Remove', 'danger', function () { blocks.splice(i, 1); render(); sync(); }));
      fs.appendChild(bar);
      host.appendChild(fs);
    });
  }

  var addMd = document.getElementById('add-markdown');
  var addYt = document.getElementById('add-youtube');
  var addImg = document.getElementById('add-image');
  if (addMd) addMd.addEventListener('click', function () { blocks.push({ type: 'markdown', markdown: '' }); render(); sync(); });
  if (addYt) addYt.addEventListener('click', function () { blocks.push({ type: 'youtube', youtube: {} }); render(); sync(); });
  if (addImg) addImg.addEventListener('click', function () { blocks.push({ type: 'image', image: {} }); render(); sync(); });
  var addMediaBtn = document.getElementById('add-media');
  if (addMediaBtn) addMediaBtn.addEventListener('click', function () { blocks.push({ type: 'media', media: { kind: 'video' } }); render(); sync(); });
  var addCallout = document.getElementById('add-callout');
  if (addCallout) addCallout.addEventListener('click', function () { blocks.push({ type: 'callout', callout: { variant: 'note' } }); render(); sync(); });
  var addCitation = document.getElementById('add-citation');
  if (addCitation) addCitation.addEventListener('click', function () { blocks.push({ type: 'citation', citation: {} }); render(); sync(); });
  var addCode = document.getElementById('add-code');
  if (addCode) addCode.addEventListener('click', function () { blocks.push({ type: 'code', code: {} }); render(); sync(); });
  var addDetails = document.getElementById('add-details');
  if (addDetails) addDetails.addEventListener('click', function () { blocks.push({ type: 'details', details: {} }); render(); sync(); });
  var addTOC = document.getElementById('add-toc');
  if (addTOC) addTOC.addEventListener('click', function () { blocks.push({ type: 'toc', toc: { depth: 3 } }); render(); sync(); });
  var addRelated = document.getElementById('add-related');
  if (addRelated) addRelated.addEventListener('click', function () { blocks.push({ type: 'related', related: { count: 5 } }); render(); sync(); });
  var addGallery = document.getElementById('add-gallery');
  if (addGallery) addGallery.addEventListener('click', function () { blocks.push({ type: 'gallery', gallery: { mode: 'manual', columns: 3 } }); render(); sync(); });
  var addShare = document.getElementById('add-share');
  if (addShare) addShare.addEventListener('click', function () { blocks.push({ type: 'share', share: { copyLink: true, email: true, mastodon: true } }); render(); sync(); });
  var addEmbed = document.getElementById('add-embed');
  if (addEmbed) addEmbed.addEventListener('click', function () { blocks.push({ type: 'embed', embed: {} }); render(); sync(); });
  var addIndex = document.getElementById('add-index');
  if (addIndex) addIndex.addEventListener('click', function () { blocks.push({ type: 'index', index: { depth: 1, sort: 'date-desc', style: 'titles' } }); render(); sync(); });
  var addReveal = document.getElementById('add-reveal');
  if (addReveal) addReveal.addEventListener('click', function () { blocks.push({ type: 'reveal', reveal: { kind: 'text' } }); render(); sync(); });
  var addComments = document.getElementById('add-comments');
  if (addComments) addComments.addEventListener('click', function () { blocks.push({ type: 'comments' }); render(); sync(); });

  // Append a high-entropy suffix to the page path, turning an unlisted page's URL
  // into an unguessable capability (SPEC §6.16). Uses the browser CSPRNG; nothing
  // leaves the page.
  var pathRandom = document.getElementById('path-random');
  var pathInput = document.getElementById('page-path');
  if (pathRandom && pathInput) {
    pathRandom.addEventListener('click', function () {
      var bytes = new Uint8Array(8);
      crypto.getRandomValues(bytes);
      var hex = Array.prototype.map.call(bytes, function (b) {
        return ('0' + b.toString(16)).slice(-2);
      }).join('');
      var base = pathInput.value.trim().replace(/\/+$/, '');
      if (base === '' || base === '/') { base = '/page'; }
      pathInput.value = base + '-' + hex;
      pathInput.focus();
    });
  }

  render();
})();
`

// adminJS drives the live preview: it posts the editor form to /preview and
// shows the rendered page in an iframe. Self-hosted, no inline handlers.
const adminJS = `(function () {
  var form = document.getElementById('editor');
  if (!form) return;
  var desktop = document.getElementById('preview-desktop');
  var mobile = document.getElementById('preview-mobile');
  var wrap = document.getElementById('preview-desktop-wrap');
  var badges = document.getElementById('link-badges');
  var mediaPanel = document.getElementById('media-panel');
  var mediaWarnings = document.getElementById('media-warnings');
  var bodyLabel = document.getElementById('body-label');
  var timer;
  // Toggle a "— Broken Media" flag on a heading/label element (body label or a
  // block legend), so the author sees exactly which field references media that
  // is not in the library. Idempotent: it adds or removes one .broken-flag span.
  function setBrokenFlag(el, on) {
    if (!el) return;
    var ex = el.querySelector(':scope > .broken-flag');
    if (on && !ex) {
      var s = document.createElement('span');
      s.className = 'broken-flag';
      s.textContent = ' — Broken Media';
      el.appendChild(s);
    } else if (!on && ex) {
      ex.remove();
    }
  }
  function applyScan(data) {
    if (badges) badges.innerHTML = data.badges || '';
    if (mediaWarnings) mediaWarnings.innerHTML = data.media || '';
    if (mediaPanel) mediaPanel.hidden = !(data.media && data.media.trim());
    setBrokenFlag(bodyLabel, !!data.body);
    var set = {};
    (data.blocks || []).forEach(function (i) { set[i] = true; });
    var legends = document.querySelectorAll('#blocks-editor .block > legend');
    for (var i = 0; i < legends.length; i++) { setBrokenFlag(legends[i], !!set[i]); }
  }
  // The desktop preview renders at a fixed wide viewport and is scaled down to the
  // column so it mirrors the real desktop layout (floats, breakout width, etc.);
  // the mobile preview is a real phone width. VH is how much page height to show.
  var VW = 1100, VH = 900;
  function fitDesktop() {
    if (!wrap || !desktop) return;
    var scale = Math.min(1, wrap.clientWidth / VW);
    desktop.style.transform = 'scale(' + scale + ')';
    wrap.style.height = (VH * scale) + 'px';
  }
  function post(url) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams(new FormData(form)).toString()
    }).then(function (r) { return r.text(); });
  }
  function update() {
    post('/preview').then(function (html) {
      if (desktop) desktop.srcdoc = html;
      if (mobile) mobile.srcdoc = html;
    }).catch(function () {});
    post('/scan').then(function (text) {
      var data;
      try { data = JSON.parse(text); } catch (e) { return; }
      applyScan(data);
    }).catch(function () {});
  }
  form.addEventListener('input', function () { clearTimeout(timer); timer = setTimeout(update, 400); });
  // The library's media can change out-of-band — a separate upload/delete, or a
  // back/forward (bfcache) navigation from the Media library — with no editor
  // input to trigger a refresh, so the last-rendered preview can keep showing an
  // image that is now missing (or miss one just added). Re-render whenever the
  // editor is shown again; the preview and its media are served no-store, so the
  // reloaded iframe reflects the true current state.
  window.addEventListener('pageshow', function (e) { if (e.persisted) update(); });
  document.addEventListener('visibilitychange', function () { if (!document.hidden) update(); });
  window.addEventListener('resize', fitDesktop);
  fitDesktop();
  update();
})();
`

// adminThemeJS gives the editor chrome the same Auto/Light/Dark control as the
// built site (render.ThemeJS), but keyed on 'pbcssg-admin-theme' so the operator's
// editor preference is independent of the preview iframe (which shares the origin
// and uses 'pbcssg-theme'). It loads blocking in <head> so a stored choice applies
// before first paint, and reveals the nav toggle only when it runs (progressive
// enhancement). No inline code — the admin CSP allows a same-origin script.
const adminThemeJS = `(function () {
  var KEY = 'pbcssg-admin-theme';
  var root = document.documentElement;
  function stored() {
    try { var v = localStorage.getItem(KEY); return (v === 'light' || v === 'dark') ? v : null; }
    catch (e) { return null; }
  }
  function apply(v) { if (v) { root.setAttribute('data-theme', v); } else { root.removeAttribute('data-theme'); } }
  apply(stored());
  function label(v) { return v === 'dark' ? 'Dark' : v === 'light' ? 'Light' : 'Auto'; }
  function icon(v) { return v === 'dark' ? '☾' : v === 'light' ? '☀' : '◐'; }
  function wire() {
    var btn = document.querySelector('[data-pbcssg-theme-toggle]');
    if (!btn) { return; }
    var cur = stored();
    function render() {
      btn.textContent = icon(cur) + ' ' + label(cur);
      btn.setAttribute('aria-label', 'Editor colour theme: ' + label(cur) + ' (following your device when Auto). Activate to change.');
    }
    render();
    btn.hidden = false;
    btn.addEventListener('click', function () {
      cur = cur === null ? 'light' : cur === 'light' ? 'dark' : null;
      apply(cur);
      try { if (cur) { localStorage.setItem(KEY, cur); } else { localStorage.removeItem(KEY); } } catch (e) {}
      render();
    });
  }
  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', wire); } else { wire(); }
})();
`

// copyJS wires any element carrying a data-copy attribute to copy that text to
// the clipboard (the media library's Markdown/Path buttons, the dashboard and
// editor "Copy MD link" buttons). Self-hosted, no inline handlers; the clipboard
// API works because the editor is served over a secure context (the https admin
// origin, or http://localhost in dev), with an execCommand fallback. Confirms with a
// transient label swap.
const copyJS = `(function () {
  function flash(btn) {
    var prev = btn.textContent;
    btn.textContent = 'Copied!';
    setTimeout(function () { btn.textContent = prev; }, 1200);
  }
  function fallback(text, btn) {
    var ta = document.createElement('textarea');
    ta.value = text; ta.setAttribute('readonly', '');
    ta.style.position = 'absolute'; ta.style.left = '-9999px';
    document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); flash(btn); } catch (e) {}
    document.body.removeChild(ta);
  }
  function copy(text, btn) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { flash(btn); }).catch(function () { fallback(text, btn); });
    } else {
      fallback(text, btn);
    }
  }
  var btns = document.querySelectorAll('[data-copy]');
  for (var i = 0; i < btns.length; i++) {
    (function (b) {
      b.addEventListener('click', function () { copy(b.getAttribute('data-copy'), b); });
    })(btns[i]);
  }
})();
`

// classifyJS is the self-hosted structured editor for the custom classification
// dataset (§6.8). It renders per-domain cards from the raw-JSON textarea (the
// no-JS fallback + submit source), keeps the two in sync, and shows each domain's
// live grade by posting the candidate dataset to /admin/classification/preview
// (grading stays in Go so the preview matches a build). No third-party resources.
const classifyJS = `(function () {
  var raw = document.getElementById('classify-raw');
  var editor = document.getElementById('classify-editor');
  var statusEl = document.getElementById('classify-status');
  var addBtn = document.getElementById('classify-add');
  var form = document.getElementById('classify-form');
  if (!raw || !editor || !form) return;

  var TERNARY = ['unknown', 'no', 'yes'];
  var LEVEL = ['unknown', 'none', 'low', 'high'];
  var TRUST = ['unknown', 'imported', 'audited', 'own'];
  var SIGNALS = [
    ['adTrackingCookies', 'Third-party ad cookies', TERNARY],
    ['honorsGPC', 'Honours GPC', TERNARY],
    ['adsTrackers', 'Ads & trackers', LEVEL],
    ['thirdPartyScripts', 'Third-party scripts', LEVEL],
    ['fingerprinting', 'Fingerprinting', TERNARY],
    ['sessionReplay', 'Session replay', TERNARY],
    ['sellsSharesData', 'Sells / shares data', TERNARY]
  ];
  var model = [];

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function select(values, val, on) {
    var s = document.createElement('select');
    values.forEach(function (v) {
      var o = document.createElement('option');
      o.value = v; o.textContent = v;
      if (v === (val || 'unknown')) o.selected = true;
      s.appendChild(o);
    });
    s.addEventListener('change', on);
    return s;
  }
  function field(label, control) {
    var w = el('label', 'classify-field');
    w.appendChild(el('span', 'classify-flabel', label));
    w.appendChild(control);
    return w;
  }
  function setStatus(msg, bad) {
    if (!statusEl) return;
    statusEl.textContent = msg;
    statusEl.className = 'classify-status ' + (bad ? 'bad' : 'ok');
  }

  function loadModel() {
    var obj = {}, s = raw.value.trim();
    if (s) { try { obj = JSON.parse(s); } catch (e) { return false; } }
    if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return false;
    model = Object.keys(obj).map(function (d) {
      var e = obj[d] || {}; if (!e.signals) e.signals = {};
      return { domain: d, entry: e };
    });
    return true;
  }

  function serialize() {
    var out = {};
    model.forEach(function (row) {
      var d = (row.domain || '').trim().toLowerCase();
      if (!d) return;
      var e = row.entry || {}, clean = {};
      if (e.trust && e.trust !== 'unknown') clean.trust = e.trust;
      if (e.verified) clean.verified = e.verified;
      var sig = {};
      SIGNALS.forEach(function (s) {
        var v = e.signals && e.signals[s[0]];
        if (v && v !== 'unknown') sig[s[0]] = v;
      });
      var tpd = e.signals && e.signals.thirdPartyDomains;
      if (tpd != null && tpd !== '') { var n = parseInt(tpd, 10); if (!isNaN(n)) sig.thirdPartyDomains = n; }
      if (Object.keys(sig).length) clean.signals = sig;
      if (e.evidence) clean.evidence = e.evidence;
      if (e.note) clean.note = e.note;
      out[d] = clean;
    });
    return out;
  }

  var timer = null;
  function sync() {
    var obj = serialize();
    raw.value = Object.keys(obj).length ? JSON.stringify(obj, null, 2) : '';
    if (timer) clearTimeout(timer);
    timer = setTimeout(preview, 350);
  }

  function card(row) {
    var c = el('div', 'classify-card');
    c.setAttribute('data-domain', (row.domain || '').trim().toLowerCase());
    var head = el('div', 'classify-head');
    var dom = document.createElement('input');
    dom.type = 'text'; dom.value = row.domain || ''; dom.placeholder = 'example.com';
    dom.className = 'classify-domain'; dom.setAttribute('aria-label', 'Domain');
    dom.addEventListener('input', function () {
      row.domain = dom.value; c.setAttribute('data-domain', dom.value.trim().toLowerCase()); sync();
    });
    head.appendChild(dom);
    head.appendChild(el('span', 'grade grade-unknown classify-grade', '?'));
    var rm = el('button', 'btn danger', 'Remove'); rm.type = 'button';
    rm.addEventListener('click', function () {
      var i = model.indexOf(row); if (i >= 0) model.splice(i, 1); render(); sync();
    });
    head.appendChild(rm);
    c.appendChild(head);

    var e = row.entry; if (!e.signals) e.signals = {};
    var grid = el('div', 'classify-grid');
    grid.appendChild(field('Trust', select(TRUST, e.trust, function (ev) { e.trust = ev.target.value; sync(); })));
    var ver = document.createElement('input'); ver.type = 'date'; ver.value = e.verified || '';
    ver.addEventListener('input', function () { e.verified = ver.value; sync(); });
    grid.appendChild(field('Verified', ver));
    SIGNALS.forEach(function (s) {
      grid.appendChild(field(s[1], select(s[2], e.signals[s[0]], function (ev) { e.signals[s[0]] = ev.target.value; sync(); })));
    });
    var tpd = document.createElement('input'); tpd.type = 'number'; tpd.min = '0';
    tpd.value = (e.signals.thirdPartyDomains != null ? e.signals.thirdPartyDomains : '');
    tpd.addEventListener('input', function () { e.signals.thirdPartyDomains = tpd.value; sync(); });
    grid.appendChild(field('3rd-party domains', tpd));
    c.appendChild(grid);

    var ev = document.createElement('textarea'); ev.rows = 2; ev.value = e.evidence || '';
    ev.addEventListener('input', function () { e.evidence = ev.value; sync(); });
    c.appendChild(field('Evidence', ev));
    var nt = document.createElement('textarea'); nt.rows = 2; nt.value = e.note || '';
    nt.addEventListener('input', function () { e.note = nt.value; sync(); });
    c.appendChild(field('Note', nt));
    return c;
  }

  function render() {
    editor.textContent = '';
    if (!model.length) { editor.appendChild(el('p', 'classify-empty', 'No custom entries yet — the library defaults are used. Add a domain to override or extend them.')); }
    model.forEach(function (row) { editor.appendChild(card(row)); });
  }

  function preview() {
    fetch('/admin/classification/preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'dataset=' + encodeURIComponent(raw.value)
    }).then(function (r) { return r.json(); }).then(function (res) {
      if (!res.ok) { setStatus(res.error || 'Invalid dataset', true); return; }
      var n = Object.keys(res.grades || {}).length;
      setStatus('Valid — ' + n + ' custom domain' + (n === 1 ? '' : 's') + '.', false);
      var cards = editor.querySelectorAll('.classify-card');
      for (var i = 0; i < cards.length; i++) {
        var d = cards[i].getAttribute('data-domain');
        var g = res.grades && res.grades[d];
        var badge = cards[i].querySelector('.classify-grade');
        if (!badge) continue;
        if (g) {
          badge.textContent = g.grade + ' ' + g.name;
          badge.className = 'grade ' + g.class + ' classify-grade';
          badge.title = (g.reasons || []).join('; ');
        } else {
          badge.textContent = '?'; badge.className = 'grade grade-unknown classify-grade'; badge.title = '';
        }
      }
    }).catch(function () { setStatus('Preview unavailable.', true); });
  }

  if (!loadModel()) {
    setStatus('Raw JSON is not valid; edit it in the Raw JSON section below.', true);
  } else {
    render(); preview();
  }
  if (addBtn) addBtn.addEventListener('click', function () {
    if (!loadModel()) { setStatus('Fix the raw JSON before adding entries.', true); return; }
    model.push({ domain: '', entry: { signals: {} } });
    render(); sync();
  });
  raw.addEventListener('change', function () {
    if (loadModel()) { render(); preview(); } else { setStatus('Raw JSON is not valid.', true); }
  });
})();
`
