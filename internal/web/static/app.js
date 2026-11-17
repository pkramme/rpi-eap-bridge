// EAP Bridge – small progressive enhancements. Every page also works
// without JavaScript.
(function () {
  'use strict';
  document.documentElement.classList.add('js');

  // Ask before dangerous actions (data-confirm on a form or button).
  document.addEventListener('click', function (ev) {
    var btn = ev.target.closest ? ev.target.closest('button[data-confirm]') : null;
    if (btn && !window.confirm(btn.getAttribute('data-confirm'))) {
      ev.preventDefault();
    }
  });
  document.addEventListener('submit', function (ev) {
    var form = ev.target;
    var msg = form.getAttribute('data-confirm');
    if (msg && !window.confirm(msg)) {
      ev.preventDefault();
      return;
    }
    // Show that something happens while the server works.
    var btn = ev.submitter || form.querySelector('button[type=submit]');
    if (btn && btn.hasAttribute('data-busy')) {
      var label = btn.getAttribute('data-busy');
      window.setTimeout(function () {
        btn.disabled = true;
        btn.classList.add('working');
        btn.textContent = label;
      }, 0);
    }
  });

  // Live status: reload the status cards every few seconds.
  var live = document.getElementById('live-status');
  if (live) {
    var url = live.getAttribute('data-refresh');
    var last = null;
    var busy = false;
    var refresh = function () {
      if (busy || document.hidden) return;
      var active = document.activeElement;
      if (active && active !== document.body && live.contains(active) && active.tagName !== 'A') return;
      busy = true;
      fetch(url, { cache: 'no-store', credentials: 'same-origin' })
        .then(function (r) {
          if (r.status === 401) { window.location.reload(); return null; }
          return r.ok ? r.text() : null;
        })
        .then(function (html) {
          if (html === null || html === last) return;
          var open = [];
          live.querySelectorAll('details[open][id]').forEach(function (d) { open.push(d.id); });
          live.innerHTML = html;
          last = html;
          open.forEach(function (id) {
            var d = document.getElementById(id);
            if (d) d.open = true;
          });
        })
        .catch(function () { /* Pi restarting or connection lost: try again later */ })
        .then(function () { busy = false; });
    };
    window.setInterval(refresh, 3000);
    document.addEventListener('visibilitychange', refresh);
  }

  // WiFi form: show only the fields that matter for the current choices.
  var form = document.getElementById('wifi-form');
  if (form) {
    var eap = form.querySelector('#eap');
    var sync = function () {
      form.querySelectorAll('[data-eap]').forEach(function (el) {
        el.hidden = el.getAttribute('data-eap') !== eap.value;
      });
      var ca = form.querySelector('input[name=ca_mode]:checked');
      var mode = ca ? ca.value : 'system';
      form.querySelectorAll('[data-ca]').forEach(function (el) {
        el.hidden = el.getAttribute('data-ca').split(' ').indexOf(mode) < 0;
      });
      var other = form.querySelector('input[name=ssid][value="__other__"]');
      var box = form.querySelector('.other-ssid');
      if (other && box) box.hidden = !other.checked;
    };
    form.addEventListener('change', sync);
    sync();

    var show = document.getElementById('show-password');
    var pw = document.getElementById('password');
    if (show && pw) {
      show.addEventListener('change', function () { pw.type = show.checked ? 'text' : 'password'; });
    }

    // Show which domain will be used when the field is left empty.
    var id = document.getElementById('identity');
    var dom = document.getElementById('domain');
    if (id && dom) {
      var hint = function () {
        var v = id.value.trim();
        var at = v.lastIndexOf('@');
        dom.placeholder = (at >= 0 && at < v.length - 1)
          ? v.slice(at + 1).toLowerCase() + '  (taken from your username)'
          : 'for example university.edu';
      };
      id.addEventListener('input', hint);
      hint();
    }
  }
})();
