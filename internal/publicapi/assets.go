package publicapi

import (
	"net/http"

	"go.privatebychoice.com/pbcssg/internal/render"
)

// registerAssetRoutes serves the self-hosted comments widget (§8: no third-party
// resources). The paths are render.CommentsJSPath / render.CommentsCSSPath — the same
// fixed URLs the build links from a page carrying a comments block, so the serving and
// linking sides share one source of truth. The assets are always available; the widget
// degrades gracefully to read-only when member auth is off (the /_pbc/auth/* endpoints
// 404).
func (a *api) registerAssetRoutes() {
	a.mux.HandleFunc("GET "+render.CommentsJSPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(commentsJS))
	})
	a.mux.HandleFunc("GET "+render.CommentsCSSPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(commentsCSS))
	})
}

// commentsCSS styles the comments widget using the site's theme tokens (with safe
// fallbacks) so it matches the built page in light and dark.
const commentsCSS = `.pbc-comments{margin-block:2rem;max-width:var(--measure,100%)}
.pbc-comments-title{font-size:1.2rem;margin-block:0 .75rem}
.pbc-comments-list{margin-block:1rem}
.pbc-comment{border-top:1px solid var(--border,#d8d8d8);padding-block:.75rem}
.pbc-comment.pbc-reply{margin-inline-start:1.5rem;border-top:1px dotted var(--border,#d8d8d8);border-inline-start:2px solid var(--border,#d8d8d8);padding-inline-start:.75rem}
.pbc-comment-meta{display:flex;gap:.5rem;align-items:baseline;font-size:.85em;color:var(--muted,#5a5a5a);flex-wrap:wrap}
.pbc-comment-alias{font-weight:600}
.pbc-comment-you{font-weight:600;font-size:.7em;text-transform:uppercase;letter-spacing:.03em;color:var(--accent,#0b5cad);border:1px solid var(--accent,#0b5cad);border-radius:4px;padding:.02em .35em;align-self:center}
.pbc-comment-mod{font-weight:700;font-size:.72em;letter-spacing:.03em;text-transform:uppercase;color:var(--bg,#fff);background:var(--accent,#0b5cad);padding:.05em .4em;border-radius:4px;align-self:center}
.pbc-comment-mod.pbc-author{background:var(--accent2,#6d28d9)}
.pbc-comment-body{white-space:pre-wrap;overflow-wrap:break-word;margin-block:.25rem 0}
.pbc-comment-body.pbc-comment-deleted{white-space:normal;font-style:italic;color:var(--muted,#5a5a5a)}
.pbc-comment-actions{margin-block:.35rem 0;font-size:.85em;display:flex;gap:.5rem;align-items:center}
.pbc-comments-empty{color:var(--muted,#5a5a5a)}
.pbc-comments-auth{margin-block:1rem}
.pbc-comments-identity{margin-block:.25rem .5rem;font-size:.9em;display:flex;flex-wrap:wrap;gap:.35rem;align-items:center}
.pbc-comments-account{margin-block:.75rem;font-size:.9em;display:flex;flex-wrap:wrap;gap:.35rem;align-items:center}
.pbc-comments .linklike{background:none;border:0;padding:0;color:var(--accent,#0b5cad);cursor:pointer;font:inherit;text-decoration:underline}
.pbc-comments .linklike.pbc-danger{color:var(--danger,#b00020)}
.pbc-comments-form textarea{display:block;width:100%;min-height:5rem;box-sizing:border-box;margin-block:.25rem}
.pbc-comments-form.pbc-reply-form{margin-block:.5rem}
.pbc-comments-form.pbc-reply-form textarea{min-height:3.5rem}
.pbc-comments-form input[type=text]{display:block;width:100%;max-width:24rem;box-sizing:border-box;margin-block:.25rem}
.pbc-comments-note{color:var(--muted,#5a5a5a);font-size:.9em}
.pbc-comments-error{color:var(--danger,#b00020)}
.pbc-comments details{margin-block:.5rem}
`

// commentsJS is the self-hosted comments widget. It mounts on any
// <section data-pbc-comments="/path"> (or uses the current path), renders approved comments
// threaded one level deep (always via textContent — user text is never injected as markup),
// and, reusing Community Member / moderator auth, shows a compose box, per-comment Reply, and
// a Delete control on the viewer's own comments. The display name is the signed-in account's
// single alias (unique across accounts), changed in one place, never per post. Vanilla JS,
// no framework.
const commentsJS = `(function () {
  var API = '/_pbc';

  function el(tag, cls, txt) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (txt != null) e.textContent = txt; // textContent: user content never becomes markup
    return e;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  function sep(n) { if (n.childNodes.length) n.appendChild(document.createTextNode(' · ')); }
  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {})
    });
  }
  function okJSON(r) { return r.ok ? r.json() : r.text().then(function (t) { throw new Error(t || 'request failed'); }); }
  function b64urlToBuf(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    var pad = s.length % 4; if (pad) s += '===='.slice(pad);
    var bin = atob(s), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }
  function bufToB64url(buf) {
    var b = new Uint8Array(buf), s = '';
    for (var i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function badge(role) {
    // Staff badge: creators read as "Author" to a visitor, moderators as "Moderator".
    var isAuthor = role === 'creator';
    return el('span', 'pbc-comment-mod' + (isAuthor ? ' pbc-author' : ''), isAuthor ? 'Author' : 'Moderator');
  }

  function passkeyLogin() {
    return postJSON(API + '/auth/login/options', {})
      .then(function (r) { if (!r.ok) throw new Error('login unavailable'); return r.json(); })
      .then(function (opt) {
        var pk = opt.publicKey;
        pk.challenge = b64urlToBuf(pk.challenge);
        pk.allowCredentials = (pk.allowCredentials || []).map(function (id) { return { type: 'public-key', id: b64urlToBuf(id) }; });
        return navigator.credentials.get({ publicKey: pk }).then(function (cred) {
          var resp = cred.response;
          return postJSON(API + '/auth/login/verify', {
            id: opt.id,
            credentialId: bufToB64url(cred.rawId),
            userHandle: resp.userHandle ? bufToB64url(resp.userHandle) : '',
            response: {
              clientDataJSON: bufToB64url(resp.clientDataJSON),
              authenticatorData: bufToB64url(resp.authenticatorData),
              signature: bufToB64url(resp.signature)
            }
          });
        });
      });
  }

  function passkeyRegister(invite) {
    return postJSON(API + '/auth/register/options', { invite: invite })
      .then(function (r) { if (!r.ok) return r.text().then(function (t) { throw new Error(t || 'invite invalid'); }); return r.json(); })
      .then(function (opt) {
        var pk = opt.publicKey;
        pk.challenge = b64urlToBuf(pk.challenge);
        pk.user.id = b64urlToBuf(pk.user.id);
        pk.excludeCredentials = (pk.excludeCredentials || []).map(function (id) { return { type: 'public-key', id: b64urlToBuf(id) }; });
        return navigator.credentials.create({ publicKey: pk }).then(function (cred) {
          var resp = cred.response;
          return postJSON(API + '/auth/register/verify', {
            id: opt.id,
            response: { clientDataJSON: bufToB64url(resp.clientDataJSON), attestationObject: bufToB64url(resp.attestationObject) },
            transports: (resp.getTransports && resp.getTransports()) || []
          });
        });
      });
  }

  // signInArea renders the passkey sign-in and invite-registration UI; done() is called
  // once a member/moderator is authenticated so the caller can reload.
  function signInArea(authEl, done) {
    clear(authEl);
    if (!window.PublicKeyCredential || !navigator.credentials) {
      authEl.appendChild(el('p', 'pbc-comments-note', 'Sign in with a passkey to comment (this browser does not support passkeys).'));
      return;
    }
    var msg = el('span', 'pbc-comments-note');
    var login = el('button', null, 'Sign in with a passkey'); login.type = 'button';
    login.addEventListener('click', function () {
      login.disabled = true; msg.textContent = 'Waiting for your passkey…';
      passkeyLogin().then(function (r) { if (r && r.ok) { done(); } else { throw new Error('sign-in failed'); } })
        .catch(function (e) { msg.className = 'pbc-comments-error'; msg.textContent = e.message || 'Sign-in failed.'; login.disabled = false; });
    });
    authEl.appendChild(login); authEl.appendChild(document.createTextNode(' ')); authEl.appendChild(msg);

    var det = el('details');
    det.appendChild(el('summary', null, 'Have an invite? Register'));
    var reg = el('form', 'pbc-comments-form');
    var code = el('input'); code.type = 'text'; code.placeholder = 'Invite code'; code.required = true;
    var rbtn = el('button', null, 'Register a passkey'); rbtn.type = 'submit';
    var rmsg = el('span', 'pbc-comments-note');
    reg.appendChild(code); reg.appendChild(rbtn); reg.appendChild(document.createTextNode(' ')); reg.appendChild(rmsg);
    reg.addEventListener('submit', function (e) {
      e.preventDefault();
      rbtn.disabled = true; rmsg.className = 'pbc-comments-note'; rmsg.textContent = 'Waiting for your passkey…';
      passkeyRegister(code.value.trim()).then(function (r) { if (r && r.ok) { done(); } else { throw new Error('registration failed'); } })
        .catch(function (er) { rmsg.className = 'pbc-comments-error'; rmsg.textContent = er.message || 'Registration failed.'; rbtn.disabled = false; });
    });
    det.appendChild(reg);
    authEl.appendChild(det);
  }

  // mount wires one comments section: the list (threaded), and the auth/compose area. All the
  // per-section state (path, elements, current auth) is captured in this closure.
  function mount(section) {
    var path = section.getAttribute('data-pbc-comments') || location.pathname;
    clear(section);
    section.classList.add('pbc-comments');
    section.appendChild(el('h2', 'pbc-comments-title', 'Comments'));
    var list = el('div', 'pbc-comments-list'); section.appendChild(list);
    var authEl = el('div', 'pbc-comments-auth'); section.appendChild(authEl);

    // auth: { signedIn, staff, role, alias }. Reloaded by loadAuth().
    var auth = { signedIn: false, staff: false, role: '', alias: '' };

    function renderComment(c, isReply) {
      var item = el('div', 'pbc-comment' + (isReply ? ' pbc-reply' : ''));
      var meta = el('div', 'pbc-comment-meta');
      if (c.deleted) {
        meta.appendChild(el('span', 'pbc-comment-alias', '[deleted]'));
        item.appendChild(meta);
        item.appendChild(el('div', 'pbc-comment-body pbc-comment-deleted', 'This comment was deleted.'));
        return item;
      }
      if (c.role) { meta.appendChild(badge(c.role)); }
      meta.appendChild(el('span', 'pbc-comment-alias', c.alias || 'Anonymous'));
      if (c.mine) { meta.appendChild(el('span', 'pbc-comment-you', 'You')); }
      var d = new Date((c.createdAt || 0) * 1000);
      meta.appendChild(el('time', 'pbc-comment-time', isNaN(d) ? '' : d.toLocaleDateString()));
      item.appendChild(meta);
      item.appendChild(el('div', 'pbc-comment-body', c.body || ''));

      var actions = el('div', 'pbc-comment-actions');
      // Reply is offered on roots only (threading is one level deep) to a signed-in visitor.
      if (auth.signedIn && !isReply) {
        var replyBtn = el('button', 'linklike', 'Reply'); replyBtn.type = 'button';
        replyBtn.addEventListener('click', function () { toggleReply(item, c.id); });
        actions.appendChild(replyBtn);
      }
      if (c.mine) {
        var delBtn = el('button', 'linklike pbc-danger', 'Delete'); delBtn.type = 'button';
        delBtn.addEventListener('click', function () { removeComment(c.id); });
        actions.appendChild(delBtn);
      }
      if (actions.childNodes.length) item.appendChild(actions);
      return item;
    }

    function renderList() {
      fetch(API + '/comments?path=' + encodeURIComponent(path)).then(function (r) {
        return r.ok ? r.json() : { comments: [] };
      }).then(function (data) {
        clear(list);
        var cs = (data && data.comments) || [];
        if (!cs.length) { list.appendChild(el('p', 'pbc-comments-empty', 'No comments yet.')); return; }
        var byParent = {}, roots = [];
        cs.forEach(function (c) {
          if (c.parentId) { (byParent[c.parentId] = byParent[c.parentId] || []).push(c); }
          else { roots.push(c); }
        });
        var rendered = {};
        roots.forEach(function (r) {
          rendered[r.id] = true;
          list.appendChild(renderComment(r, false));
          (byParent[r.id] || []).forEach(function (rep) { rendered[rep.id] = true; list.appendChild(renderComment(rep, true)); });
        });
        // Fallback: a reply whose root isn't in the approved set (e.g. root still pending)
        // is shown at the top level rather than dropped.
        cs.forEach(function (c) { if (!rendered[c.id]) { list.appendChild(renderComment(c, false)); } });
      });
    }

    function toggleReply(item, parentId) {
      var existing = item.querySelector('.pbc-reply-form');
      if (existing) { existing.parentNode.removeChild(existing); return; }
      var form = el('form', 'pbc-comments-form pbc-reply-form');
      var body = el('textarea'); body.required = true; body.placeholder = 'Write a reply…'; body.maxLength = 4096;
      var btn = el('button', null, 'Post reply'); btn.type = 'submit';
      var out = el('span', 'pbc-comments-note');
      form.appendChild(body); form.appendChild(btn); form.appendChild(el('div')).appendChild(out);
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        btn.disabled = true; out.className = 'pbc-comments-note'; out.textContent = 'Posting…';
        postJSON(API + '/comments', { path: path, parentId: parentId, body: body.value.trim() }).then(okJSON)
          .then(function (res) {
            if (res && res.status === 'approved') { renderList(); } // staff reply is public now
            else { body.value = ''; out.textContent = 'Thanks — your reply is awaiting review.'; btn.disabled = false; }
          })
          .catch(function (err) { out.className = 'pbc-comments-error'; out.textContent = err.message || 'Could not post.'; btn.disabled = false; });
      });
      item.appendChild(form);
      body.focus();
    }

    function removeComment(id) {
      if (!window.confirm('Delete your comment? If it has replies, they are kept and it shows as “[deleted]”.')) { return; }
      postJSON(API + '/comments/' + id + '/delete', {}).then(okJSON)
        .then(function () { renderList(); })
        .catch(function () { window.alert('Could not delete the comment.'); });
    }

    // renderCompose draws the identity line + a root-comment box for a signed-in user, or the
    // sign-in area otherwise. Members also get sign-out / delete-account; staff get a link to
    // the moderation page instead.
    function renderCompose() {
      clear(authEl);
      if (!auth.signedIn) { signInArea(authEl, loadAuth); return; }

      var ident = el('div', 'pbc-comments-identity');
      ident.appendChild(document.createTextNode('Commenting as '));
      ident.appendChild(el('strong', null, auth.alias || 'Anonymous'));
      var rename = el('button', 'linklike', 'change name'); rename.type = 'button';
      ident.appendChild(rename);
      var inmsg = el('span', 'pbc-comments-note');
      ident.appendChild(inmsg);
      authEl.appendChild(ident);
      rename.addEventListener('click', function () { changeName(ident, rename, inmsg); });

      var form = el('form', 'pbc-comments-form');
      var body = el('textarea'); body.required = true; body.placeholder = 'Add a comment…'; body.maxLength = 4096;
      var btn = el('button', null, 'Post comment'); btn.type = 'submit';
      var out = el('span', 'pbc-comments-note');
      form.appendChild(body); form.appendChild(btn); form.appendChild(el('div')).appendChild(out);
      authEl.appendChild(form);
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        btn.disabled = true; out.className = 'pbc-comments-note'; out.textContent = 'Posting…';
        postJSON(API + '/comments', { path: path, body: body.value.trim() }).then(okJSON)
          .then(function (res) {
            body.value = '';
            if (res && res.status === 'approved') { out.textContent = 'Posted.'; renderList(); }
            else { out.textContent = 'Thanks — your comment is awaiting review.'; }
            btn.disabled = false;
          })
          .catch(function (err) { out.className = 'pbc-comments-error'; out.textContent = err.message || 'Could not post.'; btn.disabled = false; });
      });

      if (auth.staff) {
        var mnote = el('p', 'pbc-comments-note');
        mnote.appendChild(document.createTextNode('You are signed in as a '));
        var mlink = el('a', null, 'moderator'); mlink.href = '/_pbc/moderate';
        mnote.appendChild(mlink);
        mnote.appendChild(document.createTextNode(' — your comments are labelled and auto-approved.'));
        authEl.appendChild(mnote);
        return;
      }

      // Member self-service: sign out, or delete my account.
      var acct = el('div', 'pbc-comments-account');
      var signout = el('button', 'linklike', 'Sign out'); signout.type = 'button';
      var del = el('button', 'linklike', 'Delete my account'); del.type = 'button';
      var amsg = el('span', 'pbc-comments-note');
      acct.appendChild(signout); sep(acct); acct.appendChild(del); acct.appendChild(el('div')).appendChild(amsg);
      authEl.appendChild(acct);
      signout.addEventListener('click', function () { postJSON(API + '/auth/logout', {}).then(function () { loadAuth(); }); });
      del.addEventListener('click', function () { deleteAccount(acct); });
    }

    function changeName(ident, rename, inmsg) {
      if (ident.querySelector('input')) { return; }
      rename.disabled = true;
      var input = el('input'); input.type = 'text'; input.maxLength = 64; input.value = auth.alias || ''; input.placeholder = 'Name (blank = anonymous)';
      var save = el('button', 'linklike', 'save'); save.type = 'button';
      var cancel = el('button', 'linklike', 'cancel'); cancel.type = 'button';
      ident.appendChild(input); sep(ident); ident.appendChild(save); sep(ident); ident.appendChild(cancel);
      input.focus();
      function done() { renderCompose(); }
      cancel.addEventListener('click', done);
      save.addEventListener('click', function () {
        inmsg.className = 'pbc-comments-note'; inmsg.textContent = 'Saving…';
        postJSON(API + '/account/alias', { alias: input.value.trim() }).then(okJSON)
          .then(function (d) { auth.alias = (d && d.alias) || ''; renderList(); renderCompose(); })
          .catch(function (err) {
            // A 409 body explains the name is taken; keep the editor open so they can retry.
            inmsg.className = 'pbc-comments-error';
            inmsg.textContent = /taken/i.test(err.message) ? 'That name is already taken.' : (err.message || 'Could not save.');
          });
      });
    }

    function deleteAccount(acct) {
      clear(acct);
      acct.appendChild(el('p', 'pbc-comments-note', 'Delete your account? This cannot be undone.'));
      var keepBtn = el('button', 'linklike', 'Delete, keep my comments (anonymized)'); keepBtn.type = 'button';
      var wipeBtn = el('button', 'linklike pbc-danger', 'Delete account and comments'); wipeBtn.type = 'button';
      var cancel = el('button', 'linklike', 'Cancel'); cancel.type = 'button';
      var dmsg = el('span', 'pbc-comments-note');
      acct.appendChild(keepBtn); sep(acct); acct.appendChild(wipeBtn); sep(acct); acct.appendChild(cancel);
      acct.appendChild(el('div')).appendChild(dmsg);
      function forget(deleteComments) {
        dmsg.className = 'pbc-comments-note'; dmsg.textContent = 'Deleting…';
        postJSON(API + '/account/forget', { deleteComments: deleteComments }).then(okJSON)
          .then(function () { loadAuth(); renderList(); })
          .catch(function (err) { dmsg.className = 'pbc-comments-error'; dmsg.textContent = err.message || 'Could not delete.'; });
      }
      keepBtn.addEventListener('click', function () { forget(false); });
      wipeBtn.addEventListener('click', function () { forget(true); });
      cancel.addEventListener('click', function () { renderCompose(); });
    }

    // loadAuth resolves the session (member first, then moderator), stores it, and repaints
    // both the compose area and the list (so "You"/Delete reflect the signed-in viewer).
    function loadAuth() {
      fetch(API + '/auth/me').then(function (r) { return r.ok ? r.json() : { authenticated: false }; })
        .then(function (me) {
          if (me && me.authenticated) { auth = { signedIn: true, staff: false, role: 'member', alias: me.alias || '' }; renderCompose(); renderList(); return; }
          return fetch(API + '/mod/auth/me').then(function (r) { return r.ok ? r.json() : { authenticated: false }; })
            .then(function (mm) {
              if (mm && mm.authenticated) { auth = { signedIn: true, staff: true, role: 'moderator', alias: mm.alias || '' }; }
              else { auth = { signedIn: false, staff: false, role: '', alias: '' }; }
              renderCompose(); renderList();
            });
        })
        .catch(function () { auth = { signedIn: false, staff: false, role: '', alias: '' }; clear(authEl); renderList(); }); // auth disabled -> read-only
    }

    loadAuth();
  }

  var nodes = document.querySelectorAll('[data-pbc-comments]');
  for (var i = 0; i < nodes.length; i++) mount(nodes[i]);
})();
`
