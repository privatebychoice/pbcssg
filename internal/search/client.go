package search

// ClientJS is the self-hosted, dependency-free client-side search matcher
// (SPEC §6.2). It lazy-loads the index (only when the user starts typing),
// matches entirely in the browser — so the query never leaves the device — and
// renders the top results as links. It uses no inline code and only a
// same-origin fetch, so a strict default-src 'self' CSP allows it. With
// JavaScript disabled the search box is simply inert (graceful degradation).
const ClientJS = `(function () {
  var input = document.querySelector('[data-pbcssg-search]');
  var results = document.getElementById('pbcssg-search-results');
  if (!input || !results) return;

  var docs = null, loading = false;

  function load(then) {
    if (docs) { then(); return; }
    if (loading) return;
    loading = true;
    // cache:'no-cache' forces a revalidation so a rebuilt index (new pages) is
    // never masked by a stale cached copy; the server's ETag keeps it a cheap 304.
    fetch('/search/index.json', { credentials: 'omit', cache: 'no-cache' })
      .then(function (r) { return r.json(); })
      .then(function (data) { docs = (data && data.docs) || []; then(); })
      .catch(function () { loading = false; });
  }

  function terms(q) {
    return q.toLowerCase().split(/\s+/).filter(Boolean);
  }

  function score(doc, ts) {
    var title = (doc.title || '').toLowerCase();
    var text = (doc.text || '').toLowerCase();
    var s = 0;
    for (var i = 0; i < ts.length; i++) {
      if (title.indexOf(ts[i]) !== -1) s += 5;
      if (text.indexOf(ts[i]) !== -1) s += 1;
    }
    return s;
  }

  function render(q) {
    results.textContent = '';
    var ts = terms(q);
    if (!ts.length) return;
    var scored = [];
    for (var i = 0; i < docs.length; i++) {
      var s = score(docs[i], ts);
      if (s > 0) scored.push({ d: docs[i], s: s });
    }
    scored.sort(function (a, b) { return b.s - a.s; });
    for (var j = 0; j < Math.min(scored.length, 10); j++) {
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.setAttribute('href', scored[j].d.url);
      a.textContent = scored[j].d.title;
      li.appendChild(a);
      results.appendChild(li);
    }
  }

  input.addEventListener('input', function () {
    var q = input.value;
    if (q.length < 2) { results.textContent = ''; return; }
    load(function () { render(q); });
  });
})();
`
