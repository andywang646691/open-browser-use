// Node test for getTargetMapping
// Run: node apps/chrome-extension/target-mapping.test.mjs

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import vm from "node:vm";
import assert from "node:assert/strict";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BG_PATH = path.join(HERE, "background.js");

function noopEvent() {
  return { addListener() {}, removeListener() {} };
}

async function fixture(opts = {}) {
  const bgSource = await readFile(BG_PATH, "utf8");
  const calls = [];
  const tabs = new Map([
    [101, { id: 101, url: "https://example.com/1", discarded: !!opts.discarded101, frozen: false }],
    [102, { id: 102, url: "https://example.com/2", discarded: false, frozen: false }]
  ]);

  const chromeMock = {
    storage: {
      local: {
        get: async () => ({}),
        set: async () => {}
      }
    },
    tabs: {
      get: async (id) => {
        calls.push(["tabs.get", id]);
        const tab = tabs.get(id);
        if (!tab) throw new Error(`Tab ${id} not found`);
        return tab;
      },
      reload: async (id) => {
        calls.push(["tabs.reload", id]);
        if (opts.reloadFails) throw new Error("Reload network failure");
        const tab = tabs.get(id);
        if (tab) tab.discarded = false;
      },
      query: async () => [],
      onRemoved: noopEvent()
    },
    tabGroups: { get: async () => ({}), query: async () => [], update: async () => ({}), onRemoved: noopEvent() },
    windows: { create: async () => ({ tabs: [{ id: 1 }] }), getAll: async () => [], onRemoved: noopEvent() },
    debugger: {
      attach: async () => {
        calls.push(["debugger.attach"]);
        throw new Error("debugger.attach should NEVER be called by getTargetMapping");
      },
      detach: async () => {},
      sendCommand: async () => ({}),
      getTargets: async () => {
        calls.push(["debugger.getTargets"]);
        return [
          { id: "T-101", tabId: 101, type: "page" },
          { id: "T-102", tabId: 102, type: "page" }
        ];
      },
      onEvent: noopEvent(),
      onDetach: noopEvent()
    },
    runtime: {
      id: "test-ext-id",
      getManifest: () => ({ version: "1.0.0" }),
      onInstalled: noopEvent(),
      onStartup: noopEvent(),
      onSuspend: noopEvent(),
      onConnectExternal: noopEvent(),
      onMessage: noopEvent(),
      connectNative: () => ({ onMessage: noopEvent(), onDisconnect: noopEvent(), postMessage() {}, disconnect() {} })
    },
    alarms: { create: async () => {}, clear: async () => {}, onAlarm: noopEvent() },
    downloads: { onCreated: noopEvent(), onChanged: noopEvent() }
  };

  const context = vm.createContext({
    chrome: chromeMock,
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
    crypto: globalThis.crypto
  });
  vm.runInContext(bgSource + "\nglobalThis.Backend = BrowserBackend;", context);
  const backend = new context.Backend({
    readinessTimeoutMs: 200,
    queueTimeoutMs: 200,
    cursorTimeoutMs: 50
  });

  const session = await backend.store.getSession("sess-1");
  session.tabOrigins[101] = "created";

  return { backend, calls, tabs };
}

async function run() {
  // Test 1: getTargetMapping returns exact { tabId, targetId } without attaching
  {
    const { backend, calls } = await fixture();
    const result = await backend.getTargetMapping({
      session_id: "sess-1",
      turn_id: "turn-1",
      tabId: 101
    });
    assert.equal(result.tabId, 101);
    assert.equal(result.targetId, "T-101");
    assert.ok(!calls.some((c) => c[0] === "debugger.attach"), "must not attach chrome.debugger");
  }

  // Test 2: requires session_id
  {
    const { backend } = await fixture();
    await assert.rejects(
      async () => {
        await backend.getTargetMapping({ turn_id: "turn-1", tabId: 101 });
      },
      /Missing required browser session_id/
    );
  }

  // Test 3: requires turn_id
  {
    const { backend } = await fixture();
    await assert.rejects(
      async () => {
        await backend.getTargetMapping({ session_id: "sess-1", tabId: 101 });
      },
      /Missing required browser turn_id/
    );
  }

  // Test 4: rejects tab not owned by session
  {
    const { backend } = await fixture();
    await assert.rejects(
      async () => {
        await backend.getTargetMapping({
          session_id: "sess-1",
          turn_id: "turn-1",
          tabId: 102
        });
      },
      /is not part of browser session sess-1/
    );
  }

  // Test 5: reloads discarded tab and propagates error if reload fails
  {
    const { backend, calls } = await fixture({ discarded101: true });
    const result = await backend.getTargetMapping({
      session_id: "sess-1",
      turn_id: "turn-1",
      tabId: 101
    });
    assert.equal(result.tabId, 101);
    assert.equal(result.targetId, "T-101");
    assert.ok(calls.some((c) => c[0] === "tabs.reload" && c[1] === 101), "must reload discarded tab");
  }

  {
    const { backend } = await fixture({ discarded101: true, reloadFails: true });
    await assert.rejects(
      async () => {
        await backend.getTargetMapping({
          session_id: "sess-1",
          turn_id: "turn-1",
          tabId: 101
        });
      },
      /Reload network failure/
    );
  }

  console.log("All target-mapping tests passed.");
}

run().catch((err) => {
  console.error(err);
  process.exit(1);
});
