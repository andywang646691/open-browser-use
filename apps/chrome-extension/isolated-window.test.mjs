// Node tests for the isolated-window session model (the default OBU window
// placement: every session's tabs live in a dedicated, unfocused, non-minimized
// Chrome window that never disturbs the user).
// Run: node apps/chrome-extension/isolated-window.test.mjs
//
// Covers:
// 1. createTab without params is isolated by default: creates one owned
//    window, reuses it for the next tab, never touches the focused user window.
// 2. window_mode="user" opts back into the legacy shared-window behavior.
// 3. claimUserTab moves a user tab into the session's isolated window (tab
//    groups cannot span windows).
// 4. getUserTabs excludes tabs living in session-owned windows.
// 5. finalizeTabs closes an empty owned window; keeps it when a released user
//    tab still lives in it.
// 6. finalizeTabs moves deliverable tabs into a user-visible window before
//    closing the owned window; keeps the window alive for handoffs.

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import vm from "node:vm";
import assert from "node:assert/strict";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BG_PATH = path.join(HERE, "background.js");

function createChromeFake() {
  const state = {
    nextTabId: 1000,
    nextGroupId: 100,
    nextWindowId: 1,
    focusedWindowId: null,
    // When true, chrome.tabs.group ignores createProperties.windowId and pins
    // the new group to the focused window (observed on some Chrome builds).
    ignoreGroupCreateProperties: false,
    tabs: new Map(),          // id -> { id, windowId, groupId, url, active }
    groups: new Map(),        // id -> { id, windowId, title }
    windows: new Map(),       // id -> { id, focused, type }
    storage: {},
    removedTabs: [],
    removedWindows: [],
    createdWindows: []
  };

  function windowTabCount(windowId) {
    return [...state.tabs.values()].filter((t) => t.windowId === windowId).length;
  }

  function dissolveEmptyGroups() {
    // real Chrome removes a group when its last member leaves
    for (const [groupId, g] of [...state.groups.entries()]) {
      const members = [...state.tabs.values()].filter((t) => t.groupId === groupId);
      if (members.length === 0) {
        state.groups.delete(groupId);
      }
    }
  }

  const chrome = {
    storage: {
      local: {
        async get(key) {
          if (typeof key === "string") {
            return key in state.storage ? { [key]: state.storage[key] } : {};
          }
          return { ...state.storage };
        },
        async set(obj) {
          Object.assign(state.storage, obj);
        }
      }
    },
    tabs: {
      async get(tabId) {
        const tab = state.tabs.get(tabId);
        if (!tab) throw new Error(`No tab with id ${tabId}`);
        return { ...tab };
      },
      async create({ windowId, active = false, url = "about:blank" }) {
        const winId = typeof windowId === "number" ? windowId : 1;
        if (!state.windows.has(winId)) {
          throw new Error(`No window with id ${winId}`);
        }
        const id = state.nextTabId++;
        const tab = {
          id,
          windowId: winId,
          groupId: -1,
          url,
          active: windowTabCount(winId) === 0 ? true : active,
          index: windowTabCount(winId)
        };
        state.tabs.set(id, tab);
        if (tab.active) {
          for (const t of state.tabs.values()) {
            if (t.windowId === winId) t.active = t.id === id;
          }
        }
        return { ...tab };
      },
      async query(filter) {
        const out = [];
        for (const tab of state.tabs.values()) {
          if (filter) {
            if (typeof filter.groupId === "number" && tab.groupId !== filter.groupId) continue;
            if (typeof filter.windowId === "number" && tab.windowId !== filter.windowId) continue;
          }
          out.push({ ...tab });
        }
        return out;
      },
      async move(tabIds, { windowId, index = -1 }) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        if (!state.windows.has(windowId)) {
          throw new Error(`No window with id ${windowId}`);
        }
        const moved = [];
        for (const id of ids) {
          const t = state.tabs.get(id);
          if (!t) continue;
          t.windowId = windowId;
          t.index = index;
          moved.push({ ...t });
        }
        return ids.length === 1 ? moved[0] : moved;
      },
      async remove(tabIds) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        for (const id of ids) {
          if (!state.tabs.has(id)) throw new Error(`No tab with id ${id}`);
          state.removedTabs.push(id);
          state.tabs.delete(id);
        }
        dissolveEmptyGroups();
      },
      async group({ groupId, tabIds, createProperties }) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        let targetGroupId = groupId;
        let groupWindowId = null;
        if (typeof targetGroupId === "number") {
          // real Chrome moves grouped tabs into the group's window; an
          // already-dissolved group rejects the call
          const g = state.groups.get(targetGroupId);
          if (!g) throw new Error(`No tab group ${targetGroupId}`);
          groupWindowId = g.windowId;
        } else {
          // real Chrome defaults a new group to the *current* (focused) window
          // and moves the tabs there — unless createProperties.windowId pins it
          groupWindowId =
            createProperties?.windowId ?? state.focusedWindowId ?? 1;
          if (state.ignoreGroupCreateProperties) {
            groupWindowId = state.focusedWindowId ?? 1;
          }
          targetGroupId = state.nextGroupId++;
          state.groups.set(targetGroupId, {
            id: targetGroupId,
            windowId: groupWindowId,
            title: ""
          });
        }
        for (const id of ids) {
          const t = state.tabs.get(id);
          if (!t) continue;
          if (typeof groupWindowId === "number" && t.windowId !== groupWindowId) {
            t.windowId = groupWindowId;
          }
          t.groupId = targetGroupId;
        }
        return targetGroupId;
      },
      async ungroup(tabIds) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        for (const id of ids) {
          const t = state.tabs.get(id);
          if (t) t.groupId = -1;
        }
        dissolveEmptyGroups();
      },
      async update(tabId, patch) {
        const t = state.tabs.get(tabId);
        if (t) Object.assign(t, patch);
        return t ? { ...t } : {};
      }
    },
    tabGroups: {
      async get(groupId) {
        const g = state.groups.get(groupId);
        if (!g) throw new Error(`No group ${groupId}`);
        return { ...g };
      },
      async query(filter) {
        const out = [];
        for (const g of state.groups.values()) {
          if (filter) {
            if (typeof filter.windowId === "number" && g.windowId !== filter.windowId) continue;
            if (typeof filter.title === "string" && g.title !== filter.title) continue;
          }
          out.push({ ...g });
        }
        return out;
      },
      async update(groupId, patch) {
        const g = state.groups.get(groupId);
        if (g) Object.assign(g, patch);
        return g ? { ...g } : {};
      },
      async move(groupId, { windowId }) {
        const g = state.groups.get(groupId);
        if (!g) throw new Error(`No group ${groupId}`);
        if (!state.windows.has(windowId)) throw new Error(`No window ${windowId}`);
        g.windowId = windowId;
        for (const t of state.tabs.values()) {
          if (t.groupId === groupId) {
            t.windowId = windowId;
          }
        }
        return { ...g, id: groupId };
      },
      onRemoved: { addListener() {} }
    },
    windows: {
      async get(windowId) {
        const win = state.windows.get(windowId);
        if (!win) throw new Error(`No window with id ${windowId}`);
        return { ...win };
      },
      async create(createData = {}) {
        const id = state.nextWindowId++;
        const win = {
          id,
          focused: Boolean(createData.focused),
          type: createData.type ?? "normal",
          state: "normal",
          ...(createData.width ? { width: createData.width } : {}),
          ...(createData.height ? { height: createData.height } : {}),
          ...(typeof createData.left === "number" ? { left: createData.left } : {}),
          ...(typeof createData.top === "number" ? { top: createData.top } : {})
        };
        state.windows.set(id, win);
        state.createdWindows.push(win);
        const tab = await chrome.tabs.create({
          windowId: id,
          url: createData.url ?? "about:blank",
          active: true
        });
        return { ...win, tabs: [{ ...tab }] };
      },
      async getAll({ windowTypes } = {}) {
        const wins = [...state.windows.values()];
        if (Array.isArray(windowTypes)) {
          return wins.filter((w) => windowTypes.includes(w.type)).map((w) => ({ ...w }));
        }
        return wins.map((w) => ({ ...w }));
      },
      async remove(windowId) {
        if (!state.windows.has(windowId)) throw new Error(`No window with id ${windowId}`);
        state.windows.delete(windowId);
        state.removedWindows.push(windowId);
        for (const [id, t] of [...state.tabs.entries()]) {
          if (t.windowId === windowId) state.tabs.delete(id);
        }
      },
      onRemoved: { addListener() {} }
    },
    debugger: {
      async attach() {},
      async detach() {},
      onEvent: { addListener() {} },
      onDetach: { addListener() {} }
    },
    runtime: {
      onInstalled: { addListener() {} },
      onStartup: { addListener() {} },
      onSuspend: { addListener() {} },
      onConnectExternal: { addListener() {} },
      onMessage: { addListener() {} },
      connectNative: () => ({ onMessage: { addListener() {} }, onDisconnect: { addListener() {} }, postMessage() {}, disconnect() {} })
    },
    alarms: { create() { return Promise.resolve(); }, clear() { return Promise.resolve(); }, onAlarm: { addListener() {} } },
    downloads: { onCreated: { addListener() {} }, onChanged: { addListener() {} }, onDeterminingFilename: { addListener() {} } },
    notifications: { create() {}, onClicked: { addListener() {} } }
  };

  function addWindow({ id = state.nextWindowId++, focused = false, type = "normal" } = {}) {
    state.windows.set(id, { id, focused, type });
    if (focused) {
      state.focusedWindowId = id;
    }
    return id;
  }

  function addTab({ windowId = 1, groupId = -1, url = "https://example.com" } = {}) {
    const id = state.nextTabId++;
    state.tabs.set(id, { id, windowId, groupId, url, active: false, index: 0 });
    return id;
  }

  return { chrome, state, helpers: { addWindow, addTab } };
}

async function loadBackground(bgSource, chromeFake) {
  const inject = `globalThis.__obu_export = (name, value) => { globalThis[name] = value; };`;
  const exporter = `\n__obu_export("BrowserBackend", BrowserBackend);`;
  const context = vm.createContext({
    chrome: chromeFake,
    console,
    setTimeout,
    clearTimeout,
    setInterval,
    clearInterval,
    queueMicrotask,
    URL,
    TextEncoder,
    TextDecoder,
    structuredClone,
    crypto: globalThis.crypto,
    Date,
    // A normal desktop display: the isolated window should fill it (minus a
    // small margin) and be centered, not a corner popup.
    screen: { availWidth: 1920, availHeight: 1080 }
  });
  vm.runInContext(inject, context);
  vm.runInContext(
    "chrome.runtime.connectNative = () => ({ onMessage: { addListener(){} }, onDisconnect: { addListener(){} }, postMessage(){}, disconnect(){} });",
    context
  );
  vm.runInContext(bgSource + exporter, context);
  return context.BrowserBackend;
}

function sessionParams(sessionId = "obu-test", extra = {}) {
  return { session_id: sessionId, turn_id: `${sessionId}-1`, ...extra };
}

async function run() {
  const bgSource = await readFile(BG_PATH, "utf8");

  // === Test 1: default is the user window (no owned window); explicit
  //      window_mode="isolated" creates one owned window, reused, and leaves
  //      the user's window untouched ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const defTab = await backend.createTab(sessionParams("obu-def"));
    assert.equal(fake.state.createdWindows.length, 0, "default mode creates no window");
    assert.equal(fake.state.tabs.get(defTab.id).windowId, userWindow, "default tab lands in the user window");
    const first = await backend.createTab(sessionParams("obu-iso", { window_mode: "isolated" }));
    await backend.createTab(sessionParams("obu-iso", { window_mode: "isolated" }));
    assert.equal(fake.state.createdWindows.length, 1, "exactly one window created");
    const win = fake.state.createdWindows[0];
    assert.equal(win.focused, false, "created window must not steal focus");
    assert.equal(win.type, "normal");
    // Normal size, centered — near-fullscreen (1920x1080 screen minus 2x24px
    // margin), not a corner-sized popup. A full viewport keeps SPA virtual-
    // scroll lists mounting real content.
    assert.equal(win.width, 1920 - 48, "isolated window fills the screen width");
    assert.equal(win.height, 1080 - 48, "isolated window fills the screen height");
    assert.equal(win.left, 24, "isolated window is centered horizontally");
    assert.equal(win.top, 24, "isolated window is centered vertically");
    assert.equal(first.windowId, win.id, "createTab result carries the owned windowId");
    assert.equal(typeof first.windowId, "number", "createTab returns windowId");
    const session = await backend.store.getSession("obu-iso");
    assert.equal(session.windowId, win.id, "session records the owned window");
    assert.equal(
      typeof session.chromeGroupId,
      "number",
      "isolated session establishes a tab group"
    );
    const group = fake.state.groups.get(session.chromeGroupId);
    assert.equal(group?.windowId, win.id, "session group anchors in the owned window");
    const allTabIds = [...fake.state.tabs.keys()];
    const sessionTabIds = allTabIds.filter((id) => id >= first.id);
    for (const tabId of sessionTabIds) {
      const tab = fake.state.tabs.get(tabId);
      assert.equal(tab.windowId, win.id, `session tab ${tabId} must be in the owned window`);
    }
    const isoSessionTabIds = [...fake.state.tabs.keys()].filter((id) => id >= first.id);
    const leakingTabs = isoSessionTabIds.filter((id) => fake.state.tabs.get(id).windowId === userWindow);
    assert.equal(leakingTabs.length, 0, "no isolated-session tab leaked into the user's window");
  }

  // === Test 2: window_mode="user" opts out into the legacy shared-window path ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-user", { window_mode: "user" }));
    assert.equal(fake.state.createdWindows.length, 0, "no window created in user mode");
    assert.equal(fake.state.tabs.get(tab.id).windowId, userWindow, "tab lands in the focused user window");
  }

  // === Test 3: claim moves a user tab into the session's isolated window ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" }); // userWindow = 1
    const userTab = fake.helpers.addTab({ windowId: 1, url: "https://x.com/home" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const claimed = await backend.claimUserTab(
      sessionParams("obu-claim", { tabId: userTab, window_mode: "isolated" })
    );
    const session = await backend.store.getSession("obu-claim");
    assert.equal(claimed.id, userTab);
    assert.equal(
      fake.state.tabs.get(userTab).windowId,
      session.windowId,
      "claimed tab moved into the isolated window"
    );
    const sessionTabs = await backend.getSessionTabs("obu-claim");
    assert.ok(
      [...sessionTabs].some((t) => t.id === userTab),
      "claimed tab is tracked as a session tab (window-based membership)"
    );
    assert.equal(
      typeof session.chromeGroupId,
      "number",
      "claim establishes a tab group in the isolated session"
    );
    assert.equal(
      fake.state.groups.get(session.chromeGroupId)?.windowId,
      session.windowId,
      "claim group anchors in the owned window"
    );
    assert.equal(fake.state.tabs.get(userTab).groupId, session.chromeGroupId, "claimed tab joins the group");
  }

  // === Test 4: getUserTabs excludes session-owned window tabs ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const userTab = fake.helpers.addTab({ windowId: userWindow, url: "https://example.com" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const created = await backend.createTab(sessionParams("obu-test", { window_mode: "isolated" }));
    const tabs = await backend.getUserTabs(sessionParams());
    const ids = [...tabs].map((t) => t.id);
    assert.ok(ids.includes(userTab), "real user tab still listed");
    assert.ok(ids.includes(created.id), "all real pages remain discoverable");
    assert.equal(tabs.find((t) => t.id === created.id).ownerSessionId, "obu-test");
  }

  // === Test 5: finalize closes the owned window only when it is empty ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-test", { window_mode: "isolated" }));
    const session = await backend.store.getSession("obu-test");
    const winId = session.windowId;
    const report = await backend.finalizeTabs(sessionParams("obu-test", { keep: [] }));
    assert.deepEqual([...report.closed], [tab.id]);
    assert.ok(fake.state.removedWindows.includes(winId), "empty owned window closed at finalize");
    const sessions = fake.state.storage["OPEN_BROWSER_USE_SESSION_STATE"]?.sessions ?? {};
    assert.equal(sessions["obu-test"], undefined, "session removed");
  }

  // === Test 6: finalize never closes a window holding a released user tab ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const userTab = fake.helpers.addTab({ windowId: userWindow, url: "https://example.com" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    await backend.claimUserTab(
      sessionParams("obu-test", { tabId: userTab, window_mode: "isolated" })
    );
    const session = await backend.store.getSession("obu-test");
    const winId = session.windowId;
    await backend.finalizeTabs(sessionParams("obu-test", { keep: [] }));
    assert.ok(!fake.state.removedWindows.includes(winId), "window with a released user tab stays open");
    assert.equal(fake.state.tabs.get(userTab).windowId, winId, "released user tab remains in the owned window");
  }

  // === Test 7: finalize moves deliverable tabs to a user window, then closes the owned window ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-test", { window_mode: "isolated" }));
    const session = await backend.store.getSession("obu-test");
    const winId = session.windowId;
    const report = await backend.finalizeTabs(
      sessionParams("obu-test", { keep: [{ tabId: tab.id, status: "deliverable" }] })
    );
    assert.deepEqual(
      [...report.kept].map((k) => ({ tabId: k.tabId, status: k.status })),
      [{ tabId: tab.id, status: "deliverable" }]
    );
    assert.equal(
      fake.state.tabs.get(tab.id).windowId,
      userWindow,
      "deliverable tab moved into the user's window"
    );
    assert.ok(fake.state.removedWindows.includes(winId), "owned window closed after deliverable left it");
  }

  // === Test 8: handoff keeps the owned window alive for the next turn ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-test", { window_mode: "isolated" }));
    const session = await backend.store.getSession("obu-test");
    const winId = session.windowId;
    await backend.finalizeTabs(
      sessionParams("obu-test", { keep: [{ tabId: tab.id, status: "handoff" }] })
    );
    assert.ok(!fake.state.removedWindows.includes(winId), "handoff keeps the owned window");
    const sessions = fake.state.storage["OPEN_BROWSER_USE_SESSION_STATE"]?.sessions ?? {};
    assert.ok(sessions["obu-test"], "handoff keeps the session");
  }

  // === Test 9: a user-mode tab opened inside an isolated session is still finalized ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const a = await backend.createTab(sessionParams("obu-mix", { window_mode: "isolated" }));
    const ua = await backend.createTab(sessionParams("obu-mix", { window_mode: "user" }));
    const report = await backend.finalizeTabs(sessionParams("obu-mix", { keep: [] }));
    assert.deepEqual(
      [...report.closed].sort(),
      [ua.id, a.id].sort(),
      "user-mode tab in an isolated session is closed with the session"
    );
  }

  // === Test 10: buggy Chrome (createProperties ignored) — group repaired into the owned window ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    fake.state.ignoreGroupCreateProperties = true;
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const a = await backend.createTab(sessionParams("obu-repair", { window_mode: "isolated" }));
    const b = await backend.createTab(sessionParams("obu-repair", { window_mode: "isolated" }));
    const session = await backend.store.getSession("obu-repair");
    assert.equal(session.windowId, a.windowId, "owned window recorded");
    assert.equal(a.windowId, b.windowId, "both tabs stay in the same owned window");
    const group = fake.state.groups.get(session.chromeGroupId);
    assert.ok(group, "session group exists after repair");
    assert.equal(
      group.windowId,
      session.windowId,
      "group anchored in the owned window despite buggy createProperties"
    );
    assert.equal(fake.state.tabs.get(a.id).groupId, session.chromeGroupId, "session tab joined the group");
    const windowTabs = [...fake.state.tabs.values()].filter((t) => t.windowId === session.windowId);
    assert.equal(windowTabs.length, 2, "no scaffold leftover in the owned window");
    const userWindowTabs = [...fake.state.tabs.values()].filter((t) => t.windowId === userWindow);
    assert.equal(userWindowTabs.length, 0, "no scaffold or session tab leaked into the user's window");
  }

  // === Test 11: re-claiming a session's own tab keeps agent origin — finalize still closes it ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-reclaim", { window_mode: "isolated" }));
    await backend.claimUserTab(sessionParams("obu-reclaim", { tabId: tab.id }));
    const session = await backend.store.getSession("obu-reclaim");
    assert.equal(session.tabOrigins[String(tab.id)], "agent", "re-claim keeps the agent origin");
    const report = await backend.finalizeTabs(sessionParams("obu-reclaim", { keep: [] }));
    assert.deepEqual([...report.closed], [tab.id], "agent tab closed after re-claim");
    assert.ok(
      fake.state.removedWindows.includes(session.windowId),
      "owned window closed after its only tab was finalized"
    );
  }

  // === Test 12: claim-created window's startup blank is removed immediately ===
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const userTab = fake.helpers.addTab({ windowId: userWindow, url: "https://linux.do" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    await backend.claimUserTab(
      sessionParams("obu-claimfirst", { tabId: userTab, window_mode: "isolated" })
    );
    const session = await backend.store.getSession("obu-claimfirst");
    const winId = session.windowId;
    const windowTabs = await fake.chrome.tabs.query({ windowId: winId });
    assert.deepEqual(windowTabs.map(t => t.id), [userTab], "claim leaves no startup blank");
    assert.deepEqual(Object.keys(session.tabOrigins), [String(userTab)], "startup blank is no longer tracked");
    const userBlank = fake.helpers.addTab({ windowId: winId, url: "about:blank" });
    const report = await backend.finalizeTabs(sessionParams("obu-claimfirst", { keep: [] }));
    assert.deepEqual([...report.closed], [], "no leftover startup page needs finalize");
    assert.ok(fake.state.tabs.has(userBlank), "never close an unrelated user blank");
    assert.deepEqual([...report.released], [userTab], "claimed user tab is released");
    assert.ok(
      !fake.state.removedWindows.includes(winId),
      "window kept because the released user tab still lives in it"
    );
  }

  // Explicit ownership survives grouping changes, but release does not move
  // pages and never grants the old window owner access to them again.
  {
    const fake = createChromeFake();
    const userWindow = fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const p = (id, extra = {}) => sessionParams(id, extra);
    const a = await backend.createTab(p("a", { window_mode: "isolated" }));
    const original = { ...fake.state.tabs.get(a.id) };
    const unrelated = fake.helpers.addTab({ windowId: original.windowId, url: "https://example.test/user" });
    assert.ok(!(await backend.getTabs(p("a"))).some(t => t.id === unrelated));
    const released = await backend.releaseTabs(p("a"));
    assert.deepEqual([...released.released], [a.id]);
    assert.equal(released.failed.length, 0);
    assert.equal(fake.state.tabs.get(a.id).windowId, original.windowId);
    assert.equal(fake.state.tabs.get(a.id).groupId, original.groupId);
    assert.equal((await backend.getTabs(p("a"))).length, 0);
    await assert.rejects(() => backend.attach(p("a", { tabId: a.id })), /not part/);
    await backend.claimUserTab(p("b", { tabId: a.id }));
    const denied = await backend.releaseTabs(p("a", { tabIds: [a.id] }));
    assert.equal(denied.failed.length, 1);
    await assert.rejects(() => backend.closeUserTab(p("a", { tabId: a.id })), /not part/);
    await backend.markDeliverable(p("b", { tabId: a.id }));
    assert.equal((await backend.store.findTabOwner(a.id)).sessionId, "b");
    assert.equal((await backend.getTabs(p("b"))).length, 1);
    await backend.releaseTabs(p("b"));
    const again = await backend.releaseTabs(p("b", { tabIds: [a.id] }));
    assert.deepEqual([...again.alreadyReleased], [a.id]);
    assert.ok(fake.state.tabs.has(unrelated));

    // Reload persisted ownership, then race two different claimers.
    const restarted = new Backend();
    await restarted.store.ready;
    const race = await Promise.allSettled([
      restarted.claimUserTab(p("c", { tabId: a.id })),
      restarted.claimUserTab(p("d", { tabId: a.id }))
    ]);
    assert.equal(race.filter(r => r.status === "fulfilled").length, 1);
    assert.equal(race.filter(r => r.status === "rejected").length, 1);
    const owner = (await restarted.store.findTabOwner(a.id)).sessionId;
    restarted.attachedTabs.add(a.id);
    const detach = fake.chrome.debugger.detach;
    fake.chrome.debugger.detach = async () => { throw new Error("detach failed"); };
    const failed = await restarted.releaseTabs(p(owner));
    assert.equal(failed.failed.length, 1);
    assert.deepEqual([...failed.remaining], [a.id]);
    fake.chrome.debugger.detach = detach;
    const save = restarted.store.save.bind(restarted.store);
    restarted.store.save = async () => { throw new Error("storage failed"); };
    const storageFailed = await restarted.releaseTabs(p(owner));
    assert.equal(storageFailed.failed.length, 1);
    assert.equal((await restarted.store.findTabOwner(a.id)).sessionId, owner);
    restarted.store.save = save;
    await restarted.releaseTabs(p(owner));
    assert.equal(await restarted.store.findTabOwner(a.id), null);

    const result = await restarted.createTab(p("result", { window_mode: "isolated" }));
    await restarted.markDeliverable(p("result", { tabId: result.id }));
    assert.equal(fake.state.tabs.get(result.id).windowId, userWindow);
    assert.equal((await restarted.store.findTabOwner(result.id)).sessionId, "result");
    await restarted.markDeliverable(p("result", { tabId: result.id }));
    await fake.chrome.tabs.remove(result.id);
    const gone = await restarted.releaseTabs(p("result"));
    assert.deepEqual([...gone.released], [result.id]);
    assert.equal(gone.remaining.length, 0);
  }

  console.log("isolated-window tests passed");
}

run().catch((error) => {
  console.error(error);
  process.exit(1);
});