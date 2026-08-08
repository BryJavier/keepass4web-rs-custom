(function () {
  function onReady(fn) {
    if (document.readyState !== 'loading') fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  function getCsrfToken() {
    var el = document.querySelector('[data-csrf-token]');
    return el ? el.dataset.csrfToken : '';
  }

  function filterEntries(q) {
    q = (q || '').toLowerCase();
    var visible = 0;
    document.querySelectorAll('#entry-list > details').forEach(function (d) {
      var match = q.length === 0 || (d.dataset.search || '').indexOf(q) !== -1;
      d.hidden = !match;
      if (match) visible++;
    });
    var empty = document.getElementById('no-results');
    if (empty) empty.hidden = visible !== 0;
  }

  var clipboardClearTimer = null;
  function copyWithAutoClear(value) {
    navigator.clipboard.writeText(value);
    if (clipboardClearTimer) clearTimeout(clipboardClearTimer);
    clipboardClearTimer = setTimeout(function () {
      navigator.clipboard.readText().then(function (current) {
        if (current === value) navigator.clipboard.writeText('');
      }).catch(function () {});
    }, 10000);
  }

  function startIdlePolling() {
    var banner = document.getElementById('idle-banner');
    if (!banner) return;
    var threshold = 15;
    async function poll() {
      try {
        var res = await fetch('/session/status', { credentials: 'same-origin' });
        if (!res.ok) return;
        var data = await res.json();
        if (data.active && data.vault_active && data.idle_seconds_remaining <= threshold) {
          banner.hidden = false;
          var el = document.getElementById('idle-seconds');
          if (el) el.textContent = data.idle_seconds_remaining;
        } else {
          banner.hidden = true;
        }
      } catch (err) {}
    }
    poll();
    setInterval(poll, 5000);
    var lastBeat = 0;
    function heartbeat() {
      var now = Date.now();
      if (now - lastBeat < 4000) return;
      lastBeat = now;
      fetch('/session/heartbeat', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'csrf_token=' + encodeURIComponent(getCsrfToken()),
      }).catch(function () {});
    }
    ['mousemove', 'keydown', 'click'].forEach(function (evt) {
      document.addEventListener(evt, heartbeat, { passive: true });
    });
  }

  onReady(function () {
    startIdlePolling();
    var search = document.getElementById('entry-search');
    if (search) search.addEventListener('input', function () { filterEntries(search.value); });

    var signinForm = document.getElementById('signin-form');
    if (signinForm) {
      var SUPABASE_URL = document.body.dataset.supabaseUrl;
      var SUPABASE_ANON_KEY = document.body.dataset.supabaseAnonKey;
      signinForm.addEventListener('submit', async function (e) {
        e.preventDefault();
        var errorBox = document.getElementById('signin-error');
        if (errorBox) errorBox.hidden = true;
        var email = document.getElementById('email').value;
        var password = document.getElementById('password').value;
        try {
          var res = await fetch(SUPABASE_URL + '/auth/v1/token?grant_type=password', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', apikey: SUPABASE_ANON_KEY },
            body: JSON.stringify({ email: email, password: password }),
          });
          if (!res.ok) throw new Error('bad credentials');
          var data = await res.json();
          document.getElementById('access_token').value = data.access_token;
          signinForm.submit();
        } catch (err) {
          if (errorBox) errorBox.hidden = false;
        }
      });
    }
  });

  document.addEventListener('click', function (e) {
    var reveal = e.target.closest('[data-action="toggle-reveal"]');
    if (reveal) {
      var wrap = reveal.closest('.relative');
      var input = wrap ? wrap.querySelector('input') : null;
      if (input) input.type = input.type === 'password' ? 'text' : 'password';
      return;
    }
    var copy = e.target.closest('[data-action="copy"]');
    if (copy) {
      var wrap2 = copy.closest('.relative');
      var input2 = wrap2 ? wrap2.querySelector('input') : null;
      if (input2) {
        copyWithAutoClear(input2.value);
        copy.classList.add('text-emerald-600');
        setTimeout(function () { copy.classList.remove('text-emerald-600'); }, 900);
      }
      return;
    }
    var copyId = e.target.closest('[data-action="copy-by-id"]');
    if (copyId) {
      e.preventDefault();
      var input3 = document.getElementById(copyId.dataset.targetId);
      if (input3) {
        copyWithAutoClear(input3.value);
        copyId.classList.add('text-emerald-600');
        setTimeout(function () { copyId.classList.remove('text-emerald-600'); }, 900);
      }
      return;
    }
    var openModal = e.target.closest('[data-action="show-modal"]');
    if (openModal) {
      var dlg = document.getElementById(openModal.dataset.target);
      if (dlg && dlg.showModal) dlg.showModal();
      return;
    }
    var closeModal = e.target.closest('[data-action="close-modal"]');
    if (closeModal) {
      var dlg2 = closeModal.closest('dialog');
      if (dlg2) dlg2.close();
      return;
    }
    var dismiss = e.target.closest('[data-action="dismiss-idle"]');
    if (dismiss) {
      var b = document.getElementById('idle-banner');
      if (b) b.hidden = true;
    }
  });

  document.addEventListener('change', function (e) {
    if (e.target.classList && e.target.classList.contains('js-file-input')) {
      var wrap = e.target.closest('label');
      var label = wrap ? wrap.querySelector('.js-file-label') : null;
      if (label) label.textContent = e.target.files[0] ? e.target.files[0].name : label.dataset.placeholder;
    }
  });
})();
