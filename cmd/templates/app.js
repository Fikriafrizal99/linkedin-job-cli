(function () {
  'use strict';
  document.documentElement.classList.add('js-enabled');
  var all = function (selector, root) { return Array.from((root || document).querySelectorAll(selector)); };
  var byId = function (id) { return document.getElementById(id); };
  var tabs = all('.js-settings-tab');
  function showSettings(tab) {
    if (!tabs.some(function (a) { return a.dataset.target === 'settings-' + tab; })) tab = 'general';
    tabs.forEach(function (a) { var active = a.dataset.target === 'settings-' + tab; a.classList.toggle('active', active); if (active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current'); });
    all('.settings-pane').forEach(function (p) { p.classList.toggle('active', p.id === 'settings-' + tab); });
  }
  tabs.forEach(function (a) { a.addEventListener('click', function (e) { e.preventDefault(); var tab = a.dataset.target.replace('settings-', ''); history.pushState(null, '', '?tab=' + tab); showSettings(tab); }); });
  showSettings(new URLSearchParams(location.search).get('tab'));
  window.addEventListener('popstate', function () { showSettings(new URLSearchParams(location.search).get('tab')); });
  var menu = document.querySelector('.mobile-menu');
  document.addEventListener('click', function (e) { if (menu && !menu.contains(e.target)) menu.open = false; });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && menu && menu.open) { menu.open = false; menu.querySelector('summary').focus(); } });

  // Native GET filters keep the URL as the source of truth.
  var k = byId('collect-keywords'), l = byId('collect-locations'), p = byId('collect-posted'), t = byId('collect-top'), out = byId('collect-preview'), planStatus = byId('collect-plan'), collectSubmit = byId('collect-submit');
  function quote(v) { return '"' + String(v || '').replace(/"/g, '\\"') + '"'; }
  function lines(el) {
    if (!el) return [];
    var seen = new Set();
    return el.value.split(/\r?\n/).map(function (v) { return v.trim(); }).filter(function (v) {
      var key = v.toLowerCase();
      if (!v || seen.has(key)) return false;
      seen.add(key); return true;
    });
  }
  function preview() {
    if (!out || !k) return;
    var queries = lines(k), locations = lines(l), effectiveLocations = Math.max(1, locations.length), combinations = queries.length * effectiveLocations;
    var command = ['linkedin-jobs', 'collect'];
    queries.forEach(function (q) { command.push('--query', quote(q)); });
    locations.forEach(function (loc) { command.push('--location', quote(loc)); });
    command.push('--posted-within', p.value, '--top', t.value);
    out.textContent = command.join(' ');
    if (planStatus) {
      planStatus.textContent = queries.length + ' quer' + (queries.length === 1 ? 'y' : 'ies') + ' × ' + (locations.length || 1) + ' location' + (effectiveLocations === 1 ? '' : 's') + ' = ' + combinations + ' search combination' + (combinations === 1 ? '' : 's') + '.';
      if (!locations.length) planStatus.textContent += ' Location is unrestricted.';
      if (combinations > 50) planStatus.textContent += ' Reduce the list to 50 combinations or fewer.';
    }
    if (collectSubmit) {
      collectSubmit.disabled = queries.length === 0 || queries.length > 20 || locations.length > 10 || combinations > 50;
      collectSubmit.title = combinations > 50 ? 'Maximum 50 query/location combinations' : '';
    }
  }
  [k, l, p, t].forEach(function (el) { if (el) el.addEventListener('input', preview); }); preview();

  function selection(checks, selectAll, count, update) {
    function sync() {
      var selected = checks.filter(function (x) { return x.checked; });
      if (count) count.textContent = selected.length + ' selected';
      if (selectAll) { selectAll.checked = selected.length > 0 && selected.length === checks.length; selectAll.indeterminate = selected.length > 0 && selected.length < checks.length; selectAll.disabled = checks.length === 0; }
      checks.forEach(function (x) { x.closest('tr').classList.toggle('row-selected', x.checked); });
      update(selected);
    }
    if (selectAll) selectAll.addEventListener('change', function () { checks.forEach(function (x) { x.checked = selectAll.checked; }); sync(); });
    checks.forEach(function (x) { x.addEventListener('change', sync); }); sync();
    return sync;
  }
  var jobs = all('.js-job-check');
  var syncJobs = selection(jobs, byId('select-all-jobs'), byId('selected-jobs-count'), function (selected) {
    var n = selected.length, queue = byId('queue-selected-jobs'), process = byId('process-selected-jobs');
    if (!queue) return;
    queue.disabled = n === 0 || n > 50; process.disabled = n === 0 || n > 25;
    queue.title = n > 50 ? 'Select at most 50 jobs' : 'Save supported jobs for later preparation';
    process.title = n > 25 ? 'Select at most 25 jobs' : 'Create email drafts and queue manual Easy Apply jobs';
    var email = selected.filter(function (x) { return x.dataset.method === 'EMAIL'; }).length;
    var easy = selected.filter(function (x) { return x.dataset.method === 'EASY_APPLY'; }).length;
    byId('jobs-selection-detail').textContent = n ? email + ' email · ' + easy + ' Easy Apply · ' + (n - email - easy) + ' unsupported. ' + (n > 25 ? 'Reduce selection to 25 to process. ' : '') + 'Existing protected applications are skipped.' : 'Select jobs to preview their application methods.';
  });
  var appForm = byId('bulk-app-form');
  var syncApps = selection(all('.js-app-check'), byId('select-all-apps'), byId('selected-count'), function (selected) {
    if (!appForm) return;
    var n = selected.length, gmail = appForm.dataset.gmail === 'true';
    var count = function (states, prepared) { return selected.filter(function (x) { return states.includes(x.dataset.state) && (!prepared || x.dataset.prepared === 'true'); }).length; };
    var eligible = {prepare: count(['READY_EMAIL']), draft: count(['READY_EMAIL'], true), review: count(['DRAFT_CREATED']), remove: count(['READY_EMAIL', 'NEED_REVIEW', 'READY_EASY_APPLY', 'IN_PROGRESS']), 'send-confirm': count(['APPROVED'])};
    var preferredAction = eligible.prepare > eligible.draft ? 'prepare' : eligible.draft && gmail ? 'draft' : eligible.review ? 'review' : eligible['send-confirm'] ? 'send-confirm' : 'prepare';
    all('button[formaction]', appForm).forEach(function (button) {
      var action = button.getAttribute('formaction').split('/').pop();
      var limit = action === 'draft' || action === 'send-confirm' ? 25 : 50;
      var total = eligible[action] || 0;
      button.hidden = n > 0 && total === 0;
      button.disabled = n === 0 || total === 0 || n > limit || (action === 'draft' && !gmail);
      button.title = n > limit ? 'Select at most ' + limit + ' applications' : action === 'draft' && !gmail ? 'Connect Gmail in Settings to create drafts' : total + ' eligible; other selected records will be skipped';
      button.classList.remove('primary');
      if (!button.disabled && action === preferredAction) button.classList.add('primary');
    });
    var counts = n ? eligible.prepare + ' can prepare · ' + eligible.draft + ' can create drafts · ' + eligible.review + ' can review · ' + eligible['send-confirm'] + ' approved.' : 'Select applications to see eligible actions.';
    byId('app-selection-detail').textContent = counts + (n > 25 ? ' Draft/send limit: 25 selected. Other actions: 50.' : '') + (eligible.draft && !gmail ? ' Connect Gmail in Settings to create drafts.' : '') + (n && !eligible.prepare && !eligible.review && !eligible['send-confirm'] ? ' Use the Easy Apply queue for manual applications, or inspect jobs needing attention.' : ' Ineligible selected records are skipped.');
    var attachments = appForm.querySelector('.bulk-attachments'); if (attachments) attachments.hidden = n > 0 && !eligible.draft;
  });

  // Gate visual availability on explicit confirmation, without replacing server guards.
  function syncConfirmation(form) {
    var checks = all('input[type=checkbox][required]', form);
    if (!checks.length) return;
    var ready = checks.every(function (x) { return x.checked; }) && form.dataset.opened !== 'false';
    all('button[type=submit]', form).forEach(function (b) { b.disabled = !ready; b.title = ready ? '' : 'Read and check the confirmation above to continue'; });
  }
  all('form').forEach(function (form) {
    all('input[type=checkbox][required]', form).forEach(function (c) { c.addEventListener('change', function () { syncConfirmation(form); }); });
    syncConfirmation(form);
  });

  // Reserve tabs during the explicit click, then update local progress only for opened tabs.
  async function openManual(url, endpoint) {
    var tab = window.open('about:blank', '_blank');
    if (!tab) throw new Error('Your browser blocked the tab. Allow popups for this local app and try again.');
    tab.opener = null;
    try {
      var csrf = document.querySelector('meta[name="csrf-token"]').content;
      var response = await fetch(endpoint, {method: 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/x-www-form-urlencoded', 'X-CSRF-Token': csrf}, body: 'csrf=' + encodeURIComponent(csrf) + '&no_redirect=1'});
      if (!response.ok) throw new Error('Could not record progress. Refresh this queue and try again.');
      tab.location.href = url;
    } catch (error) { tab.close(); throw error; }
  }
  var openForm = byId('easy-open-form'), openStatus = byId('easy-open-status'), confirmForm = byId('easy-confirm-form');
  if (openForm) openForm.addEventListener('submit', async function (e) {
    e.preventDefault(); var button = openForm.querySelector('button'); if (button.disabled) return;
    button.disabled = true; openStatus.textContent = 'Opening LinkedIn…';
    try {
      await openManual(openForm.dataset.url, openForm.action);
      var badge = byId('easy-state'); badge.textContent = 'In progress'; badge.title = 'IN_PROGRESS'; badge.className = 'badge state-in_progress';
      byId('easy-opened').textContent = 'Opened just now';
      confirmForm.dataset.opened = 'true';
      var submitted = confirmForm.querySelector('input[name=apply_confirm]'); submitted.disabled = false; submitted.checked = false;
      syncConfirmation(confirmForm);
      byId('easy-confirm-help').textContent = 'Confirm only when LinkedIn shows your submission is complete.';
      button.classList.remove('primary'); button.classList.add('ghost');
      openStatus.textContent = 'LinkedIn opened. Submit manually, then return here to confirm. Opening the page does not count as applying.';
    } catch (error) { openStatus.textContent = error.message; }
    finally { button.disabled = false; }
  });
  var nextThree = byId('easy-open-next3');
  if (nextThree) nextThree.addEventListener('click', async function () {
    nextThree.disabled = true;
    var targets = all('.easy-open-target').slice(0, 3);
    // Invoke all opens synchronously so browser popup policy remains authoritative.
    var results = await Promise.allSettled(targets.map(function (target) { return openManual(target.dataset.url, target.dataset.mark); }));
    var opened = results.filter(function (r) { return r.status === 'fulfilled'; }).length;
    openStatus.textContent = opened + ' LinkedIn tabs opened. Submit each application manually.';
    var failed = results.find(function (r) { return r.status === 'rejected'; });
    if (failed) openStatus.textContent += ' ' + failed.reason.message;
    nextThree.disabled = false;
  });

  // Keep native form submission and its original submitter (including formaction).
  // aria-disabled plus a form lock prevents duplicates without dropping posted values.
  all('form[method=post]').forEach(function (form) {
    if (form.target === '_blank') return;
    form.addEventListener('submit', function (e) {
      if (e.defaultPrevented) return;
      if (form.dataset.busy === 'true') { e.preventDefault(); return; }
      form.dataset.busy = 'true'; form.setAttribute('aria-busy', 'true');
      var button = e.submitter;
      if (button) { button.dataset.originalText = button.textContent; button.textContent = form.id === 'collect-form' ? 'Collecting…' : 'Working…'; button.setAttribute('aria-disabled', 'true'); }
      var status = document.createElement('div'); status.className = 'action-status'; status.setAttribute('role', 'status'); status.textContent = 'Processing your request. Please wait.'; form.appendChild(status);
      if (form.id === 'collect-form') byId('collect-status').textContent = 'Collecting public listings and reading job details. This can take a few minutes. Keep this page open; results will appear when finished.';
    });
  });
  window.addEventListener('pageshow', function () {
    all('form[data-busy]').forEach(function (form) { delete form.dataset.busy; form.removeAttribute('aria-busy'); all('button[data-original-text]', form).forEach(function (b) { b.textContent = b.dataset.originalText; b.removeAttribute('aria-disabled'); }); all('.action-status', form).forEach(function (s) { s.remove(); }); });
    syncJobs(); syncApps(); all('form').forEach(syncConfirmation);
  });
  var next = byId('review-next') || byId('easy-next'), prev = byId('review-prev') || byId('easy-prev');
  document.addEventListener('keydown', function (e) {
    if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.repeat) return;
    if (e.target.closest('input,textarea,select,button,a,summary,[contenteditable="true"]') || document.querySelector('form[data-busy="true"]')) return;
    var target = ['j', 'J', 'ArrowRight'].includes(e.key) ? next : ['k', 'K', 'ArrowLeft'].includes(e.key) ? prev : null;
    if (target) { e.preventDefault(); location.href = target.href; }
  });
})();
