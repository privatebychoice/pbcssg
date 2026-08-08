package creator

// registerJS drives the creator registration ceremony (§2.4) from the register
// page: it posts the invite to /admin/auth/register/options, runs
// navigator.credentials.create with the returned options, and posts the attestation
// to /admin/auth/register/verify. Self-hosted vanilla JS, no framework (§8). Binary
// WebAuthn fields cross the wire as base64url and are converted to/from ArrayBuffers
// here.
const registerJS = `(function () {
  var form = document.getElementById('register-form');
  var btn = document.getElementById('register-btn');
  var statusEl = document.getElementById('status');
  var csrf = document.getElementById('csrf').value;

  function setStatus(msg, isError) {
    statusEl.textContent = msg;
    statusEl.className = isError ? 'error' : '';
  }

  function b64urlToBuf(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    var pad = s.length % 4;
    if (pad) { s += '===='.slice(pad); }
    var bin = atob(s);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
    return out.buffer;
  }
  function bufToB64url(buf) {
    var bytes = new Uint8Array(buf), bin = '';
    for (var i = 0; i < bytes.length; i++) { bin += String.fromCharCode(bytes[i]); }
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify(body)
    });
  }

  function register(invite) {
    if (!window.PublicKeyCredential || !navigator.credentials) {
      setStatus('This browser does not support passkeys (WebAuthn).', true);
      return;
    }
    setStatus('Requesting a challenge…', false);
    postJSON('/admin/auth/register/options', { invite: invite }).then(function (r) {
      if (!r.ok) { return r.text().then(function (t) { throw new Error(t || 'options failed'); }); }
      return r.json();
    }).then(function (opt) {
      var pk = opt.publicKey;
      pk.challenge = b64urlToBuf(pk.challenge);
      pk.user.id = b64urlToBuf(pk.user.id);
      pk.excludeCredentials = (pk.excludeCredentials || []).map(function (id) {
        return { type: 'public-key', id: b64urlToBuf(id) };
      });
      setStatus('Waiting for your authenticator…', false);
      return navigator.credentials.create({ publicKey: pk }).then(function (cred) {
        var resp = cred.response;
        var transports = (resp.getTransports && resp.getTransports()) || [];
        return postJSON('/admin/auth/register/verify', {
          id: opt.id,
          response: {
            clientDataJSON: bufToB64url(resp.clientDataJSON),
            attestationObject: bufToB64url(resp.attestationObject)
          },
          transports: transports
        });
      });
    }).then(function (r) {
      if (r && r.ok) {
        setStatus('Registered. Redirecting…', false);
        window.location = '/';
      } else if (r) {
        return r.text().then(function (t) { throw new Error(t || 'verification failed'); });
      }
    }).catch(function (e) {
      // A user-cancelled ceremony surfaces as NotAllowedError; keep the message plain.
      setStatus(e && e.message ? e.message : 'Registration failed.', true);
      btn.disabled = false;
    });
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var invite = document.getElementById('invite').value.trim();
    if (!invite) { setStatus('Enter your invite code.', true); return; }
    btn.disabled = true;
    register(invite);
  });
})();
`

// passkeysJS drives the authenticated add-a-passkey ceremony (§2.4, A1) from the
// Passkeys admin page: it posts an optional label to /admin/passkeys/add/options, runs
// navigator.credentials.create with the returned options (excludeCredentials keeps the
// same authenticator from enrolling twice), and posts the attestation to
// /admin/passkeys/add/verify. On success it reloads the page to show the new key.
// Self-hosted vanilla JS (§8); the same binary base64url conversions as registerJS.
const passkeysJS = `(function () {
  var btn = document.getElementById('add-passkey-btn');
  if (!btn) { return; }
  var labelEl = document.getElementById('passkey-label');
  var statusEl = document.getElementById('add-status');
  var csrf = document.getElementById('csrf').value;

  function setStatus(msg, isError) {
    statusEl.textContent = msg;
    statusEl.className = isError ? 'error' : '';
  }
  function b64urlToBuf(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    var pad = s.length % 4;
    if (pad) { s += '===='.slice(pad); }
    var bin = atob(s);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
    return out.buffer;
  }
  function bufToB64url(buf) {
    var bytes = new Uint8Array(buf), bin = '';
    for (var i = 0; i < bytes.length; i++) { bin += String.fromCharCode(bytes[i]); }
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify(body || {})
    });
  }

  function add(label) {
    if (!window.PublicKeyCredential || !navigator.credentials) {
      setStatus('This browser does not support passkeys (WebAuthn).', true);
      btn.disabled = false;
      return;
    }
    setStatus('Requesting a challenge…', false);
    postJSON('/admin/passkeys/add/options', { label: label }).then(function (r) {
      if (!r.ok) { return r.text().then(function (t) { throw new Error(t || 'options failed'); }); }
      return r.json();
    }).then(function (opt) {
      var pk = opt.publicKey;
      pk.challenge = b64urlToBuf(pk.challenge);
      pk.user.id = b64urlToBuf(pk.user.id);
      pk.excludeCredentials = (pk.excludeCredentials || []).map(function (id) {
        return { type: 'public-key', id: b64urlToBuf(id) };
      });
      setStatus('Waiting for your authenticator…', false);
      return navigator.credentials.create({ publicKey: pk }).then(function (cred) {
        var resp = cred.response;
        var transports = (resp.getTransports && resp.getTransports()) || [];
        return postJSON('/admin/passkeys/add/verify', {
          id: opt.id,
          response: {
            clientDataJSON: bufToB64url(resp.clientDataJSON),
            attestationObject: bufToB64url(resp.attestationObject)
          },
          transports: transports
        });
      });
    }).then(function (r) {
      if (r && r.ok) {
        setStatus('Added. Reloading…', false);
        window.location = '/admin/passkeys';
      } else if (r) {
        return r.text().then(function (t) { throw new Error(t || 'verification failed'); });
      }
    }).catch(function (e) {
      setStatus(e && e.message ? e.message : 'Could not add the passkey.', true);
      btn.disabled = false;
    });
  }

  btn.addEventListener('click', function () {
    btn.disabled = true;
    add(labelEl ? labelEl.value.trim() : '');
  });
})();
`

// loginJS drives the creator login (assertion) ceremony (§2.4): options -> passkey
// get -> verify. Usernameless (discoverable credentials), so there is no username
// field — the authenticator offers the passkey for this RP. Self-hosted vanilla JS.
const loginJS = `(function () {
  var btn = document.getElementById('login-btn');
  var statusEl = document.getElementById('status');
  var csrf = document.getElementById('csrf').value;

  function setStatus(msg, isError) {
    statusEl.textContent = msg;
    statusEl.className = isError ? 'error' : '';
  }
  function b64urlToBuf(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    var pad = s.length % 4;
    if (pad) { s += '===='.slice(pad); }
    var bin = atob(s);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) { out[i] = bin.charCodeAt(i); }
    return out.buffer;
  }
  function bufToB64url(buf) {
    var bytes = new Uint8Array(buf), bin = '';
    for (var i = 0; i < bytes.length; i++) { bin += String.fromCharCode(bytes[i]); }
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function postJSON(url, body) {
    return fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify(body || {})
    });
  }

  function login() {
    if (!window.PublicKeyCredential || !navigator.credentials) {
      setStatus('This browser does not support passkeys (WebAuthn).', true);
      return;
    }
    setStatus('Requesting a challenge…', false);
    postJSON('/admin/auth/login/options', {}).then(function (r) {
      if (!r.ok) { return r.text().then(function (t) { throw new Error(t || 'options failed'); }); }
      return r.json();
    }).then(function (opt) {
      var pk = opt.publicKey;
      pk.challenge = b64urlToBuf(pk.challenge);
      pk.allowCredentials = (pk.allowCredentials || []).map(function (id) {
        return { type: 'public-key', id: b64urlToBuf(id) };
      });
      setStatus('Waiting for your authenticator…', false);
      return navigator.credentials.get({ publicKey: pk }).then(function (cred) {
        var resp = cred.response;
        return postJSON('/admin/auth/login/verify', {
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
    }).then(function (r) {
      if (r && r.ok) {
        setStatus('Signed in. Redirecting…', false);
        window.location = '/';
      } else if (r) {
        return r.text().then(function (t) { throw new Error(t || 'sign-in failed'); });
      }
    }).catch(function (e) {
      setStatus(e && e.message ? e.message : 'Sign-in failed.', true);
      btn.disabled = false;
    });
  }

  btn.addEventListener('click', function () {
    btn.disabled = true;
    login();
  });
})();
`
