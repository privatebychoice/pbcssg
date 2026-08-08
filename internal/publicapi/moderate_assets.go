package publicapi

// moderateHTML is the server-rendered moderator page (html/template auto-escapes all
// user content — page paths, aliases, bodies). It links a self-hosted stylesheet and
// script (strict CSP: default-src 'self', no inline), so the passkey ceremony and the
// confirm dialogs run from same-origin assets only.
const moderateHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>Moderation</title>
<link rel="stylesheet" href="/_pbc/mod/assets/moderate.css">
</head>
<body>
<main class="mod">
<h1>Moderation</h1>
{{if .Authed}}
<div class="mod-bar">
  <span class="mod-who">Signed in{{if .Label}} as <strong>{{.Label}}</strong>{{end}} · <strong>{{.PendingCount}}</strong> pending</span>
  <span class="mod-baractions">{{if .CanBan}}<a class="btn" href="/_pbc/mod/accounts">Accounts</a> {{end}}{{if .CanInvite}}<a class="btn" href="/_pbc/mod/invites">Invites</a> {{end}}<a class="btn" href="/_pbc/mod/passkeys">Passkeys</a> <button type="button" id="mod-logout" class="btn">Sign out</button></span>
</div>
<form method="post" action="/_pbc/moderate/identity" class="mod-identity">
  <label>Your comment name <input type="text" name="alias" value="{{.Alias}}" maxlength="64" placeholder="blank = Anonymous"></label>
  <button type="submit" class="btn">Save name</button>
  <span class="hint">Shown next to your <strong>Moderator</strong> badge when you comment; unique across accounts.</span>
</form>
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
<form method="get" action="/_pbc/moderate" class="mod-filter">
  <label>Status
  <select name="status">
    <option value="pending"{{if eq .Status "pending"}} selected{{end}}>Pending</option>
    <option value="approved"{{if eq .Status "approved"}} selected{{end}}>Approved</option>
    <option value="rejected"{{if eq .Status "rejected"}} selected{{end}}>Rejected</option>
  </select></label>
  <label>Page <input type="text" name="q_page" value="{{.QPage}}" placeholder="path contains…"></label>
  <label>Author <input type="text" name="q_author" value="{{.QAuthor}}" placeholder="name contains…"></label>
  <label>Comment <input type="text" name="q_body" value="{{.QBody}}" placeholder="text contains…"></label>
  <label>From <input type="date" name="from" value="{{.From}}"></label>
  <label>To <input type="date" name="to" value="{{.To}}"></label>
  <label>Sort
  <select name="sort">
    <option value="posted"{{if eq .Sort "posted"}} selected{{end}}>Posted date</option>
    <option value="page"{{if eq .Sort "page"}} selected{{end}}>Page</option>
    <option value="author"{{if eq .Sort "author"}} selected{{end}}>Author</option>
  </select></label>
  <label>Order
  <select name="dir">
    <option value="desc"{{if eq .Dir "desc"}} selected{{end}}>Descending</option>
    <option value="asc"{{if eq .Dir "asc"}} selected{{end}}>Ascending</option>
  </select></label>
  <button type="submit" class="btn">Filter</button>
  <a class="linklike" href="/_pbc/moderate">Reset</a>
</form>
{{if .Rows}}
<p class="hint">Showing {{.RangeStart}}–{{.RangeEnd}} of {{.Total}} {{.Status}} comment{{if ne .Total 1}}s{{end}}.</p>
<div class="mod-scroll">
<table class="mod-table">
<thead><tr><th>Page</th><th>Author</th><th>Comment</th><th>Posted</th><th>Status</th><th>Actions</th></tr></thead>
<tbody>
{{range .Rows}}
<tr>
<td><code>{{.PagePath}}</code></td>
<td>{{.Alias}}{{if .Badge}} <span class="mod-badge">{{.Badge}}</span>{{end}}</td>
<td class="mod-cbody">{{if .IsReply}}<div class="reply-to">↳ in reply to {{.ReplyToAlias}}</div>{{end}}{{.Body}}</td>
<td class="mod-time">{{.Created}}</td>
<td class="mod-status">{{.Status}}</td>
<td class="mod-actions">
{{if .Staff}}<span class="hint">Staff comment — visible for context; only the site owner can moderate it (§2.4).</span>{{else}}
{{if eq .Status "pending"}}
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/approve"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Approve</button></form>
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/reject"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Reject</button></form>
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/delete" data-confirm="{{if .HasReplies}}Delete this comment and its {{.ReplyCount}} repl{{if eq .ReplyCount 1}}y{{else}}ies{{end}}? The replies are removed with it (§7.3). {{end}}This cannot be undone."><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Delete</button></form>
{{else if eq .Status "approved"}}
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/reject" data-confirm="Unpublish this comment? It will be hidden from the page."><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Unpublish</button></form>
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/delete" data-confirm="{{if .HasReplies}}Delete this comment and its {{.ReplyCount}} repl{{if eq .ReplyCount 1}}y{{else}}ies{{end}}? The replies are removed with it (§7.3). {{end}}This cannot be undone."><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Delete</button></form>
{{else}}
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/approve" data-confirm="Restore (publish) this comment?"><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="btn">Restore</button></form>
<form method="post" action="/_pbc/moderate/comments/{{.ID}}/delete" data-confirm="{{if .HasReplies}}Delete this comment and its {{.ReplyCount}} repl{{if eq .ReplyCount 1}}y{{else}}ies{{end}}? The replies are removed with it (§7.3). {{end}}This cannot be undone."><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Delete</button></form>
{{end}}
{{if and $.CanBan .AuthorID}}<form method="post" action="/_pbc/moderate/comments/{{.ID}}/ban-author" data-confirm="Ban {{if .Alias}}{{.Alias}}{{else}}this member{{end}}? Their sessions end now; their comments are untouched."><input type="hidden" name="ctx" value="{{$.FilterCtx}}"><input type="hidden" name="p" value="{{$.PageNo}}"><button type="submit" class="danger">Ban author</button></form>{{end}}
{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
</div>
{{if gt .TotalPages 1}}
<nav class="mod-pager" aria-label="Pagination">
{{if .PrevURL}}<a class="btn" href="{{.PrevURL}}" rel="prev">← Prev</a>{{else}}<span class="btn disabled">← Prev</span>{{end}}
<span>Page {{.PageNo}} of {{.TotalPages}}</span>
{{if .NextURL}}<a class="btn" href="{{.NextURL}}" rel="next">Next →</a>{{else}}<span class="btn disabled">Next →</span>{{end}}
</nav>
{{end}}
{{else}}
<p class="hint">No {{.Status}} comments match these filters.</p>
{{end}}
{{else}}
<p class="hint">Sign in with your moderator passkey to review comments. New moderators: use the invite you were given.</p>
<div class="mod-auth">
  <button type="button" id="mod-login" class="btn">Sign in with a passkey</button>
  <span id="mod-login-msg" class="hint"></span>
  <details>
    <summary>Have an invite? Register</summary>
    <form id="mod-register">
      <input type="text" id="mod-invite" placeholder="Invite code" autocomplete="off" required>
      <button type="submit" class="btn">Register a passkey</button>
      <span id="mod-register-msg" class="hint"></span>
    </form>
  </details>
</div>
{{end}}
</main>
<script src="/_pbc/mod/assets/moderate.js"></script>
</body>
</html>
`

// moderateCSS styles the standalone moderator page (self-hosted, light/dark aware).
const moderateCSS = `:root{--bg:#fff;--fg:#1a1a1a;--muted:#5a5a5a;--border:#d8d8d8;--accent:#0b5cad;--danger:#b00020;--card:#f6f6f6}
@media (prefers-color-scheme:dark){:root{--bg:#161616;--fg:#e8e8e8;--muted:#a0a0a0;--border:#3a3a3a;--accent:#6db3f2;--danger:#ff6b6b;--card:#1f1f1f}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
.mod{max-width:70rem;margin:0 auto;padding:1.5rem 1rem}
h1{font-size:1.5rem;margin:0 0 1rem}
.hint{color:var(--muted)}
.alert.ok{background:var(--card);border:1px solid var(--border);border-left:4px solid var(--accent);padding:.5rem .75rem;border-radius:6px}
.btn{display:inline-block;padding:.4rem .7rem;font:inherit;font-size:.9rem;color:var(--fg);background:var(--card);border:1px solid var(--border);border-radius:6px;cursor:pointer;text-decoration:none}
.btn:hover{border-color:var(--accent)}
.btn.disabled{opacity:.5;pointer-events:none}
.danger{padding:.4rem .7rem;font:inherit;font-size:.9rem;color:#fff;background:var(--danger);border:1px solid var(--danger);border-radius:6px;cursor:pointer}
.linklike{background:none;border:0;color:var(--accent);text-decoration:underline;cursor:pointer;font:inherit}
.mod-bar{display:flex;justify-content:space-between;align-items:center;gap:1rem;flex-wrap:wrap;margin-bottom:1rem}
.mod-identity{display:flex;flex-wrap:wrap;gap:.5rem;align-items:center;margin-bottom:1rem;font-size:.9em}
.mod-identity input{padding:.3em .5em}
.mod-filter{display:flex;flex-wrap:wrap;gap:.6rem .9rem;align-items:end;margin:1rem 0;padding:.75rem;border:1px solid var(--border);border-radius:8px;background:var(--card)}
.mod-filter label{display:flex;flex-direction:column;gap:.2rem;font-size:.78rem;color:var(--muted)}
.mod-filter input,.mod-filter select{padding:.35rem .4rem;font:inherit;font-size:.9rem;color:var(--fg);background:var(--bg);border:1px solid var(--border);border-radius:6px}
.mod-scroll{overflow-x:auto}
.mod-table{width:100%;border-collapse:collapse;font-size:.92rem}
.mod-table th,.mod-table td{text-align:left;padding:.5rem .6rem;border-top:1px solid var(--border);vertical-align:top}
.mod-cbody{white-space:pre-wrap;overflow-wrap:anywhere;max-width:28rem}
.mod-time{white-space:nowrap;color:var(--muted)}
.mod-status{text-transform:capitalize;color:var(--muted);font-size:.85em}
.mod-badge{font-size:.7em;font-weight:700;text-transform:uppercase;letter-spacing:.03em;color:var(--bg,#fff);background:var(--accent);padding:.05em .4em;border-radius:4px}
.reply-to{font-size:.82em;color:var(--muted);margin-bottom:.15rem}
.mod-actions{display:flex;flex-wrap:wrap;gap:.35rem}
.mod-actions form{display:inline}
.mod-pager{display:flex;gap:1rem;align-items:center;justify-content:center;margin:1.25rem 0}
.mod-auth{display:flex;flex-direction:column;gap:.75rem;align-items:flex-start;margin-top:1rem}
.mod-auth input{padding:.4rem;font:inherit;color:var(--fg);background:var(--bg);border:1px solid var(--border);border-radius:6px}
.mod-auth summary{cursor:pointer;color:var(--accent)}
.mod-auth form{display:flex;gap:.5rem;flex-wrap:wrap;align-items:center;margin-top:.5rem}
.alert.danger{background:var(--card);border:1px solid var(--danger);border-left:4px solid var(--danger);padding:.5rem .75rem;border-radius:6px;color:var(--danger)}
.mod-code{margin:1rem 0}
.mod-codebox{display:inline-block;padding:.5rem .75rem;background:var(--card);border:1px solid var(--border);border-radius:6px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;user-select:all;word-break:break-all}
`

// moderateJS is the self-hosted passkey auth + confirm-dialog script (no inline code, so
// a strict script-src 'self' allows it). It drives the moderator register/login/logout
// ceremonies against /_pbc/mod/auth and reloads the server-rendered page on success.
const moderateJS = `(function () {
  var API = '/_pbc/mod/auth';
  function b64urlToBuf(s){s=s.replace(/-/g,'+').replace(/_/g,'/');var pad=s.length%4;if(pad)s+='===='.slice(pad);var bin=atob(s),out=new Uint8Array(bin.length);for(var i=0;i<bin.length;i++)out[i]=bin.charCodeAt(i);return out.buffer;}
  function bufToB64url(buf){var b=new Uint8Array(buf),s='';for(var i=0;i<b.length;i++)s+=String.fromCharCode(b[i]);return btoa(s).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');}
  function postJSON(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body||{})});}

  function login(){
    return postJSON(API+'/login/options',{}).then(function(r){if(!r.ok)throw new Error('sign-in unavailable');return r.json();})
      .then(function(opt){var pk=opt.publicKey;pk.challenge=b64urlToBuf(pk.challenge);pk.allowCredentials=(pk.allowCredentials||[]).map(function(id){return{type:'public-key',id:b64urlToBuf(id)};});
        return navigator.credentials.get({publicKey:pk}).then(function(cred){var resp=cred.response;
          return postJSON(API+'/login/verify',{id:opt.id,credentialId:bufToB64url(cred.rawId),userHandle:resp.userHandle?bufToB64url(resp.userHandle):'',response:{clientDataJSON:bufToB64url(resp.clientDataJSON),authenticatorData:bufToB64url(resp.authenticatorData),signature:bufToB64url(resp.signature)}});});});
  }
  function register(invite){
    return postJSON(API+'/register/options',{invite:invite}).then(function(r){if(!r.ok)return r.text().then(function(t){throw new Error(t||'invite invalid');});return r.json();})
      .then(function(opt){var pk=opt.publicKey;pk.challenge=b64urlToBuf(pk.challenge);pk.user.id=b64urlToBuf(pk.user.id);pk.excludeCredentials=(pk.excludeCredentials||[]).map(function(id){return{type:'public-key',id:b64urlToBuf(id)};});
        return navigator.credentials.create({publicKey:pk}).then(function(cred){var resp=cred.response;
          return postJSON(API+'/register/verify',{id:opt.id,response:{clientDataJSON:bufToB64url(resp.clientDataJSON),attestationObject:bufToB64url(resp.attestationObject)},transports:(resp.getTransports&&resp.getTransports())||[]});});});
  }

  var loginBtn=document.getElementById('mod-login');
  if(loginBtn){var lmsg=document.getElementById('mod-login-msg');
    loginBtn.addEventListener('click',function(){loginBtn.disabled=true;lmsg.textContent='Waiting for your passkey…';
      login().then(function(r){if(r&&r.ok){location.reload();return;}return r.text().then(function(t){throw new Error(t||'sign-in failed');});})
        .catch(function(e){lmsg.textContent=e.message||'Sign-in failed.';loginBtn.disabled=false;});});}

  var regForm=document.getElementById('mod-register');
  if(regForm){var rmsg=document.getElementById('mod-register-msg');
    regForm.addEventListener('submit',function(e){e.preventDefault();var code=document.getElementById('mod-invite').value.trim();rmsg.textContent='Waiting for your passkey…';
      register(code).then(function(r){if(r&&r.ok){location.reload();return;}return r.text().then(function(t){throw new Error(t||'registration failed');});})
        .catch(function(er){rmsg.textContent=er.message||'Registration failed.';});});}

  var logoutBtn=document.getElementById('mod-logout');
  if(logoutBtn){logoutBtn.addEventListener('click',function(){postJSON(API+'/logout',{}).then(function(){location.reload();});});}

  // Passkey manager (present only on /_pbc/mod/passkeys): authenticated add-a-key ceremony.
  function addKey(label){
    return postJSON('/_pbc/mod/passkeys/add/options',{label:label}).then(function(r){if(!r.ok)return r.text().then(function(t){throw new Error(t||'could not start');});return r.json();})
      .then(function(opt){var pk=opt.publicKey;pk.challenge=b64urlToBuf(pk.challenge);pk.user.id=b64urlToBuf(pk.user.id);pk.excludeCredentials=(pk.excludeCredentials||[]).map(function(id){return{type:'public-key',id:b64urlToBuf(id)};});
        return navigator.credentials.create({publicKey:pk}).then(function(cred){var resp=cred.response;
          return postJSON('/_pbc/mod/passkeys/add/verify',{id:opt.id,response:{clientDataJSON:bufToB64url(resp.clientDataJSON),attestationObject:bufToB64url(resp.attestationObject)},transports:(resp.getTransports&&resp.getTransports())||[]});});});
  }
  var addBtn=document.getElementById('mod-addkey');
  if(addBtn){var alabel=document.getElementById('mod-addkey-label'),amsg=document.getElementById('mod-addkey-msg');
    addBtn.addEventListener('click',function(){addBtn.disabled=true;amsg.textContent='Waiting for your passkey…';
      addKey(alabel?alabel.value.trim():'').then(function(r){if(r&&r.ok){location.reload();return;}return r.text().then(function(t){throw new Error(t||'could not add key');});})
        .catch(function(e){amsg.textContent=e.message||'Could not add the passkey.';addBtn.disabled=false;});});}

  var confirmForms=document.querySelectorAll('form[data-confirm]');
  for(var i=0;i<confirmForms.length;i++){(function(f){f.addEventListener('submit',function(e){if(!confirm(f.getAttribute('data-confirm'))){e.preventDefault();}});})(confirmForms[i]);}
})();
`

// modPasskeysHTML is the moderator passkey manager page (html/template auto-escapes all
// user content). It reuses the moderate stylesheet and script.
const modPasskeysHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>Your passkeys</title>
<link rel="stylesheet" href="/_pbc/mod/assets/moderate.css">
</head>
<body>
<main class="mod">
<h1>Your passkeys</h1>
<div class="mod-bar">
  <span class="mod-who">Signed in{{if .Label}} as <strong>{{.Label}}</strong>{{end}}</span>
  <span class="mod-baractions"><a class="btn" href="/_pbc/moderate">← Moderation</a> <button type="button" id="mod-logout" class="btn">Sign out</button></span>
</div>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
<p class="hint">Register more than one passkey so you don't lose access if a device is lost (§2.4). You must always keep at least one.</p>
<div class="mod-auth">
  <input type="text" id="mod-addkey-label" placeholder="Label (e.g. Laptop)" autocomplete="off" maxlength="120">
  <button type="button" id="mod-addkey" class="btn">Add a passkey</button>
  <span id="mod-addkey-msg" class="hint"></span>
</div>
<div class="mod-scroll">
<table class="mod-table">
<thead><tr><th>Label</th><th>Transports</th><th>Added</th><th>Last used</th><th>Actions</th></tr></thead>
<tbody>
{{range .Passkeys}}
<tr>
<td>{{.Label}}</td>
<td>{{.Transports}}</td>
<td class="mod-time">{{.Created}}</td>
<td class="mod-time">{{.LastUsed}}</td>
<td class="mod-actions">
<form method="post" action="/_pbc/mod/passkeys/{{.ID}}/label"><input type="text" name="label" value="{{.Label}}" maxlength="120"><button type="submit" class="btn">Rename</button></form>
{{if $.OnlyOne}}<span class="hint">last key</span>{{else}}<form method="post" action="/_pbc/mod/passkeys/{{.ID}}/remove" data-confirm="Remove this passkey?"><button type="submit" class="danger">Remove</button></form>{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
</div>
</main>
<script src="/_pbc/mod/assets/moderate.js"></script>
</body>
</html>
`

// modInvitesHTML is the moderator member-invite page (html/template auto-escapes all
// user content). Reuses the moderate stylesheet and script (Sign out + confirm dialogs).
const modInvitesHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>Member invites</title>
<link rel="stylesheet" href="/_pbc/mod/assets/moderate.css">
</head>
<body>
<main class="mod">
<h1>Member invites</h1>
<div class="mod-bar">
  <span class="mod-who">Signed in{{if .Label}} as <strong>{{.Label}}</strong>{{end}}</span>
  <span class="mod-baractions"><a class="btn" href="/_pbc/moderate">← Moderation</a> <button type="button" id="mod-logout" class="btn">Sign out</button></span>
</div>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
{{if .MintedCode}}
<div class="mod-code">
  <p class="hint">Give this code to the person you're inviting. It won't be shown again.</p>
  <code class="mod-codebox">{{.MintedCode}}</code>
</div>
{{end}}
<p class="hint">Invites you create are member-only and expire in 30 days. You may hold up to {{.Cap}} unredeemed at a time ({{.Outstanding}} in use).</p>
<form method="post" action="/_pbc/mod/invites/mint">
  <button type="submit" class="btn"{{if .AtCap}} disabled{{end}}>Generate a member invite</button>
  {{if .AtCap}}<span class="hint">At your limit — revoke an unused invite to free a slot.</span>{{end}}
</form>
{{if .Invites}}
<div class="mod-scroll">
<table class="mod-table">
<thead><tr><th>Created</th><th>Expires</th><th>Status</th><th>Actions</th></tr></thead>
<tbody>
{{range .Invites}}
<tr>
<td class="mod-time">{{.Created}}</td>
<td class="mod-time">{{.Expires}}</td>
<td class="mod-status">{{.Status}}</td>
<td class="mod-actions">{{if .Live}}<form method="post" action="/_pbc/mod/invites/revoke" data-confirm="Revoke this invite?"><input type="hidden" name="lineage" value="{{.Lineage}}"><button type="submit" class="danger">Revoke</button></form>{{else}}<span class="hint">—</span>{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
</div>
{{else}}
<p class="hint">You haven't created any invites yet.</p>
{{end}}
</main>
<script src="/_pbc/mod/assets/moderate.js"></script>
</body>
</html>
`

// modAccountsHTML is the moderator account-moderation page (member soft-ban/unban).
const modAccountsHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>Members</title>
<link rel="stylesheet" href="/_pbc/mod/assets/moderate.css">
</head>
<body>
<main class="mod">
<h1>Members</h1>
<div class="mod-bar">
  <span class="mod-who">Signed in{{if .Label}} as <strong>{{.Label}}</strong>{{end}}</span>
  <span class="mod-baractions"><a class="btn" href="/_pbc/moderate">← Moderation</a> <button type="button" id="mod-logout" class="btn">Sign out</button></span>
</div>
{{if .Error}}<p class="alert danger">{{.Error}}</p>{{end}}
{{if .Notice}}<p class="alert ok">{{.Notice}}</p>{{end}}
<p class="hint">Ban blocks a member and revokes their sessions; it's reversible with un-ban and leaves their comments in place. Permanently erasing an account is the site owner's action, not yours.</p>
{{if .Accounts}}
<div class="mod-scroll">
<table class="mod-table">
<thead><tr><th>Member</th><th>Name</th><th>Comments</th><th>Joined</th><th>Last seen</th><th>Status</th><th>Actions</th></tr></thead>
<tbody>
{{range .Accounts}}
<tr>
<td><code>{{.Handle}}</code></td>
<td>{{if .Alias}}{{.Alias}}{{else}}<span class="hint">(anonymous)</span>{{end}}</td>
<td>{{.Comments}}</td>
<td class="mod-time">{{.Created}}</td>
<td class="mod-time">{{.LastSeen}}</td>
<td class="mod-status">{{if .Banned}}banned{{else}}active{{end}}</td>
<td class="mod-actions">
{{if .Banned}}<form method="post" action="/_pbc/mod/accounts/{{.ID}}/unban"><button type="submit" class="btn">Un-ban</button></form>{{else}}<form method="post" action="/_pbc/mod/accounts/{{.ID}}/ban" data-confirm="Ban this member? Their sessions end now."><button type="submit" class="danger">Ban</button></form>{{end}}
</td>
</tr>
{{end}}
</tbody>
</table>
</div>
{{else}}
<p class="hint">No members yet.</p>
{{end}}
</main>
<script src="/_pbc/mod/assets/moderate.js"></script>
</body>
</html>
`
