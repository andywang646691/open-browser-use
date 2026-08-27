// Node tests for the CDP-level background wake (focus emulation + lifecycle
// "active") applied automatically to isolated-window session tabs on fresh
// attach, re-applied on main-frame navigation, and reset by detach. Run:
//   node apps/chrome-extension/background-wake.test.mjs

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import vm from "node:vm";
import assert from "node:assert/strict";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BG_PATH = path.join(HERE, "background.js");

function createChromeFake() {
  const state = {
    sentCommands: [],        // wake CDP commands in order: {method, params}
    failWakeMethods: new Set(), // CDP methods simulated to fail during wake
    onDetachListeners: [],   // chrome.debugger.onDetach registrations
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
      async detach(target) {
        // Real Chrome fires onDetach when the debugger session ends.
        if (typeof target?.tabId === "number") {
          for (const fn of state.onDetachListeners) {
            fn({ tabId: target.tabId });
          }
        }
      },
      async sendCommand(target, method, params) {
        state.sentCommands.push({ method, params: params ?? {} });
        if (state.failWakeMethods.has(method)) {
          throw new Error(`simulated failure for ${method}`);
        }
        return {};
      },
      onEvent: { addListener() {} },
      onDetach: { addListener(fn) { state.onDetachListeners.push(fn); } }
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
  // microtask flush so fire-and-forget frameNavigated wake replays land
  const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

  // === Test 1: fresh attach on an isolated session tab wakes with focus + lifecycle ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-wake"));
    await fake.chrome.tabs.update(tab.id, { url: "https://x.com/home" });
    const res = await backend.attach({ ...sessionParams("obu-wake"), tabId: tab.id });
    assert.equal(fake.state.sentCommands.length, 2, "fresh attach sends the two wake commands");
    assert.equal(
      fake.state.sentCommands[0].method,
      "Emulation.setFocusEmulationEnabled",
      "focus emulation enabled"
    );
    assert.equal(fake.state.sentCommands[0].params.enabled, true);
    assert.equal(
      fake.state.sentCommands[1].method,
      "Page.setWebLifecycleState",
      "lifecycle set to active"
    );
    assert.equal(fake.state.sentCommands[1].params.state, "active");
    assert.equal(res.wake.focus.ok, true, "focus wake reported ok");
    assert.equal(res.wake.lifecycle.ok, true, "lifecycle wake reported ok");
  }

  // === Test 2: a second attach (already attached) does not re-send the wake ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-wake2"));
    await backend.attach({ ...sessionParams("obu-wake2"), tabId: tab.id });
    await backend.attach({ ...sessionParams("obu-wake2"), tabId: tab.id });
    assert.equal(fake.state.sentCommands.length, 2, "re-attach is a no-op for the wake");
  }

  // === Test 3: user-mode (shared window) attach never wakes ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-wake-user", { window_mode: "user" }));
    await fake.chrome.tabs.update(tab.id, { url: "https://x.com/home" });
    const res = await backend.attach({ ...sessionParams("obu-wake-user"), tabId: tab.id });
    assert.equal(res.wake, null, "user-mode tab is not woken");
    assert.equal(fake.state.sentCommands.length, 0, "no CDP wake commands for user-mode tabs");
  }

  // === Test 4: main-frame navigation re-applies the wake; sub-frames do not;
  //      detach clears the wake set so later navigations stay quiet ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-wake4"));
    await backend.attach({ ...sessionParams("obu-wake4"), tabId: tab.id });
    assert.equal(fake.state.sentCommands.length, 2, "setup wake");

    backend.handleCdpEvent({ tabId: tab.id }, "Page.frameNavigated", {
      frame: { id: "f1", url: "https://x.com/home", parentId: undefined }
    });
    await flush();
    assert.equal(fake.state.sentCommands.length, 4, "main-frame navigation re-applies the wake");

    backend.handleCdpEvent({ tabId: tab.id }, "Page.frameNavigated", {
      frame: { id: "sub", url: "https://x.com/embed", parentId: "f1" }
    });
    await flush();
    assert.equal(fake.state.sentCommands.length, 4, "sub-frame navigation does not re-apply");

    await backend.detach({ ...sessionParams("obu-wake4"), tabId: tab.id });
    backend.handleCdpEvent({ tabId: tab.id }, "Page.frameNavigated", {
      frame: { id: "f2", url: "https://x.com/else", parentId: undefined }
    });
    await flush();
    assert.equal(fake.state.sentCommands.length, 4, "no wake replay after detach");

    // Re-attach wakes again (fresh attach path)
    await backend.attach({ ...sessionParams("obu-wake4"), tabId: tab.id });
    assert.equal(fake.state.sentCommands.length, 6, "re-attach after detach wakes again");
  }

  // === Test 5: wake command failures are tolerated; attach still succeeds ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-wake5"));
    fake.state.failWakeMethods.add("Emulation.setFocusEmulationEnabled");
    const res = await backend.attach({ ...sessionParams("obu-wake5"), tabId: tab.id });
    assert.equal(res.wake.focus.ok, false, "focus failure reported, not thrown");
    assert.ok(
      String(res.wake.focus.error).includes("simulated failure"),
      "focus error surfaced"
    );
    assert.equal(res.wake.lifecycle.ok, true, "lifecycle still attempted and succeeds");
  }

  // === Test 6: a session tab living outside the owned window is not woken
  //      (rare mixed case: isolated session + tab that ended up in a user window) ===
  {
    const fake = createChromeFake();
    fake.helpers.addWindow({ focused: true, type: "normal" });
    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();
    const tab = await backend.createTab(sessionParams("obu-wake6"));
    fake.state.tabs.get(tab.id).windowId = 1; // moved out of the owned window
    const res = await backend.attach({ ...sessionParams("obu-wake6"), tabId: tab.id });
    assert.equal(res.wake, null, "tab outside the owned window is not woken");
    assert.equal(fake.state.sentCommands.length, 0, "no wake commands for non-owned-window tab");
  }

  console.log("background-wake tests passed");
}

run().catch((error) => {
  console.error(error);
  process.exit(1);
});
