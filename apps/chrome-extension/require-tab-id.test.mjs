// Node test for requireTabId tab-id tolerance (the "claim_tab(\"279925170\")"
// -> "claimUserTab requires an integer tabId" fix).
// Run: node apps/chrome-extension/require-tab-id.test.mjs
//
// Loads background.js into a sandbox with stubbed chrome.* APIs and exercises
// the module-level requireTabId gate directly: numeric strings ("42", " 42 ")
// must coerce to ints so heredoc scripts / SDKs that round-trip tab ids
// through JSON keep working; everything else must still throw with a message
// that shows the received value.

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import vm from "node:vm";
import assert from "node:assert/strict";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BG_PATH = path.join(HERE, "background.js");

function chromeStub() {
  const listeners = () => ({ addListener() {} });
  return {
    storage: {
      local: { get: async () => ({}), set: async () => {} }
    },
    tabs: {
      create: async () => ({ id: 1, url: "about:blank" }),
      get: async () => ({}),
      query: async () => [],
      remove: async () => {},
      update: async () => ({}),
      group: async () => 1,
      ungroup: async () => {},
      onRemoved: listeners()
    },
    tabGroups: { get: async () => ({}), query: async () => [], update: async () => ({}), onRemoved: listeners() },
    windows: { create: async () => ({ tabs: [{ id: 1 }] }), getAll: async () => [], onRemoved: listeners() },
    debugger: { attach: async () => {}, detach: async () => {}, sendCommand: async () => ({}), getTargets: async () => [], onEvent: listeners(), onDetach: listeners() },
    runtime: {
      onInstalled: listeners(),
      onStartup: listeners(),
      onSuspend: listeners(),
      onConnectExternal: listeners(),
      onMessage: listeners(),
      connectNative: () => ({ onMessage: listeners(), onDisconnect: listeners(), postMessage() {}, disconnect() {} })
    },
    alarms: { create: async () => {}, clear: async () => {}, onAlarm: listeners() },
    downloads: { onCreated: listeners(), onChanged: listeners(), onDeterminingFilename: listeners() },
    notifications: { create() {}, onClicked: listeners() },
    history: { search: async () => [] }
  };
}

async function loadRequireTabId() {
  const bgSource = await readFile(BG_PATH, "utf8");
  const inject = `globalThis.__obu_export = (name, value) => { globalThis[name] = value; };`;
  const exporter = `\n__obu_export("requireTabId", requireTabId);`;
  const context = vm.createContext({
    chrome: chromeStub(),
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
  vm.runInContext(inject, context);
  vm.runInContext(
    "chrome.runtime.connectNative = () => ({ onMessage: { addListener(){} }, onDisconnect: { addListener(){} }, postMessage(){}, disconnect(){} });",
    context
  );
  vm.runInContext(bgSource + exporter, context);
  return context.requireTabId;
}

function throwsWith(fn, needle) {
  let message = "";
  try {
    fn();
  } catch (error) {
    message = String(error.message);
  }
  assert.ok(message, "expected requireTabId to throw");
  if (needle) assert.ok(message.includes(needle), `expected message to include ${JSON.stringify(needle)}, got: ${message}`);
}

async function run() {
  const requireTabId = await loadRequireTabId();

  assert.equal(requireTabId({ tabId: 42 }, "claimUserTab"), 42, "plain int passes through");
  assert.equal(requireTabId({ tabId: "42" }, "claimUserTab"), 42, "numeric string coerces");
  assert.equal(requireTabId({ tabId: " 42 " }, "claimUserTab"), 42, "padded numeric string coerces");
  assert.equal(requireTabId({ tabId: "0007" }, "executeCdp"), 7, "leading-zero numeric string coerces");
  assert.equal(requireTabId({ tabId: 0 }, "attach"), 0, "zero is a valid (if unusual) chrome tab id");

  throwsWith(() => requireTabId({ tabId: "abc" }, "claimUserTab"), "claimUserTab requires an integer tabId");
  throwsWith(() => requireTabId({ tabId: "" }, "attach"), "attach requires an integer tabId");
  throwsWith(() => requireTabId({ tabId: "1e3" }, "executeCdp"), "executeCdp requires an integer tabId");
  throwsWith(() => requireTabId({ tabId: "0x10" }, "claimUserTab"), "claimUserTab requires an integer tabId");
  throwsWith(() => requireTabId({ tabId: 42.5 }, "claimUserTab"), "claimUserTab requires an integer tabId");
  throwsWith(() => requireTabId({ tabId: true }, "claimUserTab"), "claimUserTab requires an integer tabId");
  throwsWith(() => requireTabId({ tabId: null }, "claimUserTab"), "claimUserTab requires an integer tabId");
  // error message surfaces the offending value
  throwsWith(() => requireTabId({ tabId: "nope" }, "finalizeTabs"), "got \"nope\"");

  console.log("All requireTabId tests passed.");
}

run().catch((err) => {
  console.error(err);
  process.exit(1);
});