// Regression for the idle-tab -> blocked queue -> late blank-tab chain.
// Run: node --test apps/chrome-extension/tab-readiness.test.mjs
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import { test } from 'node:test';

const source = await readFile(new URL('./background.js', import.meta.url), 'utf8');
const noopEvent = { addListener() {} };
const never = () => new Promise(() => {});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
const p = (tabId = 1, session_id = 'test') => ({ session_id, turn_id: 'turn', tabId });

async function fixture(options = {}) {
  const calls = [];
  const actionCalls = [];
  const debuggerDetachListeners = [];
  const tabs = new Map([[1, { id: 1, url: 'https://example.test/', windowId: 1, groupId: -1, autoDiscardable: true }]]);
  const groups = new Map();
  let nextId = 10;
  const storage = {};
  const chrome = {
    storage: { local: {
      async get(key) { return { [key]: structuredClone(storage[key]) }; },
      async set(value) { Object.assign(storage, structuredClone(value)); }
    } },
    tabs: {
      async get(id) { if (!tabs.has(id)) throw new Error('No tab'); return { ...tabs.get(id) }; },
      async query(filter = {}) { return [...tabs.values()].filter(t => filter.groupId == null || t.groupId === filter.groupId); },
      async create(props) { calls.push(['create']); const tab = { id: nextId++, groupId: -1, ...props }; tabs.set(tab.id, tab); return { ...tab }; },
      async update(id, props) { calls.push(['update', id, props]); Object.assign(tabs.get(id), props); return { ...tabs.get(id) }; },
      async reload(id) { calls.push(['reload', id]); tabs.get(id).discarded = false; },
      async remove(id) { calls.push(['remove', id]); tabs.delete(id); },
      async group({ groupId, tabIds }) { const id = groupId ?? nextId++; groups.set(id, { id, windowId: 1 }); for (const tab of tabIds) tabs.get(tab).groupId = id; return id; },
      async sendMessage() { return { ok: true }; }
    },
    tabGroups: {
      async get(id) { if (!groups.has(id)) throw new Error('No group'); return groups.get(id); },
      async query() { return [...groups.values()]; },
      async update(id, props) { Object.assign(groups.get(id), props); },
      onRemoved: noopEvent
    },
    windows: { async getAll() { return [{ id: 1, focused: true }]; }, onRemoved: noopEvent },
    scripting: { async executeScript() { calls.push(['inject']); } },
    debugger: {
      async attach({ tabId }) { calls.push(['attach', tabId]); },
      async detach({ tabId }) { calls.push(['detach', tabId]); },
      async getTargets() { return []; },
      async sendCommand({ tabId }, method) { calls.push([method, tabId]); if (method === 'Page.setWebLifecycleState') tabs.get(tabId).frozen = false; return {}; },
      onDetach: { addListener(fn) { debuggerDetachListeners.push(fn); } }, onEvent: noopEvent
    },
    action: {
      async setIcon(details) { actionCalls.push(['setIcon', structuredClone(details)]); },
      async setTitle(details) { actionCalls.push(['setTitle', structuredClone(details)]); }
    },
    alarms: { async create() {}, onAlarm: noopEvent },
    runtime: { onInstalled: noopEvent, onMessage: noopEvent, connectNative() { return { onMessage: noopEvent, onDisconnect: noopEvent, postMessage() {} }; } },
    downloads: { onCreated: noopEvent, onChanged: noopEvent }
  };
  const context = vm.createContext({ chrome, console, setTimeout, clearTimeout, crypto: globalThis.crypto, URL });
  vm.runInContext(source + '\nglobalThis.Backend = BrowserBackend;', context);
  actionCalls.length = 0;
  debuggerDetachListeners.length = 0;
  const backend = new context.Backend({ readinessTimeoutMs: 80, queueTimeoutMs: 30, cursorTimeoutMs: 10, ...options });
  const session = await backend.store.getSession('test');
  session.tabOrigins['1'] = 'user';
  const fireDebuggerDetach = (tabId) => {
    for (const listener of debuggerDetachListeners) listener({ tabId });
  };
  return { chrome, backend, tabs, calls, actionCalls, fireDebuggerDetach, session };
}

test('toolbar icon reflects CDP attachment, not native host connectivity', async () => {
  const { backend, actionCalls, fireDebuggerDetach } = await fixture();
  assert.deepEqual(actionCalls, [], 'native transport startup must not drive the toolbar icon');

  await backend.setDirectCdpConnectionState({ connected: true });
  assert.equal(actionCalls.at(-2)?.[1]?.path?.[16], 'icons/icon-connected-16.png');
  assert.equal(actionCalls.at(-1)?.[1]?.title, 'Open Browser Use — CDP connected');

  await backend.setDirectCdpConnectionState({ connected: false });
  assert.equal(actionCalls.at(-2)?.[1]?.path?.[16], 'icons/icon-16.png');
  assert.equal(actionCalls.at(-1)?.[1]?.title, 'Open Browser Use — CDP disconnected');

  await backend.attach(p());
  assert.equal(actionCalls.at(-2)?.[0], 'setIcon');
  assert.equal(actionCalls.at(-2)?.[1]?.path?.[16], 'icons/icon-connected-16.png');
  assert.equal(actionCalls.at(-1)?.[1]?.title, 'Open Browser Use — CDP connected');

  fireDebuggerDetach(1);
  await sleep(0);
  assert.equal(actionCalls.at(-2)?.[1]?.path?.[16], 'icons/icon-16.png');
  assert.equal(actionCalls.at(-1)?.[1]?.title, 'Open Browser Use — CDP disconnected');
});

test('discarded page is reloaded in background before attach; lifecycle before focus', async () => {
  const { backend, tabs, calls } = await fixture();
  tabs.get(1).discarded = true;
  await backend.attach(p());
  assert.deepEqual(calls.map(c => c[0]), ['reload', 'attach', 'Page.setWebLifecycleState', 'Emulation.setFocusEmulationEnabled']);
  assert.ok(!calls.some(c => c[2]?.active || c[2]?.focused));
});

test('resident frozen page wakes without navigation or activation', async () => {
  const { backend, tabs, calls } = await fixture();
  tabs.get(1).frozen = true;
  await backend.attach(p());
  assert.equal(tabs.get(1).frozen, false);
  assert.equal(calls[1][0], 'Page.setWebLifecycleState');
  assert.ok(!calls.some(c => c[0] === 'reload' || c[0] === 'update'));
});

test('hung attach fails internally; unrelated operations keep working', async () => {
  const { backend, chrome, calls } = await fixture();
  chrome.debugger.attach = never;
  await assert.rejects(backend.attach(p()), /readiness.*timed out/i);
  const created = await backend.createTab(p(undefined, 'other'));
  assert.ok(created.id);
  assert.equal(calls.filter(c => c[0] === 'create').length, 1);
  const released = await backend.releaseTabs(p());
  assert.equal(released.failed.length, 1, 'pending attach must not release ownership unsafely');
  assert.deepEqual([...released.remaining], [1]);
});

test('queued create expires without later producing a ghost tab', async () => {
  const { backend, chrome, calls } = await fixture();
  const gate = deferred();
  chrome.debugger.attach = () => gate.promise;
  const attached = backend.attach(p());
  const created = backend.createTab(p(undefined, 'other'));
  await assert.rejects(created, /expired.*queue/i);
  gate.resolve();
  await attached;
  await sleep(5);
  assert.equal(calls.filter(c => c[0] === 'create').length, 0);
});

test('late attach is detached, never woken or continued after timeout', async () => {
  const { backend, chrome, calls } = await fixture();
  const gate = deferred();
  chrome.debugger.attach = () => gate.promise;
  await assert.rejects(backend.attach(p()), /readiness.*timed out/i);
  await assert.rejects(backend.attach(p()), /pending/i);
  gate.resolve();
  await sleep(10);
  assert.deepEqual(calls.map(c => c[0]), ['detach']);
  const report = await backend.releaseTabs(p());
  assert.equal(report.failed.length, 0);
  assert.equal(report.remaining.length, 0);
});

test('hung lifecycle never blocks the following focus wake or global queue forever', async () => {
  const { backend, chrome, calls } = await fixture();
  const gate = deferred();
  chrome.debugger.sendCommand = async (_, method) => {
    calls.push([method]);
    if (method === 'Page.setWebLifecycleState') await gate.promise;
    return {};
  };
  await assert.rejects(backend.attach(p()), /readiness.*timed out/i);
  gate.resolve();
  await sleep(10);
  assert.ok(!calls.some(c => c[0] === 'Emulation.setFocusEmulationEnabled'));
  assert.ok(calls.some(c => c[0] === 'detach'));
  assert.equal((await backend.releaseTabs(p())).failed.length, 0);
});

test('claim cursor injection is best-effort and bounded', async () => {
  const { backend, chrome } = await fixture();
  chrome.tabs.sendMessage = async () => { throw new Error('no receiver'); };
  chrome.scripting.executeScript = never;
  await backend.claimUserTab(p());
  assert.equal((await backend.releaseTabs(p())).remaining.length, 0);
});

test('local caller deadline rejects create even before the default queue expiry', async () => {
  const { backend, chrome, calls } = await fixture({ queueTimeoutMs: 100 });
  const gate = deferred();
  chrome.debugger.attach = () => gate.promise;
  const attached = backend.attach(p());
  await assert.rejects(backend.createTab({ ...p(), _deadlineUnixMs: Date.now() + 15 }), /expired.*queue/i);
  gate.resolve();
  await attached;
  assert.ok(!calls.some(c => c[0] === 'create'));
  await assert.rejects(backend.createTab({ ...p(), _deadlineUnixMs: Date.now() - 1 }), /expired.*queue/i);
});

test('external debugger conflict is not silently adopted', async () => {
  const { backend, chrome } = await fixture();
  chrome.debugger.attach = async () => { throw new Error('Another debugger is already attached'); };
  chrome.debugger.sendCommand = async () => { throw new Error('Debugger unattached'); };
  await assert.rejects(backend.attach(p()), /unattached/);
  assert.equal(backend.attachedTabs.has(1), false);
  assert.equal(backend.pendingReadiness.size, 0);
});

test('MV3 existing own attachment is verified before reuse', async () => {
  const { backend, chrome } = await fixture();
  chrome.debugger.attach = async () => { throw new Error('Another debugger is already attached'); };
  await backend.attach(p());
  assert.equal(backend.attachedTabs.has(1), true);
});

test('already attached frozen page is re-woken without reloading', async () => {
  const { backend, tabs, calls } = await fixture();
  await backend.attach(p());
  tabs.get(1).frozen = true;
  await backend.attach(p());
  assert.equal(tabs.get(1).frozen, false);
  assert.equal(calls.filter(c => c[0] === 'attach').length, 1);
  assert.equal(calls.filter(c => c[0] === 'Page.setWebLifecycleState').length, 2);
});

test('release of closed tab clears pending readiness; late attach cannot revive ownership', async () => {
  const { backend, chrome, tabs } = await fixture();
  const gate = deferred();
  chrome.debugger.attach = () => gate.promise;
  await assert.rejects(backend.attach(p()), /readiness.*timed out/i);
  tabs.delete(1);
  assert.equal((await backend.releaseTabs(p())).remaining.length, 0);
  gate.resolve();
  await sleep(5);
  assert.equal(await backend.store.findTabOwner(1), null);
  assert.equal(backend.attachedTabs.has(1), false);
});

test('long caller deadline does not allow a queued op to start after queue TTL', async () => {
  const { backend, chrome, calls } = await fixture({ queueTimeoutMs: 30 });
  const gate = deferred();
  chrome.debugger.attach = () => gate.promise;
  const attached = backend.attach(p());
  // Caller provides a long 10s deadline, but queueTimeoutMs is 30ms.
  const created = backend.createTab({ ...p(undefined, 'other'), _deadlineUnixMs: Date.now() + 10_000 });
  await assert.rejects(created, /expired.*queue/i);
  gate.resolve();
  await attached;
  await sleep(10);
  // Even though caller deadline was 10s, queue TTL (30ms) prevented create from executing!
  assert.ok(!calls.some(c => c[0] === 'create'));
});

test('markDeliverable organizes one result and preserves another agent-owned blank tab', async () => {
  const { backend, tabs, session } = await fixture();
  // Tab 1 is real result tab.
  // Tab 2 is another agent-created blank tab (e.g. intentionally opened for upcoming work).
  const agentBlank = { id: 2, url: 'about:blank', windowId: 1, groupId: 55 };
  tabs.set(2, agentBlank);
  session.tabOrigins['2'] = 'agent';
  session.chromeGroupId = 55;
  tabs.get(1).groupId = 55;

  await backend.markDeliverable(p(1));

  // Deliverable tab 1 moved out of task group 55.
  // Agent blank tab 2 MUST BE PRESERVED according to tab ownership contract!
  assert.ok(tabs.has(2), 'agent blank tab must not be guessed or swept');
  assert.equal(session.tabOrigins['2'], 'agent');
  assert.equal(session.chromeGroupId, 55, 'task group retained with remaining agent tab');
});

test('markDeliverable preserves user blank tab and does not dissolve task group if user tab remains', async () => {
  const { backend, chrome, tabs, session } = await fixture();
  // Tab 1 is real result tab.
  // Tab 3 is a user-owned blank tab (tabOrigins === 'user').
  const userBlank = { id: 3, url: 'about:blank', windowId: 1, groupId: 55 };
  tabs.set(3, userBlank);
  session.tabOrigins['3'] = 'user';
  session.chromeGroupId = 55;
  tabs.get(1).groupId = 55;

  await backend.markDeliverable(p(1));

  // User blank tab 3 MUST NOT be removed!
  assert.ok(tabs.has(3), 'user blank tab must be preserved');
  assert.equal(session.chromeGroupId, 55, 'task group retained with user tab');
});

