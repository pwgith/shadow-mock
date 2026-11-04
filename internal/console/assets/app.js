// The Shadow Mock console. It is a client of the admin API and holds no behaviour of its own: every call it makes is
// listed in calls.json, and everything it shows comes from an answer of the API. It builds the page with textContent
// only, never from markup in an answer, so a request that a test sent cannot run script here.
"use strict";

(function () {
  const adminBase = document.querySelector('meta[name="admin-base"]').content;
  const tokenInput = document.getElementById("token");
  const statusEl = document.getElementById("status");
  let calls = {};

  try { tokenInput.value = sessionStorage.getItem("shadowmock-token") || ""; } catch (e) { /* no storage: the token is kept in the field only */ }
  tokenInput.addEventListener("change", function () {
    try { sessionStorage.setItem("shadowmock-token", tokenInput.value); } catch (e) { /* see above */ }
  });

  function say(text, isError) {
    statusEl.textContent = text;
    statusEl.className = isError ? "error" : "";
  }

  // call makes one admin call from calls.json. "{id}" in the path is replaced by params.id and "{stub}" by params.stub.
  async function call(name, params, body) {
    const spec = calls[name];
    if (!spec) { throw new Error("The console has no call named " + name); }
    const method = spec[0];
    const path = spec[1]
      .replace("{id}", encodeURIComponent((params && params.id) || ""))
      .replace("{stub}", encodeURIComponent((params && params.stub) || ""));
    const headers = {};
    if (tokenInput.value) { headers["Authorization"] = "Bearer " + tokenInput.value; }
    if (body !== undefined) { headers["Content-Type"] = "application/json"; }
    const res = await fetch(adminBase + path, { method: method, headers: headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await res.text();
    let doc = null;
    try { doc = text ? JSON.parse(text) : null; } catch (e) { doc = null; }
    if (!res.ok) {
      const reason = doc && (doc.message || doc.error) ? (doc.error || "") + " " + (doc.message || "") : res.statusText;
      throw new Error(res.status + " " + reason.trim());
    }
    return doc;
  }

  function el(tag, text, className) {
    const e = document.createElement(tag);
    if (text !== undefined) { e.textContent = text; }
    if (className) { e.className = className; }
    return e;
  }

  function pre(value) {
    return el("pre", typeof value === "string" ? value : JSON.stringify(value, null, 2));
  }

  function clear(node) { while (node.firstChild) { node.removeChild(node.firstChild); } }

  async function guarded(fn) {
    try { say("", false); await fn(); } catch (e) { say(e.message, true); }
  }

  // pick chooses between two calls: the first when flag is empty or false, the second otherwise. The scope pickers use
  // it, so that an empty scope is the global session and anything else is a session id. Both names are written out
  // here so that they can be read, and checked against calls.json.
  function pick(flag, firstName, secondName) { return flag ? secondName : firstName; }

  // Tabs.
  const tabs = ["journal", "sessions", "counters", "recordings", "dryrun"];
  document.querySelectorAll("nav button").forEach(function (b) {
    b.addEventListener("click", function () {
      tabs.forEach(function (t) {
        document.getElementById("tab-" + t).hidden = t !== b.dataset.tab;
      });
      document.querySelectorAll("nav button").forEach(function (x) { x.setAttribute("aria-pressed", String(x === b)); });
      if (b.dataset.tab === "journal") { guarded(loadJournal); }
      if (b.dataset.tab === "sessions") { guarded(loadSessions); }
      if (b.dataset.tab === "counters") { guarded(function () { return loadScopes(countersScope).then(loadCounters); }); }
      if (b.dataset.tab === "recordings") { guarded(function () { return loadScopes(recordingsScope).then(loadRecording); }); }
    });
  });

  // Journal.
  const journalList = document.getElementById("journal-list");
  const journalDetail = document.getElementById("journal-detail");

  async function loadJournal() {
    const doc = await call(pick(document.getElementById("unmatched-only").checked, "journal", "unmatched"));
    clear(journalList);
    (doc.requests || []).forEach(function (entry) {
      const li = el("li");
      const matched = entry.wasMatched;
      li.appendChild(el("span", matched ? "matched" : "unmatched", matched ? "hit" : "miss"));
      li.appendChild(el("span", entry.request.method + " " + entry.request.url));
      li.appendChild(el("span", String(entry.response.status), "hint"));
      li.setAttribute("tabindex", "0");
      li.addEventListener("click", function () { selectEntry(li, entry); });
      li.addEventListener("keydown", function (ev) { if (ev.key === "Enter") { selectEntry(li, entry); } });
      journalList.appendChild(li);
    });
    if (!journalList.firstChild) { journalList.appendChild(el("li", "No requests.", "hint")); }
  }

  async function selectEntry(li, entry) {
    journalList.querySelectorAll("li").forEach(function (x) { x.removeAttribute("aria-selected"); });
    li.setAttribute("aria-selected", "true");
    clear(journalDetail);
    journalDetail.appendChild(el("h2", "Request"));
    journalDetail.appendChild(pre(entry.request));
    journalDetail.appendChild(el("h2", "Response"));
    journalDetail.appendChild(pre(entry.response));
    if (entry.wasMatched) {
      journalDetail.appendChild(el("h2", "Served by"));
      journalDetail.appendChild(el("p", (entry.shadowMock && entry.shadowMock.layer ? entry.shadowMock.layer + " layer" : "") + (entry.stubMapping && entry.stubMapping.name ? ", mapping " + entry.stubMapping.name : "")));
      return;
    }
    journalDetail.appendChild(el("h2", "Closest mappings"));
    await guarded(async function () {
      const sample = { method: entry.request.method, url: entry.request.url, body: entry.request.body || undefined };
      const headers = {};
      Object.keys(entry.request.headers || {}).forEach(function (k) {
        const v = entry.request.headers[k];
        headers[k] = Array.isArray(v) ? v.join(", ") : String(v);
      });
      sample.headers = headers;
      renderNearMisses(journalDetail, (await call("nearMisses", {}, sample)).nearMisses || []);
    });
  }

  function renderNearMisses(into, misses) {
    if (!misses.length) { into.appendChild(el("p", "There are no mappings to compare with.", "hint")); return; }
    misses.slice(0, 5).forEach(function (nm) {
      const name = nm.stubMapping && (nm.stubMapping.name || nm.stubMapping.id) || "a mapping";
      into.appendChild(el("h3", name + (nm.layer ? " (" + nm.layer + ")" : "") + ", distance " + nm.matchResult.distance));
      into.appendChild(el("p", nm.missed && nm.missed.length ? "Missed on: " + nm.missed.join(", ") : "Nothing missed.", nm.missed && nm.missed.length ? "danger" : "hint"));
      if (nm.stubMapping) { into.appendChild(pre(nm.stubMapping.request)); }
    });
  }

  document.getElementById("refresh-journal").addEventListener("click", function () { guarded(loadJournal); });
  document.getElementById("unmatched-only").addEventListener("change", function () { guarded(loadJournal); });

  // Shadow mocks.
  const sessionsBody = document.getElementById("sessions-body");
  const sessionEdit = document.getElementById("session-edit");
  let editing = "";

  async function loadSessions() {
    const doc = await call("sessions");
    clear(sessionsBody);
    const list = doc.sessions || [];
    document.getElementById("sessions-empty").hidden = list.length > 0;
    if (!list.some(function (s) { return s.id === editing; })) { closeEdit(); }
    list.forEach(function (s) {
      const tr = el("tr");
      [s.name || s.id, s.kind + (s.inheritGlobal === false ? " (no global fallback)" : ""), s.createdBy, s.expiresAt, String(s.requestCount)].forEach(function (v) { tr.appendChild(el("td", v)); });
      const td = el("td");
      const edit = el("button", "Edit");
      edit.type = "button";
      edit.addEventListener("click", function () { showSession(s); });
      const del = el("button", "Delete");
      del.type = "button";
      del.addEventListener("click", function () {
        if (!window.confirm("Delete " + (s.name || s.id) + "?")) { return; }
        guarded(async function () { await call("deleteSession", { id: s.id }); await loadSessions(); });
      });
      td.appendChild(edit);
      td.appendChild(del);
      tr.appendChild(td);
      sessionsBody.appendChild(tr);
    });
  }
  document.getElementById("refresh-sessions").addEventListener("click", function () { guarded(loadSessions); });

  function closeEdit() { editing = ""; sessionEdit.hidden = true; clear(sessionEdit); }

  // patchSession changes one session and shows it as the admin API now has it.
  async function patchSession(s, body) {
    const updated = await call("updateSession", { id: s.id }, body);
    await loadSessions();
    showSession(updated);
  }

  function field(label, input) {
    const l = el("label", label + " ");
    l.appendChild(input);
    return l;
  }

  function input(type, value, size) {
    const i = el("input");
    i.type = type;
    if (value !== undefined) { i.value = value; }
    if (size) { i.size = size; }
    return i;
  }

  function button(text, onClick, type) {
    const b = el("button", text);
    b.type = type || "button";
    if (onClick) { b.addEventListener("click", onClick); }
    return b;
  }

  // showSession shows the panel that edits a session: name, lifetime, match tokens and masks.
  function showSession(s) {
    editing = s.id;
    clear(sessionEdit);
    sessionEdit.hidden = false;
    sessionEdit.appendChild(el("h2", "Edit " + (s.name || s.id)));
    sessionEdit.appendChild(el("p", s.kind + ", expires " + s.expiresAt + (s.baseUrl ? ", base URL " + s.baseUrl : ""), "hint"));

    const form = el("form", undefined, "edit-form");
    const nameIn = input("text", s.name || "", 30);
    nameIn.maxLength = 200;
    const ttlIn = input("number");
    ttlIn.min = "1";
    ttlIn.placeholder = "unchanged";
    form.appendChild(field("Name", nameIn));
    form.appendChild(field("Lifetime from now, in seconds", ttlIn));
    form.appendChild(button("Save", undefined, "submit"));
    form.appendChild(button("Keep alive", function () {
      guarded(async function () { const k = await call("keepAlive", { id: s.id }); await loadSessions(); showSession(k); });
    }));
    form.addEventListener("submit", function (ev) {
      ev.preventDefault();
      const body = {};
      if (nameIn.value !== (s.name || "")) { body.name = nameIn.value; }
      if (ttlIn.value) { body.ttlSeconds = Number(ttlIn.value); }
      if (!Object.keys(body).length) { say("Nothing to save.", false); return; }
      guarded(function () { return patchSession(s, body); });
    });
    sessionEdit.appendChild(form);

    if (s.kind === "key-based") {
      sessionEdit.appendChild(el("h3", "Match tokens"));
      const tokens = el("ul", undefined, "chips");
      (s.tokens || []).forEach(function (t) {
        const li = el("li", t);
        li.appendChild(button("Remove", function () {
          guarded(function () { return patchSession(s, { removeTokens: [t] }); });
        }));
        tokens.appendChild(li);
      });
      if (!tokens.firstChild) { tokens.appendChild(el("li", "None", "hint")); }
      sessionEdit.appendChild(tokens);
      const addToken = el("form", undefined, "edit-form");
      const tokenIn = input("text", undefined, 24);
      tokenIn.minLength = 6;
      tokenIn.required = true;
      addToken.appendChild(field("New token", tokenIn));
      addToken.appendChild(button("Add", undefined, "submit"));
      addToken.addEventListener("submit", function (ev) {
        ev.preventDefault();
        guarded(function () { return patchSession(s, { addTokens: [tokenIn.value] }); });
      });
      sessionEdit.appendChild(addToken);
    }

    sessionEdit.appendChild(el("h3", "Masks"));
    sessionEdit.appendChild(el("p", "A mask hides a mapping of a lower layer from this shadow mock.", "hint"));
    const masks = el("ul", undefined, "chips");
    (s.masks || []).forEach(function (m) {
      const li = el("li", m.id ? "mapping " + m.id : JSON.stringify(m.request));
      li.appendChild(button("Remove", function () {
        guarded(function () { return patchSession(s, { removeMasks: [m] }); });
      }));
      masks.appendChild(li);
    });
    if (!masks.firstChild) { masks.appendChild(el("li", "None", "hint")); }
    sessionEdit.appendChild(masks);
    const addMask = el("form", undefined, "edit-form");
    const maskIn = input("text", undefined, 40);
    maskIn.required = true;
    addMask.appendChild(field("Hide the mapping with id", maskIn));
    addMask.appendChild(button("Add", undefined, "submit"));
    addMask.addEventListener("submit", function (ev) {
      ev.preventDefault();
      guarded(function () { return patchSession(s, { addMasks: [{ id: maskIn.value }] }); });
    });
    sessionEdit.appendChild(addMask);
    sessionEdit.appendChild(button("Close", closeEdit));
  }

  // loadScopes fills a scope picker: the global session, then each shadow mock.
  async function loadScopes(select) {
    const keep = select.value;
    const doc = await call("sessions");
    clear(select);
    const g = el("option", "Global session");
    g.value = "";
    select.appendChild(g);
    (doc.sessions || []).forEach(function (s) {
      const o = el("option", (s.name || s.id) + " (" + s.kind + ")");
      o.value = s.id;
      select.appendChild(o);
    });
    select.value = keep;
    if (select.selectedIndex < 0) { select.selectedIndex = 0; }
  }

  // Counters.
  const countersScope = document.getElementById("counters-scope");
  const countersBody = document.getElementById("counters-body");

  async function loadCounters() {
    const scope = countersScope.value;
    const doc = await call(pick(scope, "counters", "sessionCounters"), { id: scope });
    clear(countersBody);
    const list = doc.counters || [];
    document.getElementById("counters-empty").hidden = list.length > 0;
    list.forEach(function (c) {
      const params = { id: scope, stub: c.stubMappingId };
      const tr = el("tr");
      [c.stubMappingId, c.stubLayer || "", String(c.count), c.lastCalledAt || ""].forEach(function (v) { tr.appendChild(el("td", v)); });
      const setTd = el("td");
      const countIn = input("number", String(c.count));
      countIn.min = "0";
      countIn.setAttribute("aria-label", "New count for " + c.stubMappingId);
      setTd.appendChild(countIn);
      setTd.appendChild(button("Set", function () {
        guarded(async function () {
          const body = { count: Number(countIn.value) };
          if (c.stubLayer) { body.stubLayer = c.stubLayer; }
          await call(pick(scope, "setCounter", "setSessionCounter"), params, body);
          await loadCounters();
        });
      }));
      tr.appendChild(setTd);
      const td = el("td");
      td.appendChild(button("Reset", function () {
        guarded(async function () { await call(pick(scope, "resetCounter", "resetSessionCounter"), params); await loadCounters(); });
      }));
      tr.appendChild(td);
      countersBody.appendChild(tr);
    });
  }
  countersScope.addEventListener("change", function () { guarded(loadCounters); });
  document.getElementById("refresh-counters").addEventListener("click", function () {
    guarded(function () { return loadScopes(countersScope).then(loadCounters); });
  });
  document.getElementById("reset-counters").addEventListener("click", function () {
    if (!window.confirm("Reset every call counter in this scope to zero?")) { return; }
    const scope = countersScope.value;
    guarded(async function () { await call(pick(scope, "resetCounters", "resetSessionCounters"), { id: scope }); await loadCounters(); });
  });

  // Recordings.
  const recordingsScope = document.getElementById("recordings-scope");
  const recordingStatus = document.getElementById("recording-status");
  const recordingResult = document.getElementById("recording-result");

  async function loadRecording() {
    const scope = recordingsScope.value;
    const doc = await call(pick(scope, "recordingStatus", "sessionRecordingStatus"), { id: scope });
    recordingStatus.textContent = doc.status;
  }
  recordingsScope.addEventListener("change", function () { recordingResult.hidden = true; guarded(loadRecording); });
  document.getElementById("refresh-recordings").addEventListener("click", function () {
    guarded(function () { return loadScopes(recordingsScope).then(loadRecording); });
  });
  document.getElementById("recording-form").addEventListener("submit", function (ev) {
    ev.preventDefault();
    const scope = recordingsScope.value;
    guarded(async function () {
      recordingResult.hidden = true;
      await call(pick(scope, "startRecording", "startSessionRecording"), { id: scope }, { targetBaseUrl: document.getElementById("rec-target").value });
      await loadRecording();
    });
  });
  document.getElementById("stop-recording").addEventListener("click", function () {
    const scope = recordingsScope.value;
    guarded(async function () {
      const res = await call(pick(scope, "stopRecording", "stopSessionRecording"), { id: scope });
      const mappings = (res && res.mappings) || [];
      clear(recordingResult);
      recordingResult.hidden = false;
      recordingResult.appendChild(el("h2", mappings.length === 1 ? "Recorded 1 mapping" : "Recorded " + mappings.length + " mappings"));
      mappings.forEach(function (m) {
        recordingResult.appendChild(el("h3", (m.request && m.request.method ? m.request.method + " " : "") + ((m.request && (m.request.url || m.request.urlPath || m.request.urlPattern)) || "")));
        recordingResult.appendChild(pre(m));
      });
      await loadRecording();
    });
  });

  // Dry run.
  document.getElementById("dryrun-form").addEventListener("submit", function (ev) {
    ev.preventDefault();
    guarded(async function () {
      const body = { request: { method: document.getElementById("dr-method").value, url: document.getElementById("dr-url").value } };
      const sid = document.getElementById("dr-session").value;
      if (sid) { body.sessionId = sid; }
      const out = document.getElementById("dryrun-result");
      clear(out);
      const res = await call("dryRun", {}, body);
      out.appendChild(el("h2", res.matched ? "Served by the " + res.layer + " layer" : "Nothing matches"));
      if (res.matched) {
        if (res.stubMapping) { out.appendChild(el("h3", "Mapping " + (res.stubMapping.name || res.stubMapping.id || ""))); }
        out.appendChild(el("h3", "Response " + res.response.status));
        out.appendChild(pre(res.response.body || ""));
      } else {
        renderNearMisses(out, res.nearMisses || []);
      }
    });
  });

  fetch("calls.json").then(function (r) { return r.json(); }).then(function (c) {
    calls = c;
    guarded(loadJournal);
  }).catch(function (e) { say("The console could not start: " + e.message, true); });
})();
