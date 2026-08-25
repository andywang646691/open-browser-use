// Node tests for finalizeTabs report + closeUserTab fallback (the
// "finalize_tabs returned {} but the tab stayed open" fix).
// Run: node apps/chrome-extension/finalize-report.test.mjs
//
// Loads background.js into a sandbox with stubbed chrome.* APIs and checks:
// 1. finalizeTabs returns {closed, released, kept} naming exactly what
//    happened, so agents can verify instead of trusting silence.
// 2. finalizeTabs on a session whose tab group dissolved returns a warning
//    ("nothing was closed") instead of an empty silent no-op.
// 3. closeUserTab closes a real tab by id and refuses chrome:// pages —
//    the cleanup fallback for leftover tabs.

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
    tabs: new Map(),
    groups: new Map(),
    storage: {},
    removed: []
  };

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
        for (const id of ids) {
          if (!state.tabs.has(id)) throw new Error(`No tab with id ${id}`);
          state.removed.push(id);
          state.tabs.delete(id);
        }
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

  function createTab({ windowId = 1, groupId = -1, url = "https://example.com" } = {}) {
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
  vm.runInContext(
    "chrome.runtime.connectNative = () => ({ onMessage: { addListener(){} }, onDisconnect: { addListener(){} }, postMessage(){}, disconnect(){} });",
    context
  );
  vm.runInContext(bgSource + exporter, context);
  return context.BrowserBackend;
}

async function run() {
  const bgSource = await readFile(BG_PATH, "utf8");

  // === Test 1: finalize reports closed/released/kept explicitly ===
  {
    const fake = createChromeFake();
    const agentTab = fake.helpers.createTab({ groupId: 100, url: "https://x.com/home" });
    const userTab = fake.helpers.createTab({ groupId: 100, url: "https://example.com" });

    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();

    // Session owns group 100; agent created one tab, claimed the other.
    const sessionState = await backend.store.getSession("obu-task");
    sessionState.chromeGroupId = 100;
    sessionState.tabOrigins = { [String(agentTab)]: "agent", [String(userTab)]: "user" };
    await backend.store.save();

    const report = await backend.finalizeTabs({ session_id: "obu-task", turn_id: "t1", keep: [] });
    // Spread into host-realm arrays: report comes from the vm sandbox, whose
    // Array prototype differs, so strict deepEqual would reject it wholesale.
    assert.deepEqual([...report.closed], [agentTab], "agent-created tab is reported as closed");
    assert.deepEqual([...report.released], [userTab], "claimed user tab is reported as released");
    assert.deepEqual([...report.kept], [], "nothing kept");
    assert.ok(!("warning" in report), "no warning when tabs were actually processed");
    assert.ok(fake.state.removed.includes(agentTab), "agent tab was really removed");
    assert.ok(!fake.state.removed.includes(userTab), "claimed user tab stays open (only ungrouped)");
  }

  // === Test 2: dissolved session → warning instead of silent no-op ===
  {
    const fake = createChromeFake();
    const strayTab = fake.helpers.createTab({ url: "https://x.com/home" }); // not in any known group

    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();

    // getSession auto-creates an empty shell: no chromeGroupId → no tabs found.
    const report = await backend.finalizeTabs({ session_id: "obu-lost", turn_id: "t1", keep: [] });
    assert.deepEqual([...report.closed], []);
    assert.ok(typeof report.warning === "string" && report.warning.includes("nothing was closed"), `expected a nothing-closed warning, got: ${JSON.stringify(report)}`);
    assert.ok(fake.state.removed.length === 0, "no tabs were touched");
    assert.ok(fake.state.tabs.has(strayTab), "stray tab survives (agent must clean up via close_tab)");
  }

  // === Test 3: closeUserTab closes by id, refuses chrome:// ===
  {
    const fake = createChromeFake();
    const target = fake.helpers.createTab({ url: "https://x.com/home" });
    const internal = fake.helpers.createTab({ url: "chrome://settings" });

    const Backend = await loadBackground(bgSource, fake.chrome);
    const backend = new Backend();

    const result = await backend.closeUserTab({ session_id: "obu-task", turn_id: "t1", tabId: target });
    assert.equal(result.closed, target, "returns the closed tab id");
    assert.ok(!fake.state.tabs.has(target), "tab was really removed");

    await assert.rejects(
      () => backend.closeUserTab({ session_id: "obu-task", turn_id: "t1", tabId: internal }),
      /cannot be closed/,
      "chrome:// pages are refused"
    );
    await assert.rejects(
      () => backend.closeUserTab({ session_id: "obu-task", turn_id: "t1", tabId: 999999 }),
      /No tab with id/,
      "unknown tab ids error instead of pretending"
    );
  }

  console.log("All finalize-report tests passed.");
}

run().catch((err) => {
  console.error(err);
  process.exit(1);
});
