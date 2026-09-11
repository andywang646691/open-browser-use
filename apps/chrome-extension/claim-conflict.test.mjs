// Node test for claimUserTab cross-session conflict message (the
// "agent stuck on a residual session's claim" fix).
// Run: node apps/chrome-extension/claim-conflict.test.mjs
//
// Loads background.js into a sandbox with stubbed chrome.* APIs and exercises
// BrowserBackend.claimUserTab when the target tab already belongs to another
// session's group. The thrown error must name the owning session AND carry an
// actionable recovery hint (resume via OBU_SESSION_ID) so agents can adopt a
// residual session instead of grinding on cryptic conflicts.

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
    tabs: new Map(), // id -> { id, windowId, groupId }
    groups: new Map() // id -> { id, windowId, title }
  };

  const chrome = {
    storage: {
      local: {
        async get(key) {
          if (!state.storage) state.storage = {};
          if (typeof key === "string") {
            return key in state.storage ? { [key]: state.storage[key] } : {};
          }
          return { ...state.storage };
        },
        async set(obj) {
          if (!state.storage) state.storage = {};
          Object.assign(state.storage, obj);
        }
      }
    },
    tabs: {
      async get(tabId) {
        const tab = state.tabs.get(tabId);
        if (!tab) throw new Error(`No tab ${tabId}`);
        return { ...tab };
      },
      async query(filter) {
        const out = [];
        for (const tab of state.tabs.values()) {
          if (filter && typeof filter.groupId === "number" && tab.groupId !== filter.groupId) continue;
          out.push({ ...tab });
        }
        return out;
      },
      async remove(tabIds) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        for (const id of ids) state.tabs.delete(id);
      },
      async group({ groupId, tabIds }) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        let targetGroupId = groupId;
        if (typeof targetGroupId !== "number") {
          targetGroupId = state.nextGroupId++;
          const first = state.tabs.get(ids[0]);
          state.groups.set(targetGroupId, { id: targetGroupId, windowId: first?.windowId ?? 1, title: "" });
        }
        for (const id of ids) {
          const t = state.tabs.get(id);
          if (t) t.groupId = targetGroupId;
        }
        return targetGroupId;
      },
      async ungroup(tabIds) {
        const ids = Array.isArray(tabIds) ? tabIds : [tabIds];
        for (const id of ids) {
          const t = state.tabs.get(id);
          if (t) t.groupId = -1;
        }
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
      async query() {
        return [...state.groups.values()].map((g) => ({ ...g }));
      },
      async update(groupId, patch) {
        const g = state.groups.get(groupId);
        if (g) Object.assign(g, patch);
        return g ? { ...g } : {};
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
    windows: { getAll: async () => [], onRemoved: { addListener() {} } },
    downloads: { onCreated: { addListener() {} }, onChanged: { addListener() {} }, onDeterminingFilename: { addListener() {} } },
    notifications: { create() {}, onClicked: { addListener() {} } }
  };

  function createTab({ windowId = 1, groupId = -1, url = "https://x.com/home" } = {}) {
    const id = state.nextTabId++;
    state.tabs.set(id, { id, windowId, groupId, url });
    return id;
  }

  return { chrome, state, helpers: { createTab } };
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
    Date
  });
  vm.runInContext(inject, context);
  vm.runInContext(bgSource + exporter, context);
  return context.BrowserBackend;
}

async function run() {
  const bgSource = await readFile(BG_PATH, "utf8");
  const OLD_SESSION = "obu-xcom-posts-20260825111737";

  // Tab sits in a group owned by a residual (never finalized) session.
  const fake = createChromeFake();
  const tabId = fake.helpers.createTab({ groupId: 100 });
  fake.state.groups.set(100, { id: 100, windowId: 1, title: `${OLD_SESSION} group` });

  const Backend = await loadBackground(bgSource, fake.chrome);
  const backend = new Backend();

  // Seed the owning session directly into the store, pointing at group 100.
  const ownerState = await backend.store.getSession(OLD_SESSION);
  ownerState.chromeGroupId = 100;
  ownerState.tabOrigins[String(tabId)] = "user";
  await backend.store.save();

  // A different session tries to claim the tab → must fail with a hint.
  await assert.rejects(
    () => backend.claimUserTab({ session_id: "obu-new-task", turn_id: "turn-1", tabId, window_mode: "user" }),
    (error) => {
      const message = String(error.message);
      assert.ok(message.includes(`owned by browser session ${OLD_SESSION}`), `message should name the owning session, got: ${message}`);
      assert.ok(message.includes("release_tabs()"), `message should carry the release hint, got: ${message}`);
      assert.equal(error.data.ownerSessionId, OLD_SESSION);
      return true;
    },
    "claimUserTab must reject tabs owned by another session"
  );

  // Same session re-claiming its own tab is fine (no conflict).
  const reclaimed = await backend.claimUserTab({ session_id: OLD_SESSION, turn_id: "turn-1", tabId, window_mode: "user" });
  assert.equal(reclaimed.id, tabId, "owner session can re-claim its own tab");

  console.log("All claim-conflict tests passed.");
}

run().catch((err) => {
  console.error(err);
  process.exit(1);
});
