// Node tests for session_inject: per-session MAIN-world content scripts
// registered via chrome.scripting (pre-page-script injection), scoped to
// session tab origins, unregistered at finalize. Run:
//   node apps/chrome-extension/main-inject.test.mjs

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import vm from "node:vm";
import assert from "node:assert/strict";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BG_PATH = path.join(HERE, "background.js");

function createChromeFake() {
  const state = {
    injectedScripts: [],
    unregisteredIds: [],
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
    scripting: {
      async registerContentScripts(list) {
        for (const s of list) {
          state.injectedScripts = state.injectedScripts.filter((x) => x.id !== s.id);
          state.injectedScripts.push(s);
        }
      },
      async unregisterContentScripts({ ids }) {
        state.unregisteredIds.push(...ids);
        state.injectedScripts = state.injectedScripts.filter((x) => !ids.includes(x.id));
      },
      async getRegisteredContentScripts() {
        return [...state.injectedScripts];
      }
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

  // === Test 1: register MAIN-world, DOCUMENT_START script scoped to session origin ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-inj"));
    await fake.chrome.tabs.update(tab.id, { url: "https://x.com/home" });
    const res = await backend.injectMainScript({ ...sessionParams("obu-inj"), script: "visibility" });
    assert.equal(res.id, "obu-main-inject-obu-inj", "id is session-scoped");
    assert.equal(res.origins.length, 1, "one scoped origin");
    assert.equal(res.origins[0], "https://x.com/*", "matches scoped to session tab origin");
    assert.equal(fake.state.injectedScripts.length, 1, "one registration");
    const reg = fake.state.injectedScripts[0];
    assert.equal(reg.runAt, "document_start", "runs before page scripts");
    assert.equal(reg.world, "MAIN", "runs in the page's real JS world");
    assert.equal(reg.matches.join(","), "https://x.com/*", "registered matches");
    assert.equal(reg.js.join(","), "session-inject-main.js", "template file");
    assert.equal(reg.persistAcrossSessions, false, "not persisted beyond the session");
    const session = await backend.store.getSession("obu-inj");
    assert.equal(session.mainInjectScripts.join(","), "obu-main-inject-obu-inj", "tracked for finalize cleanup");
  }

  // === Test 2: re-registration is idempotent (unregister first), still one live script ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-inj2"));
    await fake.chrome.tabs.update(tab.id, { url: "https://x.com/home" });
    await backend.injectMainScript({ ...sessionParams("obu-inj2"), script: "visibility" });
    await backend.injectMainScript({ ...sessionParams("obu-inj2"), script: "visibility" });
    assert.equal(fake.state.injectedScripts.length, 1, "single live registration after second call");
    // matches follow navigation: re-scope to the new origin
    await fake.chrome.tabs.update(tab.id, { url: "https://example.com/foo" });
    await backend.injectMainScript({ ...sessionParams("obu-inj2"), script: "visibility" });
    assert.equal(fake.state.injectedScripts.length, 1, "still one registration");
    assert.equal(fake.state.injectedScripts[0].matches.join(","), "https://example.com/*", "scope follows navigation");
  }

  // === Test 3: unknown template rejected; no HTTP tab -> informative error ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-inj3"));
    // only about:blank tab -> no scoping origin
    await assert.rejects(
      backend.injectMainScript({ ...sessionParams("obu-inj3"), script: "visibility" }),
      /no HTTP\(S\) session tab/,
      "no HTTP(S) session tab to scope the injection"
    );
    await fake.chrome.tabs.update(tab.id, { url: "https://x.com/" });
    await assert.rejects(
      backend.injectMainScript({ ...sessionParams("obu-inj3"), script: "bogus" }),
      /unknown template/,
      "unknown template rejected"
    );
  }

  // === Test 4: finalize unregisters the injection and clears session state ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-inj4"));
    await fake.chrome.tabs.update(tab.id, { url: "https://x.com/home" });
    await backend.injectMainScript({ ...sessionParams("obu-inj4"), script: "visibility" });
    const report = await backend.finalizeTabs(sessionParams("obu-inj4", { keep: [] }));
    assert.deepEqual([...report.closed], [tab.id], "session tab closed");
    assert.ok(
      fake.state.unregisteredIds.includes("obu-main-inject-obu-inj4"),
      "content script unregistered at finalize"
    );
    const session = await backend.store.getSession("obu-inj4");
    assert.equal(session.mainInjectScripts, undefined, "session injection state cleared");
  }

  console.log("main-inject tests passed");
}

run().catch((error) => {
  console.error(error);
  process.exit(1);
});
