const NATIVE_HOST_NAME = "com.ifuryst.open_browser_use.extension";
const NATIVE_HOST_STATUS_KEY = "OPEN_BROWSER_USE_NATIVE_HOST_STATUS";
const SESSION_STATE_KEY = "OPEN_BROWSER_USE_SESSION_STATE";
const RECONNECT_ALARM_NAME = "open-browser-use-native-reconnect";
const HEARTBEAT_ALARM_NAME = "open-browser-use-heartbeat";
const DEFAULT_CDP_TIMEOUT_MS = 10_000;
const ACTION_ICON_DEFAULT = {
  16: "icons/icon-16.png",
  32: "icons/icon-32.png"
};
const ACTION_ICON_CDP_CONNECTED = {
  16: "icons/icon-connected-16.png",
  32: "icons/icon-connected-32.png"
};
// Stay below the CLI's default 10s request deadline, including queue time.
const TAB_READINESS_TIMEOUT_MS = 6_000;
const OWNERSHIP_QUEUE_TIMEOUT_MS = 2_000;
const CURSOR_SETUP_TIMEOUT_MS = 300;
const CURSOR_ARRIVAL_TIMEOUT_MS = 1_000;
const MAX_USER_TABS = 1000;
// Isolated-window fallback bounds, used only when the screen size cannot be
// read. When it can, the window is sized near-fullscreen (normal size): a
// small corner popup is bad for agents because SPA virtual-scroll lists and
// IntersectionObserver-driven renderers mount fewer items in a small viewport
// (x.com session: DOM never showed more than ~4 posts).
const ISOLATED_WINDOW_WIDTH = 1280;
const ISOLATED_WINDOW_HEIGHT = 900;
const ISOLATED_WINDOW_MARGIN = 24;
const DEFAULT_SESSION_GROUP_TITLE = "Task - OBU";
const DELIVERABLE_GROUP_TITLE = "✅ Open Browser Use";
const LEGACY_SESSION_GROUP_TITLE_PATTERN = /^Open Browser Use [0-9a-f]{8}$/i;

class JsonRpcPeer {
  constructor(transport, handlers) {
    this.transport = transport;
    this.handlers = handlers;
    this.transport.setMessageCallback((message) => {
      void this.handleMessage(message);
    });
  }

  async handleMessage(message) {
    if (!message || typeof message !== "object" || typeof message.method !== "string") {
      return;
    }
    const id = message.id;
    try {
      const handler = this.handlers[message.method];
      if (typeof handler !== "function") {
        throw new Error(`No handler registered for method: ${message.method}`);
      }
      const result = await handler.call(this.handlers, message.params ?? {});
      if (id !== undefined) {
        this.transport.sendMessage({ jsonrpc: "2.0", id, result: result ?? {} });
      }
    } catch (error) {
      if (id !== undefined) {
        this.transport.sendMessage({
          jsonrpc: "2.0",
          id,
          error: {
            code: -32000,
            message: error instanceof Error ? error.message : String(error),
            ...(error?.data ? { data: error.data } : {})
          }
        });
      }
    }
  }

  sendNotification(method, params) {
    this.transport.sendMessage({ jsonrpc: "2.0", method, params });
  }
}

class NativeTransport {
  constructor(hostName) {
    this.hostName = hostName;
    this.port = null;
    this.messageCallback = null;
    this.disconnectCallback = null;
    this.reconnectAttempt = 0;
    this.status = {
      hostName: this.hostName,
      lastChecked: Date.now(),
      reconnectAttempt: this.reconnectAttempt,
      state: "disconnected"
    };
    this.connect();
    chrome.alarms.onAlarm.addListener((alarm) => {
      if (alarm.name === RECONNECT_ALARM_NAME && !this.port) {
        this.connect();
      }
    });
  }

  setMessageCallback(callback) {
    this.messageCallback = callback;
  }

  setDisconnectCallback(callback) {
    this.disconnectCallback = callback;
  }

  sendMessage(message) {
    if (!this.port) {
      this.scheduleReconnect("native host disconnected");
      throw new Error("Native host is disconnected");
    }
    this.port.postMessage(message);
  }

  connect() {
    if (this.port) {
      return;
    }
    try {
      const port = chrome.runtime.connectNative(this.hostName);
      this.port = port;
      this.reconnectAttempt = 0;
      this.setStatus({ state: "connected" });
      port.onMessage.addListener((message) => {
        this.messageCallback?.(message);
      });
      port.onDisconnect.addListener(() => {
        this.port = null;
        this.disconnectCallback?.();
        this.scheduleReconnect(chrome.runtime.lastError?.message ?? "native host disconnected");
      });
    } catch (error) {
      this.port = null;
      this.scheduleReconnect(error instanceof Error ? error.message : String(error));
    }
  }

  scheduleReconnect(error) {
    this.reconnectAttempt += 1;
    this.setStatus({
      state: "reconnecting",
      error,
      nextRetryMs: 5000,
      reconnectAttempt: this.reconnectAttempt
    });
    chrome.alarms.create(RECONNECT_ALARM_NAME, { periodInMinutes: 0.1 }).catch(() => {});
  }

  setStatus(status) {
    this.status = {
      hostName: this.hostName,
      lastChecked: Date.now(),
      reconnectAttempt: this.reconnectAttempt,
      ...status
    };
    chrome.storage.local
      .set({
        [NATIVE_HOST_STATUS_KEY]: this.status
      })
      .catch(() => {});
  }

  getStatus() {
    return this.status;
  }
}

class SessionStore {
  constructor() {
    this.state = { sessions: {}, deliverableGroupId: null };
    this.ready = this.load();
  }

  async load() {
    const stored = await chrome.storage.local.get(SESSION_STATE_KEY);
    const value = stored[SESSION_STATE_KEY];
    if (value && typeof value === "object") {
      this.state = {
        sessions: value.sessions && typeof value.sessions === "object" ? value.sessions : {},
        deliverableGroupId:
          typeof value.deliverableGroupId === "number" ? value.deliverableGroupId : null
      };
      let migrated = false;
      for (const session of Object.values(this.state.sessions)) {
        if (
          session &&
          typeof session === "object" &&
          typeof session.title === "string" &&
          LEGACY_SESSION_GROUP_TITLE_PATTERN.test(session.title)
        ) {
          session.title = DEFAULT_SESSION_GROUP_TITLE;
          migrated = true;
        }
      }
      if (migrated) {
        await this.save();
      }
      if (typeof this.state.deliverableGroupId === "number") {
        await chrome.tabGroups
          .update(this.state.deliverableGroupId, { title: DELIVERABLE_GROUP_TITLE })
          .catch(() => {});
      }
    }
  }

  async save() {
    await chrome.storage.local.set({ [SESSION_STATE_KEY]: this.state });
  }

  async getSession(sessionId) {
    await this.ready;
    const existing = this.state.sessions[sessionId];
    if (existing && typeof existing === "object") {
      return existing;
    }
    const created = {
      chromeGroupId: null,
      title: DEFAULT_SESSION_GROUP_TITLE,
      activeTabId: null,
      tabOrigins: {},
      windowId: null
    };
    this.state.sessions[sessionId] = created;
    await this.save();
    return created;
  }

  async removeSession(sessionId) {
    await this.ready;
    delete this.state.sessions[sessionId];
    await this.save();
  }

  async findTabOwner(tabId) {
    await this.ready;
    for (const [sessionId, session] of Object.entries(this.state.sessions)) {
      if (Object.hasOwn(session.tabOrigins ?? {}, String(tabId))) {
        return { sessionId, session };
      }
    }
    return null;
  }

  async findSessionByGroup(groupId) {
    await this.ready;
    for (const [sessionId, session] of Object.entries(this.state.sessions)) {
      if (session.chromeGroupId === groupId) {
        return { sessionId, session };
      }
    }
    return null;
  }
}

class BrowserBackend {
  constructor({ readinessTimeoutMs = TAB_READINESS_TIMEOUT_MS, queueTimeoutMs = OWNERSHIP_QUEUE_TIMEOUT_MS, cursorTimeoutMs = CURSOR_SETUP_TIMEOUT_MS } = {}) {
    this.store = new SessionStore();
    this.readinessTimeoutMs = readinessTimeoutMs;
    this.queueTimeoutMs = queueTimeoutMs;
    this.cursorTimeoutMs = cursorTimeoutMs;
    // Chrome promises cannot be cancelled. A timed-out readiness operation
    // remains quarantined until its late attach/wake has been detached.
    this.pendingReadiness = new Map();
    this.attachedTabs = new Set();
    this.directCdpConnected = false;
    // Session tabs that have the CDP-level background wake applied (focus
    // emulation + lifecycle "active"), for both isolated-window and user-window
    // sessions. Tracked by tabId because navigation events only carry a tabId,
    // not a sessionId, and the wake must be re-applied there when navigation
    // resets the lifecycle state.
    this.wokenTabIds = new Set();
    this.activeTabsBySession = new Map();
    this.cursorByTabId = new Map();
    this.downloadFilenamesById = new Map();
    this.downloadUrlsById = new Map();
    this.downloadsById = new Map();
    this.downloadWaiters = new Set();
    this.downloadChangeListeners = new Set();
    this.cursorArrivalWaitersByKey = new Map();
    this.fileChoosersById = new Map();
    this.fileChooserWaitersByTabId = new Map();
    this.nextCursorMoveSequence = 1;
    // Chrome APIs yield: serialize ownership transitions so two claimers cannot
    // both pass the owner check. Long-running event waits are not in this queue.
    let ownershipQueue = Promise.resolve();
    for (const name of ["createTab", "claimUserTab", "releaseTabs", "markDeliverable", "closeUserTab", "finalizeTabs", "attach", "detach", "executeCdp", "setFileChooserFiles", "getTargetMapping"]) {
      const operation = this[name].bind(this);
      this[name] = (params) => {
        let started = false;
        let expired = false;
        const deadline = Number.isFinite(params?._deadlineUnixMs) ? params._deadlineUnixMs : Infinity;
        const queued = ownershipQueue.then(() => {
          if (expired || Date.now() >= deadline) throw new Error(`${name} expired in ownership queue; no action was started`);
          started = true;
          clearTimeout(timer);
          return operation(params);
        });
        let timer;
        const remainingCallerDeadline = Number.isFinite(deadline) ? Math.max(0, deadline - Date.now()) : Infinity;
        const maxWaitMs = Math.min(this.queueTimeoutMs, remainingCallerDeadline);
        const expiry = new Promise((_, reject) => {
          timer = setTimeout(() => {
            if (started) return;
            expired = true;
            reject(new Error(`${name} expired in ownership queue; no action was started`));
          }, maxWaitMs);
        });
        ownershipQueue = queued.catch(() => {});
        return Promise.race([queued, expiry]).finally(() => clearTimeout(timer));
      };
    }
    chrome.debugger.onDetach.addListener((source) => {
      if (typeof source.tabId === "number") {
        this.attachedTabs.delete(source.tabId);
        this.wokenTabIds.delete(source.tabId);
        void this.updateCdpActionState();
      }
    });
  }

  async updateCdpActionState() {
    const action = chrome.action;
    if (!action) return;
    const connected = this.directCdpConnected || this.attachedTabs.size > 0;
    await Promise.allSettled([
      action.setIcon?.({ path: connected ? ACTION_ICON_CDP_CONNECTED : ACTION_ICON_DEFAULT }),
      action.setTitle?.({
        title: connected
          ? "Open Browser Use — CDP connected"
          : "Open Browser Use — CDP disconnected"
      })
    ]);
  }

  async setDirectCdpConnectionState(params) {
    this.directCdpConnected = params?.connected === true;
    await this.updateCdpActionState();
    return { ok: true };
  }

  ping() {
    return "pong";
  }

  async getInfo() {
    let { extensionInstanceId } = await chrome.storage.local.get("extensionInstanceId");
    if (typeof extensionInstanceId !== "string") {
      extensionInstanceId = crypto.randomUUID();
      await chrome.storage.local.set({ extensionInstanceId });
    }
    return {
      name: "Open Browser Use Chrome",
      version: chrome.runtime.getManifest().version,
      type: "extension",
      metadata: {
        extensionId: chrome.runtime.id,
        extensionInstanceId,
        nativeHostName: NATIVE_HOST_NAME,
        tabReadiness: "bounded-v1"
      }
    };
  }

  async createTab(params) {
    const session = await this.requireSession(params);
    const sessionState = await this.store.getSession(session.sessionId);
    let chromeTab;
    if (await this.sessionUsesIsolatedWindow(sessionState, params)) {
      const { win, created } = await this.ensureSessionWindow(sessionState);
      if (created) {
        // The window was just created and comes with its own about:blank start
        // tab (URLs aside, windows always open with one tab). Reuse it as the
        // session's first tab instead of adding a second, otherwise an untracked
        // blank tab would linger and keep the owned window open at finalize.
        chromeTab = (win.tabs ?? []).find(hasTabId) ?? null;
      }
      if (!chromeTab) {
        chromeTab = await chrome.tabs.create({ windowId: win.id, active: false, url: "about:blank" });
        if (!hasTabId(chromeTab)) {
          throw new Error("Created session window tab has no id");
        }
      }
    } else {
      chromeTab = await createBackgroundTab();
    }
    await this.ensureSessionGroup(session.sessionId, chromeTab.id, "agent");
    await this.setSessionActiveTab(session.sessionId, chromeTab.id);
    return { ...toBrowserTab(chromeTab), active: true };
  }

  // Isolated window model: a session that isolates owns exactly one Chrome
  // window. Once session.windowId is set the session stays isolated regardless
  // of later params (single-window invariant: tab groups cannot span windows).
  // window_mode values: unset/"user" = shared user window (default), "isolated"
  // = dedicated background window (opt-in via OBU_WINDOW_MODE).
  async sessionUsesIsolatedWindow(sessionState, params) {
    if (typeof sessionState.windowId === "number") {
      return true;
    }
    return params.window_mode === "isolated";
  }

  // Get-or-create the session's isolated window; never steals focus and stays
  // out of the user's way (normal-sized, unfocused, non-minimized by default).
  // Returns { win, created }: created is true when this call built the window
  // (its start tab may then be reused by the caller).
  async ensureSessionWindow(sessionState) {
    if (typeof sessionState.windowId === "number") {
      const win = await chrome.windows.get(sessionState.windowId).catch(() => null);
      if (win && typeof win.id === "number") {
        return { win, created: false };
      }
    }
    const bounds = isolatedWindowBounds();
    const createData = {
      focused: false,
      type: "normal",
      url: "about:blank",
      width: bounds.width,
      height: bounds.height
    };
    if (bounds.left !== null) {
      createData.left = bounds.left;
    }
    if (bounds.top !== null) {
      createData.top = bounds.top;
    }
    const win = await chrome.windows.create(createData);
    if (!win || typeof win.id !== "number") {
      throw new Error("Created Chrome window has no id");
    }
    sessionState.windowId = win.id;
    // Track only the initial tab we actually created, never all future tabs
    // that happen to share this window.
    for (const tab of win.tabs ?? []) {
      if (hasTabId(tab)) sessionState.tabOrigins[String(tab.id)] = "agent";
    }
    await this.store.save();
    return { win, created: true };
  }

  // Move an existing tab into the session's isolated window (claims) so the
  // session's tab group stays in one window.
  async moveTabIntoSessionWindow(sessionState, tab) {
    const { win, created } = await this.ensureSessionWindow(sessionState);
    if (tab.windowId !== win.id) {
      await chrome.tabs.move(tab.id, { windowId: win.id, index: -1 });
    }
    if (created) {
      // A claim needs the incoming page, not windows.create's startup blank.
      // Remove only the exact tabs returned by our creation call, never pages
      // discovered by URL/group membership (a user's blank tab is not ours).
      for (const initial of win.tabs ?? []) {
        if (!hasTabId(initial) || initial.id === tab.id) continue;
        const current = await chrome.tabs.get(initial.id).catch(() => null);
        if (current && current.url === "about:blank" && !current.pendingUrl) {
          await chrome.tabs.remove(initial.id);
          delete sessionState.tabOrigins[String(initial.id)];
        }
      }
      await this.store.save();
    }
    return win;
  }

  // A live user-facing window for deliverables kept at finalize: focused first,
  // then any normal window, excluding the session's own isolated window.
  async chooseUserWindowId(excludeWindowId = null) {
    const windows = await chrome.windows.getAll({ windowTypes: ["normal"] });
    const candidates = windows.filter(
      (win) => typeof win.id === "number" && win.id !== excludeWindowId
    );
    const focused = candidates.find((win) => win.focused);
    return focused?.id ?? candidates[0]?.id ?? null;
  }

  async getTabs(params) {
    const session = await this.requireSession(params);
    const tabs = await this.getSessionTabs(session.sessionId);
    return await this.withLogicalActive(session.sessionId, tabs.map(toBrowserTab));
  }

  async getUserTabs(params) {
    await this.requireSession(params);
    const tabs = (await chrome.tabs.query({}))
      .filter(hasTabId)
      .sort(compareLastAccessed)
      .slice(0, MAX_USER_TABS);
    const groupTitles = await readGroupTitles(tabs);
    return await Promise.all(tabs.map(async (tab) => {
      const owner = await this.store.findTabOwner(tab.id);
      return { ...toUserTab(tab, groupTitles), ownerSessionId: owner?.sessionId ?? null };
    }));
  }

  async getUserHistory(params) {
    await this.requireSession(params);
    const query = typeof params.query === "string" ? params.query : "";
    const maxResults =
      Number.isInteger(params.limit) && params.limit > 0 ? params.limit : 100;
    const search = { text: query, maxResults };
    if (typeof params.from === "string") {
      search.startTime = parseDate(params.from, "from");
    }
    if (typeof params.to === "string") {
      search.endTime = parseDate(params.to, "to");
    }
    const results = await chrome.history.search(search);
    return results.flatMap((item) => {
      if (typeof item.url !== "string" || typeof item.lastVisitTime !== "number") {
        return [];
      }
      return [
        {
          url: item.url,
          ...(item.title ? { title: item.title } : {}),
          dateVisited: new Date(item.lastVisitTime).toISOString()
        }
      ];
    });
  }

  async claimUserTab(params) {
    const session = await this.requireSession(params);
    const tabId = requireTabId(params, "claimUserTab");
    this.requireReadinessSettled(tabId);
    const tab = await chrome.tabs.get(tabId);
    if (!hasTabId(tab)) {
      throw new Error(`Chrome tab ${tabId} has no id`);
    }
    if (tab.url?.startsWith("chrome://")) {
      throw new Error(`Chrome internal tab ${tabId} cannot be claimed`);
    }
    const owner = await this.store.findTabOwner(tabId);
    if (owner && owner.sessionId !== session.sessionId) {
      const error = new Error(`Tab ${tabId} is owned by browser session ${owner.sessionId}; ask the owning task to release_tabs() before claiming. Do not share its session ID to bypass ownership.`);
      error.data = { reason: "tab_owned", tabId, ownerSessionId: owner.sessionId };
      throw error;
    }
    if (owner) {
      await this.setSessionActiveTab(session.sessionId, tabId);
      return { ...toBrowserTab(tab), active: true };
    }
    const sessionState = await this.store.getSession(session.sessionId);
    if (await this.sessionUsesIsolatedWindow(sessionState, params)) {
      await this.moveTabIntoSessionWindow(sessionState, tab);
    }
    // Never downgrade a session's own work tab on re-claim: an agent-created
    // tab re-claimed by the same session must stay "agent" so finalize closes
    // it, instead of flipping to "user" and leaking the tab and its window.
    const origin =
      sessionState.tabOrigins[String(tab.id)] === "agent" ? "agent" : "user";
    await this.ensureSessionGroup(session.sessionId, tab.id, origin);
    await this.setSessionActiveTab(session.sessionId, tab.id);
    return { ...toBrowserTab(tab), active: true };
  }

  async forgetOwnedTab(sessionId, tabId) {
    const state = await this.store.getSession(sessionId);
    const previous = { ...state.tabOrigins };
    const active = state.activeTabId;
    delete state.tabOrigins[String(tabId)];
    if (active === tabId) state.activeTabId = null;
    try {
      await this.store.save();
    } catch (error) {
      state.tabOrigins = previous;
      state.activeTabId = active;
      throw error;
    }
    if (this.activeTabsBySession.get(sessionId) === tabId) this.activeTabsBySession.delete(sessionId);
    this.cursorByTabId.delete(tabId);
    this.wokenTabIds.delete(tabId);
    this.attachedTabs.delete(tabId);
    this.fileChooserWaitersByTabId.get(tabId)?.reject(new Error("Tab ownership released"));
    for (const [id, chooser] of this.fileChoosersById) {
      if (chooser.tabId === tabId) this.fileChoosersById.delete(id);
    }
  }

  async releaseTabs(params) {
    const { sessionId } = await this.requireSession(params);
    const state = await this.store.getSession(sessionId);
    if (params.tabIds !== undefined && !Array.isArray(params.tabIds)) {
      throw new Error("releaseTabs tabIds must be an array; omit to release all owned tabs");
    }
    const ids = params.tabIds === undefined
      ? Object.keys(state.tabOrigins).map(Number)
      : params.tabIds.map((tabId) => requireTabId({ tabId }, "releaseTabs"));
    const report = { released: [], alreadyReleased: [], failed: [], remaining: [] };
    for (const tabId of new Set(ids)) {
      try {
        const owner = await this.store.findTabOwner(tabId);
        if (owner && owner.sessionId !== sessionId) throw new Error(`Tab ${tabId} is owned by browser session ${owner.sessionId}`);
        if (!owner) {
          report.alreadyReleased.push(tabId);
          continue;
        }
        // An absent tab needs only stale tracking cleanup. A detach failure on
        // a live attached tab retains ownership and is reported, never hidden.
        const tab = await chrome.tabs.get(tabId).catch(() => null);
        if (tab) {
          this.requireReadinessSettled(tabId);
          // MV3 restart loses the in-memory attached set, not necessarily the
          // Chrome debugger attachment. Probe it before reporting release.
          const targets = chrome.debugger.getTargets ? await chrome.debugger.getTargets() : [];
          if (this.attachedTabs.has(tabId) || targets.some(t => t.tabId === tabId && t.attached)) {
            await this.detachTab(tabId);
          }
        }
        await this.forgetOwnedTab(sessionId, tabId);
        report.released.push(tabId);
      } catch (error) {
        report.failed.push({ tabId, error: String(error?.message ?? error) });
      }
    }
    report.remaining = Object.keys(state.tabOrigins).map(Number);
    return report;
  }

  async markDeliverable(params) {
    const { sessionId } = await this.requireSession(params);
    await this.requireSessionTab(params, "markDeliverable");
    const tabId = requireTabId(params, "markDeliverable");
    const state = await this.store.getSession(sessionId);
    const tab = await chrome.tabs.get(tabId);
    if (typeof state.windowId === "number" && tab.windowId === state.windowId) {
      const userWindowId = await this.chooseUserWindowId(state.windowId);
      if (typeof userWindowId === "number") {
        await chrome.tabs.move(tabId, { windowId: userWindowId, index: -1 });
      }
    }
    await this.moveToDeliverables([tabId]);
    // Ownership is unchanged, even though the tab is now in another group.
    return { tabId, status: "deliverable", ownerSessionId: sessionId };
  }

  // Deprecated wire compatibility only. New callers use explicit close,
  // markDeliverable and releaseTabs; legacy handoff semantics are unchanged.
  async finalizeTabs(params) {
    const session = await this.requireSession(params);
    if (!Array.isArray(params.keep)) {
      throw new Error("finalizeTabs requires keep array of {tabId: number, status: \"handoff\" | \"deliverable\"}");
    }
    const tabs = await this.getSessionTabs(session.sessionId);
    for (const tab of tabs) this.requireReadinessSettled(tab.id);
    const knownTabIds = new Set(tabs.filter(hasTabId).map((tab) => tab.id));
    const keep = new Map();
    for (const entry of params.keep) {
      if (!entry || typeof entry !== "object") {
        throw new Error("finalizeTabs received invalid tab entry; expected {tabId: number, status: \"handoff\" | \"deliverable\"}");
      }
      const tabId = requireTabId(entry, "finalizeTabs");
      if (!knownTabIds.has(tabId)) {
        throw new Error(`finalizeTabs cannot keep unknown tab ${tabId}`);
      }
      if (keep.has(tabId)) {
        throw new Error(`finalizeTabs received duplicate tab ${tabId}`);
      }
      const status = entry.status;
      if (status !== "handoff" && status !== "deliverable") {
        throw new Error(`finalizeTabs received invalid status ${JSON.stringify(status)}; expected "handoff" or "deliverable"`);
      }
      keep.set(tabId, status);
    }
    const sessionState = await this.store.getSession(session.sessionId);
    const agentTabsToClose = [];
    const userTabsToRelease = [];
    const deliverableTabs = [];
    const handoffTabs = [];
    for (const tab of tabs) {
      if (!hasTabId(tab)) {
        continue;
      }
      const keptStatus = keep.get(tab.id);
      if (keptStatus === "handoff") {
        handoffTabs.push(tab.id);
        continue;
      }
      if (keptStatus === "deliverable") {
        deliverableTabs.push(tab.id);
        continue;
      }
      // In the session's own window, an untracked tab is always ours (created
      // by us, e.g. the initial about:blank of a claim-created window) — only
      // tabs explicitly tracked as "user" are the user's and must be released.
      const isAgent = sessionState.tabOrigins[String(tab.id)] !== "user";
      if (isAgent) {
        agentTabsToClose.push(tab.id);
      } else {
        userTabsToRelease.push(tab.id);
      }
    }
    // Detach every disposition, including handoff tabs we keep alive: leaving a
    // tab attached keeps Chrome's "started debugging this browser" banner up
    // after the turn ends. The debugger re-attaches lazily on the next CDP call
    // (the CLI runner and background handlers attach on demand), so dropping it
    // here is loss-free while clearing the banner.
    await this.detachMany([
      ...agentTabsToClose,
      ...userTabsToRelease,
      ...deliverableTabs,
      ...handoffTabs
    ]);
    if (agentTabsToClose.length > 0) {
      await chrome.tabs.remove(agentTabsToClose.length === 1 ? agentTabsToClose[0] : agentTabsToClose);
    }
    if (userTabsToRelease.length > 0 && chrome.tabs.ungroup) {
      await chrome.tabs.ungroup(userTabsToRelease.length === 1 ? userTabsToRelease[0] : userTabsToRelease);
    }
    const sessionWindowId = typeof sessionState.windowId === "number" ? sessionState.windowId : null;
    if (deliverableTabs.length > 0 && typeof sessionWindowId === "number") {
      const sessionWindowTabIds = (
        await chrome.tabs.query({ windowId: sessionWindowId }).catch(() => [])
      )
        .map((tab) => tab.id)
        .filter((id) => typeof id === "number");
      const inSessionWindow = deliverableTabs.filter((id) => sessionWindowTabIds.includes(id));
      if (inSessionWindow.length > 0) {
        const userWindowId = await this.chooseUserWindowId(sessionWindowId);
        if (typeof userWindowId === "number") {
          await chrome.tabs
            .move(inSessionWindow, { windowId: userWindowId, index: -1 })
            .catch(() => {});
        }
      }
    }
    if (deliverableTabs.length > 0) {
      await this.moveToDeliverables(deliverableTabs);
    }
    for (const tabId of [...agentTabsToClose, ...userTabsToRelease, ...deliverableTabs]) {
      delete sessionState.tabOrigins[String(tabId)];
      this.cursorByTabId.delete(tabId);
    }
    if (handoffTabs.length > 0) {
      sessionState.activeTabId = handoffTabs.includes(sessionState.activeTabId)
        ? sessionState.activeTabId
        : handoffTabs[0];
      this.activeTabsBySession.set(session.sessionId, sessionState.activeTabId);
      await this.store.save();
    } else {
      this.activeTabsBySession.delete(session.sessionId);
      await this.store.removeSession(session.sessionId);
      // Session over and no handoffs left: close the owned window, but only
      // when it is actually empty — never kill leftover user tabs in it.
      if (typeof sessionWindowId === "number") {
        const win = await chrome.windows.get(sessionWindowId).catch(() => null);
        if (win && typeof win.id === "number") {
          const remaining = (
            await chrome.tabs.query({ windowId: sessionWindowId }).catch(() => [])
          ).filter((tab) => typeof tab.id === "number");
          if (remaining.length === 0) {
            await chrome.windows.remove(sessionWindowId).catch(() => {});
          }
        }
      }
    }
    // Report what actually happened so agents can verify instead of trusting
    // silence: a lost session store or dissolved tab group previously degraded
    // to an empty no-op that still looked like success.
    const report = {
      deprecated: "finalizeTabs is deprecated; use markDeliverable, closeUserTab and releaseTabs. Legacy handoff retains ownership.",
      closed: agentTabsToClose,
      released: userTabsToRelease,
      kept: [
        ...deliverableTabs.map((tabId) => ({ tabId, status: "deliverable" })),
        ...handoffTabs.map((tabId) => ({ tabId, status: "handoff" }))
      ]
    };
    if (tabs.length === 0) {
      report.warning =
        "no session tabs found — nothing was closed (session state may be lost or its tab group dissolved); " +
        "verify with user_tabs() and close leftovers via close_tab(tab_id)";
    }
    return report;
  }

  async closeUserTab(params) {
    const session = await this.requireSession(params);
    await this.requireSessionTab(params, "closeUserTab");
    const tabId = requireTabId(params, "closeUserTab");
    const tab = await chrome.tabs.get(tabId);
    if (!hasTabId(tab)) {
      throw new Error(`Chrome tab ${tabId} has no id`);
    }
    if (tab.url?.startsWith("chrome://")) {
      throw new Error(`Chrome internal tab ${tabId} cannot be closed`);
    }
    if (this.attachedTabs.has(tabId)) await this.detachTab(tabId);
    await chrome.tabs.remove(tabId);
    await this.forgetOwnedTab(session.sessionId, tabId);
    return { closed: tabId };
  }

  async nameSession(params) {
    const session = await this.requireSession(params);
    const name = typeof params.name === "string" && params.name.trim() ? params.name.trim() : DEFAULT_SESSION_GROUP_TITLE;
    const sessionState = await this.store.getSession(session.sessionId);
    sessionState.title = name;
    await this.store.save();
    if (typeof sessionState.chromeGroupId === "number") {
      await chrome.tabGroups.update(sessionState.chromeGroupId, { title: name }).catch(() => {});
    }
  }

  requireReadinessSettled(tabId) {
    if (this.pendingReadiness.has(tabId)) {
      throw new Error(`Tab ${tabId} readiness is still pending in Chrome; ownership retained until cleanup settles`);
    }
  }

  async attach(params) {
    await this.requireSessionTab(params, "attach");
    const tabId = requireTabId(params, "attach");
    this.requireReadinessSettled(tabId);
    const attempt = { expired: false };
    this.pendingReadiness.set(tabId, attempt);
    const check = () => {
      if (attempt.expired) throw new Error(`Tab ${tabId} readiness expired`);
    };
    const work = (async () => {
      try {
        const tab = await chrome.tabs.get(tabId);
        check();
        if (this.attachedTabs.has(tabId) && this.wokenTabIds.has(tabId) && !tab.frozen && !tab.discarded) {
          return { wake: null };
        }
        if (tab.discarded) {
          // No renderer exists to wake with CDP. Reload only discarded pages;
          // never refresh a resident page (it may contain unsaved user state).
          await chrome.tabs.reload(tabId);
          check();
        }
        if (!this.attachedTabs.has(tabId)) {
          try {
            await chrome.debugger.attach({ tabId }, "1.3");
          } catch (error) {
            if (!String(error?.message ?? error).includes("Another debugger")) throw error;
            // After MV3 restart Chrome may still have OUR attachment. A
            // successful command proves ownership; never adopt DevTools or
            // another extension's debugger just from the error string.
            check();
            await chrome.debugger.sendCommand({ tabId }, "Page.setWebLifecycleState", { state: "active" });
          }
          this.attachedTabs.add(tabId);
          await this.updateCdpActionState();
          check();
        }
        const wake = await this.applyBackgroundWake(tabId, check);
        check();
        this.wokenTabIds.add(tabId);
        return { wake };
      } finally {
        // Do not put this cleanup back in the ownership queue: it must be
        // able to settle even when a caller has already timed out there.
        if (attempt.expired && this.attachedTabs.has(tabId)) {
          await this.detachTab(tabId).catch(() => {});
        }
        if (this.pendingReadiness.get(tabId) === attempt) this.pendingReadiness.delete(tabId);
      }
    })();
    let timer;
    try {
      return await Promise.race([
        work,
        new Promise((_, reject) => {
          timer = setTimeout(() => {
            attempt.expired = true;
            reject(new Error(`Tab ${tabId} readiness timed out after ${this.readinessTimeoutMs}ms; no page action was executed`));
          }, Number.isFinite(params._deadlineUnixMs)
            ? Math.max(1, Math.min(this.readinessTimeoutMs, params._deadlineUnixMs - Date.now() - 100))
            : this.readinessTimeoutMs);
        })
      ]);
    } finally {
      clearTimeout(timer);
    }
  }

  // Background rendering readiness is internal, not an agent-side ritual.
  // Lifecycle wake + focus emulation keep occluded/hidden pages rendering,
  // including visibility-gated lazy loading. Neither selects a tab/window.
  // Unsupported commands remain best-effort, but a pending renderer command
  // cannot run the next stage after its readiness deadline has expired.
  async applyBackgroundWake(tabId, check = () => {}) {
    if (!this.attachedTabs.has(tabId)) {
      return { skipped: "not-attached" };
    }
    // Unfreeze BEFORE sending renderer-dependent focus commands. The old
    // focus-first sequence could wait forever before reaching its own wake.
    check();
    const lifecycle = await chrome.debugger
      .sendCommand({ tabId }, "Page.setWebLifecycleState", { state: "active" })
      .then(() => ({ ok: true }))
      .catch((error) => ({ ok: false, error: String(error?.message ?? error) }));
    check();
    const focus = await chrome.debugger
      .sendCommand({ tabId }, "Emulation.setFocusEmulationEnabled", { enabled: true })
      .then(() => ({ ok: true }))
      .catch((error) => ({ ok: false, error: String(error?.message ?? error) }));
    check();
    return { focus, lifecycle };
  }

  async getTargetMapping(params) {
    await this.requireSessionTab(params, "getTargetMapping");
    const tabId = requireTabId(params, "getTargetMapping");
    const tab = await chrome.tabs.get(tabId);
    if (tab?.discarded) {
      await chrome.tabs.reload(tabId);
    }
    let match = null;
    for (let attempt = 0; attempt < 20; attempt++) {
      const targets = chrome.debugger?.getTargets ? await chrome.debugger.getTargets() : [];
      match = targets.find((t) => t.tabId === tabId);
      if (match) break;
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    if (!match) {
      throw new Error(`No debuggable target found for tab ${tabId}`);
    }
    return {
      tabId,
      targetId: match.id
    };
  }

  async detach(params) {
    await this.requireSessionTab(params, "detach");
    const tabId = requireTabId(params, "detach");
    await this.detachTab(tabId);
  }

  async executeCdp(params) {
    await this.requireSession(params);
    const target = params.target && typeof params.target === "object" ? params.target : {};
    const tabId = target.tabId != null ? requireTabId(target, "executeCdp") : undefined;
    if (tabId !== undefined) {
      // Normalize numeric strings so chrome.debugger.sendCommand gets an int.
      target.tabId = tabId;
      await this.requireSessionTab({ ...params, tabId }, "executeCdp");
      if (!this.attachedTabs.has(tabId)) {
        throw new Error("Debugger unattached");
      }
    }
    const timeoutMs =
      typeof params.timeoutMs === "number" && params.timeoutMs > 0
        ? params.timeoutMs
        : DEFAULT_CDP_TIMEOUT_MS;
    return await withTimeout(timeoutMs, async () => {
      if (params.method === "Target.getTargets") {
        return { targetInfos: await chrome.debugger.getTargets() };
      }
      if (typeof params.method !== "string") {
        throw new Error("executeCdp requires method");
      }
      return await chrome.debugger.sendCommand(target, params.method, params.commandParams ?? {});
    });
  }

  async moveMouse(params) {
    const session = await this.requireSession(params);
    await this.requireSessionTab(params, "moveMouse");
    const tabId = requireTabId(params, "moveMouse");
    if (!Number.isFinite(params.x) || !Number.isFinite(params.y)) {
      throw new Error("moveMouse requires finite x and y");
    }
    const cursorReady = await this.ensureCursorContentScript(session.sessionId, tabId);
    if (!cursorReady) {
      throw new Error(`Cannot inject cursor content script into tab ${tabId}`);
    }
    const moveSequence = this.nextCursorMoveSequence;
    this.nextCursorMoveSequence += 1;
    const cursor = {
      moveSequence,
      visible: true,
      x: params.x,
      y: params.y
    };
    this.cursorByTabId.set(tabId, cursor);
    const arrivalWaiter =
      params.waitForArrival === false
        ? null
        : this.createCursorArrivalWaiter(params.session_id, params.turn_id, moveSequence);
    try {
      await chrome.tabs.sendMessage(tabId, {
        type: "OPEN_BROWSER_USE_CURSOR",
        sessionId: params.session_id,
        turnId: params.turn_id,
        moveSequence,
        x: cursor.x,
        y: cursor.y,
        visible: cursor.visible
      });
      await arrivalWaiter?.promise;
    } catch (error) {
      arrivalWaiter?.cancel();
      throw error;
    }
  }

  async waitForFileChooser(params) {
    await this.requireSessionTab(params, "waitForFileChooser");
    const tabId = requireTabId(params, "waitForFileChooser");
    if (!this.attachedTabs.has(tabId)) {
      await this.attach(params);
    }
    const timeoutMs =
      typeof params.timeoutMs === "number" && params.timeoutMs > 0
        ? params.timeoutMs
        : DEFAULT_CDP_TIMEOUT_MS;
    const waitForEvent = this.waitForFileChooserEvent(tabId);
    try {
      await chrome.debugger.sendCommand({ tabId }, "Page.setInterceptFileChooserDialog", {
        enabled: true
      });
      return await withTimeout(timeoutMs, () => waitForEvent);
    } finally {
      this.fileChooserWaitersByTabId.delete(tabId);
      await chrome.debugger
        .sendCommand({ tabId }, "Page.setInterceptFileChooserDialog", { enabled: false })
        .catch(() => {});
    }
  }

  async setFileChooserFiles(params) {
    await this.requireSession(params);
    if (typeof params.fileChooserId !== "string" || params.fileChooserId === "") {
      throw new Error("setFileChooserFiles requires fileChooserId");
    }
    const chooser = this.fileChoosersById.get(params.fileChooserId);
    if (!chooser) {
      throw new Error(`Unknown file chooser id "${params.fileChooserId}"`);
    }
    await this.requireSessionTab({ ...params, tabId: chooser.tabId }, "setFileChooserFiles");
    if (!Array.isArray(params.files) || params.files.length === 0) {
      throw new Error("setFileChooserFiles requires at least one file");
    }
    const files = params.files.map((file) => {
      if (typeof file !== "string" || file === "") {
        throw new Error("setFileChooserFiles files must be non-empty strings");
      }
      return file;
    });
    if (!chooser.isMultiple && files.length > 1) {
      throw new Error("File chooser does not accept multiple files");
    }
    await chrome.debugger.sendCommand({ tabId: chooser.tabId }, "DOM.setFileInputFiles", {
      backendNodeId: chooser.backendNodeId,
      files
    });
    this.fileChoosersById.delete(params.fileChooserId);
  }

  async waitForDownload(params) {
    await this.requireSessionTab(params, "waitForDownload");
    const timeoutMs =
      typeof params.timeoutMs === "number" && params.timeoutMs > 0
        ? params.timeoutMs
        : DEFAULT_CDP_TIMEOUT_MS;
    const download = await this.waitForDownloadChange(
      (change) =>
        change.status === "started" ||
        change.status === "in_progress" ||
        change.status === "complete",
      timeoutMs,
      "Timed out waiting for download."
    );
    return { downloadId: download.id };
  }

  async downloadPath(params) {
    await this.requireSession(params);
    if (typeof params.downloadId !== "string" || params.downloadId === "") {
      throw new Error("downloadPath requires downloadId");
    }
    const current = this.downloadsById.get(params.downloadId);
    if (current?.status === "complete") {
      return { path: current.filename ?? null };
    }
    if (current?.status === "failed" || current?.status === "canceled") {
      return { path: null };
    }
    const timeoutMs =
      typeof params.timeoutMs === "number" && params.timeoutMs > 0
        ? params.timeoutMs
        : DEFAULT_CDP_TIMEOUT_MS;
    const download = await this.waitForDownloadChange(
      (change) =>
        change.id === params.downloadId &&
        (change.status === "complete" || change.status === "failed" || change.status === "canceled"),
      timeoutMs,
      `Timed out waiting for download ${params.downloadId}.`
    );
    return { path: download.status === "complete" ? download.filename ?? null : null };
  }

  async readClipboardText(params) {
    await this.requireSessionTab(params, "readClipboardText");
    const tabId = requireTabId(params, "readClipboardText");
    if (!this.attachedTabs.has(tabId)) {
      await this.attach(params);
    }
    const text = await this.evaluateJavascript(tabId, "navigator.clipboard.readText()", true);
    return { text: typeof text === "string" ? text : "" };
  }

  async writeClipboardText(params) {
    await this.requireSessionTab(params, "writeClipboardText");
    const tabId = requireTabId(params, "writeClipboardText");
    if (typeof params.text !== "string") {
      throw new Error("writeClipboardText requires text");
    }
    if (!this.attachedTabs.has(tabId)) {
      await this.attach(params);
    }
    await this.evaluateJavascript(tabId, `navigator.clipboard.writeText(${JSON.stringify(params.text)})`, true);
  }

  async readClipboard(params) {
    await this.requireSessionTab(params, "readClipboard");
    const tabId = requireTabId(params, "readClipboard");
    if (!this.attachedTabs.has(tabId)) {
      await this.attach(params);
    }
    const items = await this.evaluateJavascript(
      tabId,
      `((async () => {
        const clipboardItems = await navigator.clipboard.read();
        return await Promise.all(clipboardItems.map(async (item) => {
          const entries = await Promise.all(item.types.map(async (type) => {
            const blob = await item.getType(type);
            if (type.startsWith("text/")) {
              return { mime_type: type, text: await blob.text() };
            }
            const base64 = await new Promise((resolve, reject) => {
              const reader = new FileReader();
              reader.onerror = () => reject(reader.error);
              reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
              reader.readAsDataURL(blob);
            });
            return { mime_type: type, base64 };
          }));
          return { entries, presentation_style: item.presentationStyle ?? "unspecified" };
        }));
      })())`,
      true
    );
    return { items: Array.isArray(items) ? items : [] };
  }

  async writeClipboard(params) {
    await this.requireSessionTab(params, "writeClipboard");
    const tabId = requireTabId(params, "writeClipboard");
    if (!Array.isArray(params.items)) {
      throw new Error("writeClipboard requires items");
    }
    if (!this.attachedTabs.has(tabId)) {
      await this.attach(params);
    }
    await this.evaluateJavascript(
      tabId,
      `((async (items) => {
        const clipboardItems = items.map((item) => {
          const entries = {};
          for (const entry of item.entries ?? []) {
            const mime = entry.mime_type;
            if (!mime) continue;
            if (entry.text !== undefined) {
              entries[mime] = new Blob([entry.text], { type: mime });
            } else if (entry.base64 !== undefined) {
              const binary = atob(entry.base64);
              const bytes = new Uint8Array(binary.length);
              for (let index = 0; index < binary.length; index += 1) {
                bytes[index] = binary.charCodeAt(index);
              }
              entries[mime] = new Blob([bytes], { type: mime });
            }
          }
          return new ClipboardItem(entries, { presentationStyle: item.presentation_style ?? "unspecified" });
        });
        await navigator.clipboard.write(clipboardItems);
      })(${JSON.stringify(params.items)}))`,
      true
    );
  }

  async turnEnded(params) {
    const session = await this.requireSession(params);
    const tabs = await this.getSessionTabs(session.sessionId);
    await this.detachMany(tabs.filter(hasTabId).map((tab) => tab.id));
    this.activeTabsBySession.delete(session.sessionId);
    for (const tab of tabs) {
      if (hasTabId(tab)) {
        this.cursorByTabId.delete(tab.id);
      }
    }
    const sessionState = await this.store.getSession(session.sessionId);
    sessionState.activeTabId = null;
    await this.store.save();
    await Promise.allSettled(tabs.filter(hasTabId).map((tab) => this.publishCursorState(tab.id)));
  }

  async executeUnhandledCommand(params) {
    throw new Error(`Open Browser Use Chrome does not support command "${params.type}"`);
  }

  addDownloadChangeListener(listener) {
    this.downloadChangeListeners.add(listener);
    return () => this.downloadChangeListeners.delete(listener);
  }

  handleDownloadCreated(item) {
    if (!this.isBrowserControlActive() || !Number.isInteger(item?.id)) {
      return;
    }
    const filename = typeof item.filename === "string" ? item.filename : "";
    const url =
      typeof item.finalUrl === "string" ? item.finalUrl : typeof item.url === "string" ? item.url : "";
    this.downloadFilenamesById.set(item.id, filename);
    this.downloadUrlsById.set(item.id, url);
    this.emitDownloadChange({
      id: String(item.id),
      filename,
      url,
      status: "started"
    });
  }

  handleDownloadChanged(delta) {
    if (!Number.isInteger(delta?.id)) {
      return;
    }
    const filename =
      typeof delta.filename?.current === "string"
        ? delta.filename.current
        : this.downloadFilenamesById.get(delta.id);
    const url = this.downloadUrlsById.get(delta.id);
    if (typeof filename !== "string" || typeof url !== "string") {
      return;
    }
    this.downloadFilenamesById.set(delta.id, filename);
    const status = downloadStatus(delta);
    if (!status) {
      return;
    }
    this.emitDownloadChange({
      id: String(delta.id),
      filename,
      url,
      status
    });
    if (status === "complete" || status === "failed" || status === "canceled") {
      this.downloadFilenamesById.delete(delta.id);
      this.downloadUrlsById.delete(delta.id);
    }
  }

  notifyCursorArrived(params) {
    const moveSequence = params?.moveSequence;
    if (!Number.isInteger(moveSequence)) {
      return false;
    }
    const waiter = this.cursorArrivalWaitersByKey.get(cursorArrivalKey(params.sessionId, params.turnId, moveSequence));
    waiter?.();
    return Boolean(waiter);
  }

  handleCdpEvent(source, method, params) {
    if (method === "Page.fileChooserOpened" && typeof source?.tabId === "number") {
      const waiter = this.fileChooserWaitersByTabId.get(source.tabId);
      waiter?.resolve(params ?? {});
      return;
    }
    if (
      method === "Page.frameNavigated" &&
      typeof source?.tabId === "number" &&
      this.wokenTabIds.has(source.tabId) &&
      params?.frame &&
      typeof params.frame.parentId === "undefined"
    ) {
      // Navigating resets the page lifecycle state back to hidden+throttled
      // for an occluded window; re-apply the wake on the main frame so lazy
      // loading keeps working across navigations.
      // Reuse the bounded readiness path, serialized against release. Ignore
      // failure here; the next explicit attach surfaces pending readiness.
      const tabId = source.tabId;
      this.wokenTabIds.delete(tabId);
      void this.store.findTabOwner(tabId).then(owner => {
        if (owner) return this.attach({ session_id: owner.sessionId, turn_id: "navigation-wake", tabId });
      }).catch(() => {});
    }
  }

  waitForFileChooserEvent(tabId) {
    if (this.fileChooserWaitersByTabId.has(tabId)) {
      throw new Error(`Already waiting for file chooser in tab ${tabId}`);
    }
    return new Promise((resolve, reject) => {
      this.fileChooserWaitersByTabId.set(tabId, {
        reject: (error) => {
          this.fileChooserWaitersByTabId.delete(tabId);
          reject(error);
        },
        resolve: (event) => {
          this.fileChooserWaitersByTabId.delete(tabId);
          if (!Number.isInteger(event.backendNodeId)) {
            reject(new Error("File chooser event did not include backendNodeId"));
            return;
          }
          const fileChooserId = crypto.randomUUID();
          const chooser = {
            tabId,
            backendNodeId: event.backendNodeId,
            isMultiple: event.mode === "selectMultiple"
          };
          this.fileChoosersById.set(fileChooserId, chooser);
          resolve({
            fileChooserId,
            isMultiple: chooser.isMultiple
          });
        }
      });
    });
  }

  readCursorOverlayState(tabId) {
    for (const [sessionId, activeTabId] of this.activeTabsBySession) {
      if (activeTabId === tabId) {
        return {
          cursor: this.cursorByTabId.get(tabId) ?? null,
          isVisible: true,
          sessionId,
          turnId: null
        };
      }
    }
    return {
      cursor: null,
      isVisible: false,
      sessionId: null,
      turnId: null
    };
  }

  isBrowserControlActive() {
    return this.activeTabsBySession.size > 0;
  }

  createCursorArrivalWaiter(sessionId, turnId, moveSequence) {
    const key = cursorArrivalKey(sessionId, turnId, moveSequence);
    let timeoutId;
    let resolvePromise;
    const resolve = () => {
      if (timeoutId !== undefined) {
        clearTimeout(timeoutId);
      }
      this.cursorArrivalWaitersByKey.delete(key);
      resolvePromise?.();
    };
    const promise = new Promise((resolveInner) => {
      resolvePromise = resolveInner;
      timeoutId = setTimeout(resolve, CURSOR_ARRIVAL_TIMEOUT_MS);
      this.cursorArrivalWaitersByKey.set(key, resolve);
    });
    return { promise, cancel: resolve };
  }

  emitDownloadChange(change) {
    this.downloadsById.set(change.id, {
      ...(this.downloadsById.get(change.id) ?? {}),
      ...change
    });
    for (const waiter of [...this.downloadWaiters]) {
      if (waiter.predicate(change)) {
        waiter.resolve(change);
      }
    }
    for (const listener of this.downloadChangeListeners) {
      listener(change);
    }
  }

  waitForDownloadChange(predicate, timeoutMs, timeoutMessage) {
    for (const change of this.downloadsById.values()) {
      if (predicate(change)) {
        return Promise.resolve(change);
      }
    }
    return new Promise((resolve, reject) => {
      const waiter = {
        predicate,
        resolve: (change) => {
          cleanup();
          resolve(change);
        }
      };
      const cleanup = () => {
        clearTimeout(timeoutId);
        this.downloadWaiters.delete(waiter);
      };
      const timeoutId = setTimeout(() => {
        cleanup();
        reject(new Error(timeoutMessage));
      }, timeoutMs);
      this.downloadWaiters.add(waiter);
    });
  }

  async evaluateJavascript(tabId, expression, awaitPromise) {
    const result = await chrome.debugger.sendCommand({ tabId }, "Runtime.evaluate", {
      expression,
      returnByValue: true,
      awaitPromise
    });
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.text ?? "JavaScript evaluation failed");
    }
    return result.result?.value;
  }

  async ensureSessionGroup(sessionId, tabId, origin) {
    const session = await this.store.getSession(sessionId);
    session.tabOrigins[String(tabId)] = origin;
    await this.store.save();
    if (typeof session.windowId === "number") {
      // Isolated window: tabOrigins records ownership; keep a tab group only
      // for visual organization. New groups are
      // risky here: chrome.tabs.group pins a new group to the *current*
      // (focused) window and drags the tab there (createProperties.windowId is
      // ignored on some Chrome builds), which would pull a real session tab
      // into the user's window and let Chrome auto-close the emptied background
      // window. So group establishment uses a sacrificial scaffold tab and
      // repairs with tabGroups.move() — a real session tab never leaves the
      // owned window. If grouping is unavailable, explicit ownership still
      // works without a group.
      await this.ensureSessionGroupInWindow(session, session.windowId, tabId);
      await this.store.save();
      return;
    }
    let groupId = session.chromeGroupId;
    // A retained group can contain released pages. Do not reuse/rename such a
    // group when another task has since claimed one of those pages.
    if (typeof groupId === "number") {
      const grouped = await chrome.tabs.query({ groupId }).catch(() => []);
      if (grouped.some(tab => !Object.hasOwn(session.tabOrigins, String(tab.id)))) groupId = null;
    }
    if (typeof groupId === "number") {
      try {
        await chrome.tabGroups.get(groupId);
        await chrome.tabs.group({ groupId, tabIds: [tabId] });
      } catch {
        groupId = null;
      }
    }
    if (typeof groupId !== "number") {
      groupId = await chrome.tabs.group({ tabIds: [tabId] });
      session.chromeGroupId = groupId;
    }
    await this.store.save();
    await chrome.tabGroups.update(groupId, {
      title: session.title,
      color: "blue",
      collapsed: false
    });
  }

  // Establish (or confirm) a live tab group anchored inside the session's
  // owned window and join `tabId`. Returns true when the tab ended up grouped.
  // Never lets a real session tab risk being dragged: a temporary scaffold tab
  // absorbs any wrong-window placement, the group is repaired with
  // tabGroups.move(), the REAL tab joins while the scaffold still anchors the
  // group (Chrome drops empty groups when their last member leaves), and only
  // then is the scaffold removed. Failures leave explicit ownership intact.
  async ensureSessionGroupInWindow(session, winId, tabId) {
    if (typeof session.chromeGroupId === "number") {
      const grouped = await chrome.tabs.query({ groupId: session.chromeGroupId }).catch(() => []);
      if (grouped.some(tab => !Object.hasOwn(session.tabOrigins, String(tab.id)))) session.chromeGroupId = null;
    }
    if (typeof session.chromeGroupId === "number") {
      try {
        const group = await chrome.tabGroups.get(session.chromeGroupId);
        if (group && group.windowId === winId) {
          await chrome.tabs.group({ groupId: session.chromeGroupId, tabIds: [tabId] });
          return true;
        }
      } catch {
        // stale anchor: recreate below
      }
    }

    const scaffold = await chrome.tabs
      .create({ windowId: winId, active: false, url: "about:blank" })
      .catch(() => null);
    if (!hasTabId(scaffold)) {
      return false;
    }

    let groupId = null;
    try {
      groupId = await chrome.tabs.group({
        tabIds: [scaffold.id],
        createProperties: { windowId: winId }
      });
      const created = await chrome.tabGroups.get(groupId).catch(() => null);
      if (!created || created.windowId !== winId) {
        // Chrome pinned the new group to the current (focused) window and
        // dragged the scaffold there — relocate the whole group back.
        const moved = await chrome.tabGroups
          .move(groupId, { windowId: winId, index: -1 })
          .catch(() => null);
        const relocated = moved
          ? await chrome.tabGroups.get(moved.id ?? groupId).catch(() => null)
          : null;
        if (!relocated || relocated.windowId !== winId) {
          // Grouping is unreliable on this build: give up gracefully.
          await chrome.tabs.ungroup([scaffold.id]).catch(() => {});
          groupId = null;
        }
      }
      if (groupId !== null) {
        // Join the real tab while the scaffold still anchors the group; a tab
        // removed from an anchored group is safe, an emptied group is not.
        await chrome.tabs.group({ groupId, tabIds: [tabId] });
      }
    } catch {
      groupId = null;
    } finally {
      await chrome.tabs.remove(scaffold.id).catch(() => {});
    }

    if (groupId === null) {
      session.chromeGroupId = null;
      return false;
    }
    session.chromeGroupId = groupId;
    await chrome.tabGroups.update(groupId, {
      title: session.title,
      color: "blue",
      collapsed: false
    }).catch(() => {});
    return true;
  }

  async setSessionActiveTab(sessionId, tabId) {
    const session = await this.store.getSession(sessionId);
    session.activeTabId = tabId;
    this.activeTabsBySession.set(sessionId, tabId);
    await this.store.save();
    await this.publishCursorState(tabId);
  }

  async getSessionTabs(sessionId) {
    const session = await this.store.getSession(sessionId);
    // tabOrigins is the explicit ownership ledger. Groups/windows only arrange
    // pages; moving, releasing or manually ungrouping a page cannot grant it.
    const tabs = await Promise.all(Object.keys(session.tabOrigins ?? {}).map(async (raw) => {
      const tab = await chrome.tabs.get(Number(raw)).catch(() => null);
      return tab && hasTabId(tab) && !tab.url?.startsWith("chrome://") ? tab : null;
    }));
    return tabs.filter(Boolean);
  }

  async requireSession(params) {
    if (!params || typeof params !== "object") {
      throw new Error("Missing browser session params");
    }
    if (typeof params.session_id !== "string") {
      throw new Error("Missing required browser session_id");
    }
    if (typeof params.turn_id !== "string") {
      throw new Error("Missing required browser turn_id");
    }
    await this.store.getSession(params.session_id);
    return { sessionId: params.session_id, turnId: params.turn_id };
  }

  async requireSessionTab(params, command) {
    const session = await this.requireSession(params);
    const tabId = requireTabId(params, command);
    this.requireReadinessSettled(tabId);
    const tabs = await this.getSessionTabs(session.sessionId);
    if (!tabs.some((tab) => tab.id === tabId)) {
      throw new Error(`Tab ${tabId} is not part of browser session ${session.sessionId}`);
    }
  }

  async withLogicalActive(sessionId, tabs) {
    if (tabs.length === 0) {
      return tabs;
    }
    const session = await this.store.getSession(sessionId);
    let activeTabId = this.activeTabsBySession.get(sessionId) ?? session.activeTabId;
    if (!tabs.some((tab) => tab.id === activeTabId)) {
      activeTabId = tabs.find((tab) => tab.active)?.id ?? tabs[0].id;
      await this.setSessionActiveTab(sessionId, activeTabId);
    } else if (!this.activeTabsBySession.has(sessionId)) {
      this.activeTabsBySession.set(sessionId, activeTabId);
    }
    return tabs.map((tab) => ({ ...tab, active: tab.id === activeTabId }));
  }

  async moveToDeliverables(tabIds) {
    if (!Array.isArray(tabIds) || tabIds.length === 0) {
      return;
    }

    const tabRecords = await Promise.all(
      tabIds.map((id) => chrome.tabs.get(id).catch(() => null))
    );
    const tabsByWindow = new Map();
    for (const tab of tabRecords) {
      if (!tab || typeof tab.id !== "number" || typeof tab.windowId !== "number") continue;
      const arr = tabsByWindow.get(tab.windowId) ?? [];
      arr.push(tab.id);
      tabsByWindow.set(tab.windowId, arr);
    }
    if (tabsByWindow.size === 0) {
      return;
    }

    const storedGroupId = this.store.state.deliverableGroupId;
    let lastGroupId = null;

    for (const [windowId, windowTabIds] of tabsByWindow) {
      const existingGroups = await chrome.tabGroups
        .query({ windowId, title: DELIVERABLE_GROUP_TITLE })
        .catch(() => []);

      let primaryGroupId = null;
      if (existingGroups.length > 0) {
        const preferred =
          existingGroups.find((g) => g.id === storedGroupId) ??
          existingGroups.reduce((min, g) => (g.id < min.id ? g : min), existingGroups[0]);
        primaryGroupId = preferred.id;

        for (const group of existingGroups) {
          if (group.id === primaryGroupId) continue;
          const dupTabs = await chrome.tabs.query({ groupId: group.id }).catch(() => []);
          const dupTabIds = dupTabs
            .map((t) => t.id)
            .filter((id) => typeof id === "number");
          if (dupTabIds.length > 0) {
            await chrome.tabs
              .group({ groupId: primaryGroupId, tabIds: dupTabIds })
              .catch(() => {});
          }
        }
      }

      if (typeof primaryGroupId === "number") {
        try {
          await chrome.tabs.group({ groupId: primaryGroupId, tabIds: windowTabIds });
        } catch {
          primaryGroupId = null;
        }
      }

      if (typeof primaryGroupId !== "number") {
        primaryGroupId = await chrome.tabs.group({ tabIds: windowTabIds });
      }

      await chrome.tabGroups
        .update(primaryGroupId, {
          title: DELIVERABLE_GROUP_TITLE,
          color: "green",
          collapsed: false
        })
        .catch(() => {});

      lastGroupId = primaryGroupId;
    }

    if (typeof lastGroupId === "number") {
      this.store.state.deliverableGroupId = lastGroupId;
      await this.store.save();
    }
  }

  async detachMany(tabIds) {
    await Promise.allSettled(tabIds.map((tabId) => this.detachTab(tabId)));
  }

  async detachTab(tabId) {
    await chrome.debugger.detach({ tabId });
    {
      this.attachedTabs.delete(tabId);
      this.wokenTabIds.delete(tabId);
      for (const [fileChooserId, chooser] of this.fileChoosersById) {
        if (chooser.tabId === tabId) {
          this.fileChoosersById.delete(fileChooserId);
        }
      }
      this.cursorByTabId.delete(tabId);
    }
    await this.updateCdpActionState();
  }

  async ensureCursorContentScript(sessionId, tabId) {
    try {
      const response = await withTimeout(this.cursorTimeoutMs, () =>
        chrome.tabs.sendMessage(tabId, { type: "OPEN_BROWSER_USE_PING" }));
      if (response?.ok) {
        return true;
      }
    } catch {}
    try {
      await withTimeout(this.cursorTimeoutMs, () => chrome.scripting.executeScript({
        target: { tabId },
        files: ["content-cursor.js"]
      }));
      return true;
    } catch {
      return false;
    }
  }

  async publishCursorState(tabId) {
    if (!(await this.ensureCursorContentScript(null, tabId))) {
      return false;
    }
    try {
      const response = await withTimeout(this.cursorTimeoutMs, () => chrome.tabs.sendMessage(tabId, {
        type: "OPEN_BROWSER_USE_CURSOR_STATE",
        state: this.readCursorOverlayState(tabId)
      }));
      return response?.ok === true;
    } catch {
      return false;
    }
  }
}

function hasTabId(tab) {
  return typeof tab?.id === "number";
}

function toBrowserTab(tab) {
  return {
    id: tab.id,
    title: tab.title,
    active: tab.active,
    url: tab.url,
    windowId: tab.windowId,
    groupId: tab.groupId
  };
}

function toUserTab(tab, groupTitles) {
  const lastAccessed =
    typeof tab.lastAccessed === "number" && Number.isFinite(tab.lastAccessed)
      ? new Date(tab.lastAccessed).toISOString()
      : undefined;
  const groupTitle =
    typeof tab.groupId === "number" && tab.groupId !== -1 ? groupTitles.get(tab.groupId) : undefined;
  return {
    id: tab.id,
    ...(tab.title ? { title: tab.title } : {}),
    ...(tab.url ? { url: tab.url } : {}),
    ...(lastAccessed ? { lastOpened: lastAccessed } : {}),
    ...(groupTitle ? { tabGroup: groupTitle } : {})
  };
}

function requireTabId(params, command) {
  const raw = params.tabId;
  // Tolerate numeric strings ("42", " 42 ") from heredoc scripts, SDKs or
  // models that round-trip tab ids through JSON; keep plain ints untouched
  // and reject anything else with a message that shows what was received.
  if (typeof raw === "string" && /^\d+$/.test(raw.trim())) {
    return Number(raw.trim());
  }
  if (!Number.isInteger(raw)) {
    throw new Error(
      `${command} requires an integer tabId; got ${JSON.stringify(raw)}`
    );
  }
  return raw;
}

async function createBackgroundTab() {
  const windowId = await chooseWindowId();
  if (typeof windowId === "number") {
    const tab = await chrome.tabs.create({ active: false, url: "about:blank", windowId });
    if (hasTabId(tab)) {
      return tab;
    }
  }
  const win = await chrome.windows.create({
    focused: false,
    type: "normal",
    url: "about:blank"
  });
  const tab = win.tabs?.find(hasTabId);
  if (!tab) {
    throw new Error("Created Chrome window has no tab");
  }
  return tab;
}

function isolatedWindowBounds() {
  let availWidth = null;
  let availHeight = null;
  try {
    availWidth = typeof screen !== "undefined" ? screen.availWidth : null;
    availHeight = typeof screen !== "undefined" ? screen.availHeight : null;
  } catch {
    // screen unavailable in some extension contexts; fall back to defaults
  }
  // Normal size: fill the available screen like a regular Chrome window
  // (small margin around it). Not capped — a full viewport is what SPA
  // virtual-scroll lists expect. Floor guards against degenerate tiny screens.
  const margin = ISOLATED_WINDOW_MARGIN;
  const width = availWidth
    ? Math.max(320, availWidth - margin * 2)
    : ISOLATED_WINDOW_WIDTH;
  const height = availHeight
    ? Math.max(240, availHeight - margin * 2)
    : ISOLATED_WINDOW_HEIGHT;
  // Centered, like a normal window; Chrome picks default placement when the
  // screen is unavailable.
  const left = availWidth
    ? Math.max(0, Math.round((availWidth - width) / 2))
    : null;
  const top = availHeight
    ? Math.max(0, Math.round((availHeight - height) / 2))
    : null;
  return { width, height, left, top };
}

async function chooseWindowId() {
  const windows = await chrome.windows.getAll({ windowTypes: ["normal"] });
  const focused = windows.find((win) => win.focused && typeof win.id === "number");
  return focused?.id ?? windows.find((win) => typeof win.id === "number")?.id ?? null;
}

async function readGroupTitles(tabs) {
  const ids = new Set(
    tabs
      .map((tab) => tab.groupId)
      .filter((groupId) => typeof groupId === "number" && groupId !== -1)
  );
  const entries = await Promise.all(
    [...ids].map(async (groupId) => {
      try {
        const group = await chrome.tabGroups.get(groupId);
        return group.title ? [groupId, group.title] : null;
      } catch {
        return null;
      }
    })
  );
  return new Map(entries.filter(Boolean));
}

function compareLastAccessed(left, right) {
  const byLastAccessed = (right.lastAccessed ?? 0) - (left.lastAccessed ?? 0);
  if (byLastAccessed !== 0) {
    return byLastAccessed;
  }
  const byWindow = (left.windowId ?? 0) - (right.windowId ?? 0);
  return byWindow !== 0 ? byWindow : (left.index ?? 0) - (right.index ?? 0);
}

function parseDate(value, field) {
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) {
    throw new Error(`getUserHistory requires ${field} to be a valid date`);
  }
  return timestamp;
}

function downloadStatus(delta) {
  switch (delta.state?.current) {
    case "complete":
      return "complete";
    case "interrupted":
      return delta.error?.current === "USER_CANCELED" ? "canceled" : "failed";
    case "in_progress":
      return "in_progress";
    default:
      return undefined;
  }
}

function cursorArrivalKey(sessionId, turnId, moveSequence) {
  return `${sessionId}:${turnId}:${moveSequence}`;
}

async function withTimeout(timeoutMs, fn) {
  let timeout;
  try {
    return await Promise.race([
      fn(),
      new Promise((_, reject) => {
        timeout = setTimeout(() => {
          reject(new Error(`Timed out after ${timeoutMs}ms waiting for CDP command.`));
        }, timeoutMs);
      })
    ]);
  } finally {
    clearTimeout(timeout);
  }
}

function startHeartbeat(peer, backend) {
  chrome.alarms.create(HEARTBEAT_ALARM_NAME, { periodInMinutes: 0.5 }).catch(() => {});
  chrome.alarms.onAlarm.addListener((alarm) => {
    if (alarm.name === HEARTBEAT_ALARM_NAME) {
      safeSendNotification(peer, "heartbeat", { at: new Date().toISOString() });
      void backend.getInfo().catch(() => {});
    }
  });
}

chrome.runtime.onInstalled.addListener(async () => {
  const { extensionInstanceId } = await chrome.storage.local.get("extensionInstanceId");
  if (typeof extensionInstanceId !== "string") {
    await chrome.storage.local.set({ extensionInstanceId: crypto.randomUUID() });
  }
});

const backend = new BrowserBackend();
const transport = new NativeTransport(NATIVE_HOST_NAME);
transport.setDisconnectCallback(() => {
  void backend.setDirectCdpConnectionState({ connected: false });
});
const peer = new JsonRpcPeer(transport, backend);
void backend.updateCdpActionState();
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message?.type === "GET_NATIVE_HOST_STATUS") {
    const status = transport.getStatus();
    sendResponse({ ok: status.state === "connected", status, error: status.error });
    return true;
  }
  if (message?.type === "GET_OPEN_BROWSER_USE_CURSOR_STATE") {
    const tabId = typeof sender.tab?.id === "number" ? sender.tab.id : -1;
    sendResponse({ ok: true, state: backend.readCursorOverlayState(tabId) });
    return true;
  }
  if (message?.type === "OPEN_BROWSER_USE_CURSOR_ARRIVED") {
    sendResponse({ ok: backend.notifyCursorArrived(message) });
    return true;
  }
  return false;
});
chrome.debugger.onEvent.addListener((source, method, params) => {
  backend.handleCdpEvent(source, method, params);
  safeSendNotification(peer, "onCDPEvent", { source, method, params });
});
backend.addDownloadChangeListener((change) => {
  safeSendNotification(peer, "onDownloadChange", change);
});
chrome.downloads.onCreated.addListener((item) => {
  backend.handleDownloadCreated(item);
});
chrome.downloads.onChanged.addListener((delta) => {
  backend.handleDownloadChanged(delta);
});
startHeartbeat(peer, backend);

function safeSendNotification(peer, method, params) {
  try {
    peer.sendNotification(method, params);
  } catch {}
}
