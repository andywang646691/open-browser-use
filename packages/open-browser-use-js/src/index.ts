import { Socket, createConnection } from "node:net";
import { endianness } from "node:os";

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type BrowserUseRequestParams = { [key: string]: JsonValue };

export type OpenBrowserUseClientOptions = {
  socketPath: string;
  sessionId?: string;
  turnId?: string;
  timeoutMs?: number;
};

type PendingRequest = {
  resolve: (value: JsonValue) => void;
  reject: (error: Error) => void;
  timeout: NodeJS.Timeout;
};

export type OpenBrowserUseNotification = {
  method: string;
  params?: JsonValue;
};

export type NotificationHandler = (notification: OpenBrowserUseNotification) => void;

const headerBytes = 4;
const defaultNavigationTimeoutMs = 10_000;

export type OpenBrowserUseLoadState = "domcontentloaded" | "load";

export type OpenBrowserUseGotoOptions = {
  waitUntil?: OpenBrowserUseLoadState;
  timeoutMs?: number;
};


export type OpenBrowserUseWaitForLoadStateOptions = {
  state?: OpenBrowserUseLoadState;
  timeoutMs?: number;
};
export type FinalizeKeepEntry = {
  tabId: number;
  status: "handoff" | "deliverable";
};

export type CdpCommandResult = { [key: string]: JsonValue };

export type EvaluatePageFunction<A, T> = (args: A) => T | Promise<T>;

export type EvaluateOptions = {
  awaitPromise?: boolean;
};

export type WaitForOptions = {
  timeoutMs?: number;
  intervalMs?: number;
  awaitPromise?: boolean;
};

export class OpenBrowserUseEvaluateError extends Error {
  readonly tabId: number;
  readonly expression: string;

  constructor(message: string, details: { tabId: number; expression: string }) {
    super(message);
    this.name = "OpenBrowserUseEvaluateError";
    this.tabId = details.tabId;
    this.expression = details.expression;
  }
}

export class OpenBrowserUseWaitTimeoutError extends Error {
  readonly tabId: number;
  readonly condition: string;
  readonly lastValue: JsonValue;

  constructor(message: string, details: { tabId: number; condition: string; lastValue: JsonValue }) {
    super(message);
    this.name = "OpenBrowserUseWaitTimeoutError";
    this.tabId = details.tabId;
    this.condition = details.condition;
    this.lastValue = details.lastValue;
  }
}

export function serializePageFunction<A>(pageFunction: Function, args?: A): string {
  if (typeof pageFunction !== "function") {
    throw new TypeError("pageFunction must be a function");
  }
  let source: string;
  try {
    source = pageFunction.toString();
  } catch {
    throw new TypeError("pageFunction must be serializable via toString()");
  }
  if (!source.trim()) {
    throw new TypeError("pageFunction source is empty");
  }
  let argsJson: string;
  try {
    argsJson = JSON.stringify(args ?? null);
  } catch (error) {
    throw new TypeError(`pageFunction args must be JSON-serializable (${(error as Error).message})`);
  }
  return `(${source})(${argsJson})`;
}

export function formatEvaluateError(response: { [key: string]: JsonValue } | null | undefined, fallback: string): string {
  if (!response || !isObject(response.exceptionDetails)) {
    return fallback;
  }
  const details = response.exceptionDetails;
  const description = stringValue(isObject(details.exception) ? details.exception.description : undefined);
  const text = stringValue(details.text);
  const url = stringValue(details.url);
  const line = typeof details.lineNumber === "number" ? details.lineNumber : undefined;
  const column = typeof details.columnNumber === "number" ? details.columnNumber : undefined;
  if (description) {
    return description;
  }
  const location = url ? `${url}${line !== undefined ? `:${line}${column !== undefined ? `:${column}` : ""}` : ""}` : undefined;
  if (text) {
    return location ? `${text} at ${location}` : text;
  }
  return location ? `${fallback} at ${location}` : fallback;
}

export function assertFinalizeKeep(keep: unknown): asserts keep is FinalizeKeepEntry[] {
  if (!Array.isArray(keep)) {
    throw new TypeError("finalizeTabs keep must be an array of {tabId: number, status: \"handoff\" | \"deliverable\"}");
  }
  for (const entry of keep) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) {
      throw new TypeError(`invalid finalizeTabs keep entry ${JSON.stringify(entry)}; expected {tabId: number, status: "handoff" | "deliverable"}`);
    }
    const { tabId, status } = entry as { tabId?: unknown; status?: unknown };
    if (typeof tabId !== "number" || !Number.isInteger(tabId) || tabId <= 0) {
      throw new TypeError(`invalid finalizeTabs keep tabId ${JSON.stringify(tabId)}; expected a positive integer`);
    }
    if (status !== "handoff" && status !== "deliverable") {
      throw new TypeError(`invalid finalizeTabs keep status ${JSON.stringify(status)}; expected "handoff" or "deliverable"`);
    }

  }
}

export type OpenBrowserUseBrowserOptions = OpenBrowserUseClientOptions | { client: OpenBrowserUseClient };

export class OpenBrowserUseClient {
  readonly socketPath: string;
  readonly sessionId: string;
  readonly turnId: string;
  readonly timeoutMs: number;
  #socket: Socket | null = null;
  #pendingData = Buffer.alloc(0);
  #nextId = 1;
  #pending = new Map<string, PendingRequest>();
  #notificationHandlers = new Set<NotificationHandler>();

  constructor(options: OpenBrowserUseClientOptions) {
    this.socketPath = options.socketPath;
    this.sessionId = options.sessionId ?? "open-browser-use-js";
    this.turnId = options.turnId ?? `turn-${Date.now()}`;
    this.timeoutMs = options.timeoutMs ?? 10_000;
  }

  async connect(): Promise<this> {
    if (this.#socket) {
      return this;
    }
    const socket = createConnection(this.socketPath);
    this.#socket = socket;
    socket.on("data", (chunk) => this.#handleData(Buffer.from(chunk)));
    socket.on("close", () => this.#rejectAll(new Error("Open Browser Use socket closed")));
    socket.on("error", (error) => this.#rejectAll(error));
    await new Promise<void>((resolve, reject) => {
      socket.once("connect", resolve);
      socket.once("error", reject);
    });
    return this;
  }

  close(): void {
    this.#socket?.end();
    this.#socket = null;
  }

  onNotification(handler: NotificationHandler): () => void {
    this.#notificationHandlers.add(handler);
    return () => {
      this.#notificationHandlers.delete(handler);
    };
  }

  async request(method: string, params: BrowserUseRequestParams = {}): Promise<JsonValue> {
    await this.connect();
    const socket = this.#socket;
    if (!socket) {
      throw new Error("Open Browser Use socket is not connected");
    }
    const id = String(this.#nextId++);
    const mergedParams = {
      session_id: this.sessionId,
      turn_id: this.turnId,
      ...params
    };
    const message = {
      jsonrpc: "2.0",
      id,
      method,
      params: mergedParams
    };
    const payload = encodeFrame(message);
    const promise = new Promise<JsonValue>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.#pending.delete(id);
        reject(new Error(`Open Browser Use request timed out: ${method}`));
      }, this.timeoutMs);
      this.#pending.set(id, { resolve, reject, timeout });
    });
    socket.write(payload);
    return await promise;
  }

  getInfo(): Promise<JsonValue> {
    return this.request("getInfo");
  }

  createTab(): Promise<JsonValue> {
    return this.request("createTab");
  }

  getTabs(): Promise<JsonValue> {
    return this.request("getTabs");
  }

  getUserTabs(): Promise<JsonValue> {
    return this.request("getUserTabs");
  }

  getUserHistory(params: BrowserUseRequestParams = {}): Promise<JsonValue> {
    return this.request("getUserHistory", params);
  }

  claimUserTab(tabId: number): Promise<JsonValue> {
    return this.request("claimUserTab", { tabId });
  }

  finalizeTabs(keep: FinalizeKeepEntry[] = []): Promise<JsonValue> {
    assertFinalizeKeep(keep);
    return this.request("finalizeTabs", { keep });
  }

  nameSession(name: string): Promise<JsonValue> {
    return this.request("nameSession", { name });
  }

  attach(tabId: number): Promise<JsonValue> {
    return this.request("attach", { tabId });
  }

  detach(tabId: number): Promise<JsonValue> {
    return this.request("detach", { tabId });
  }

  executeCdp(tabId: number, method: string, commandParams: BrowserUseRequestParams = {}): Promise<CdpCommandResult> {
    return this.request("executeCdp", {
      target: { tabId },
      method,
      commandParams
    }) as Promise<CdpCommandResult>;
  }

  moveMouse(tabId: number, x: number, y: number, waitForArrival = true): Promise<JsonValue> {
    return this.request("moveMouse", { tabId, x, y, waitForArrival });
  }

  waitForFileChooser(tabId: number, timeoutMs?: number): Promise<JsonValue> {
    return this.request("waitForFileChooser", {
      tabId,
      ...(timeoutMs === undefined ? {} : { timeoutMs })
    });
  }

  setFileChooserFiles(fileChooserId: string, files: string[]): Promise<JsonValue> {
    return this.request("setFileChooserFiles", { fileChooserId, files });
  }

  waitForDownload(tabId: number, timeoutMs?: number): Promise<JsonValue> {
    return this.request("waitForDownload", {
      tabId,
      ...(timeoutMs === undefined ? {} : { timeoutMs })
    });
  }

  downloadPath(downloadId: string, timeoutMs?: number): Promise<JsonValue> {
    return this.request("downloadPath", {
      downloadId,
      ...(timeoutMs === undefined ? {} : { timeoutMs })
    });
  }

  browserUserHistory(params: BrowserUseRequestParams = {}): Promise<JsonValue> {
    return this.getUserHistory(params);
  }

  readClipboardText(tabId: number): Promise<JsonValue> {
    return this.request("readClipboardText", { tabId });
  }

  writeClipboardText(tabId: number, text: string): Promise<JsonValue> {
    return this.request("writeClipboardText", { tabId, text });
  }

  readClipboard(tabId: number): Promise<JsonValue> {
    return this.request("readClipboard", { tabId });
  }

  writeClipboard(tabId: number, items: JsonValue[]): Promise<JsonValue> {
    return this.request("writeClipboard", { tabId, items });
  }

  turnEnded(): Promise<JsonValue> {
    return this.request("turnEnded");
  }

  #handleData(chunk: Buffer): void {
    this.#pendingData = Buffer.concat([this.#pendingData, chunk]);
    while (this.#pendingData.length >= headerBytes) {
      const length =
        endianness() === "LE"
          ? this.#pendingData.readUInt32LE(0)
          : this.#pendingData.readUInt32BE(0);
      const total = headerBytes + length;
      if (this.#pendingData.length < total) {
        return;
      }
      const payload = this.#pendingData.subarray(headerBytes, total);
      this.#pendingData = this.#pendingData.subarray(total);
      this.#handleMessage(JSON.parse(payload.toString("utf8")) as JsonValue);
    }
  }

  #handleMessage(message: JsonValue): void {
    if (!isObject(message)) {
      return;
    }
    const id = typeof message.id === "string" || typeof message.id === "number" ? String(message.id) : null;
    if (!id && typeof message.method === "string") {
      const notification = {
        method: message.method,
        params: message.params
      };
      for (const handler of this.#notificationHandlers) {
        handler(notification);
      }
      return;
    }
    if (!id) {
      return;
    }
    const pending = this.#pending.get(id);
    if (!pending) {
      return;
    }
    this.#pending.delete(id);
    clearTimeout(pending.timeout);
    if (isObject(message.error)) {
      pending.reject(new Error(String(message.error.message ?? "Open Browser Use request failed")));
      return;
    }
    pending.resolve((message.result ?? null) as JsonValue);
  }

  #rejectAll(error: Error): void {
    for (const [id, pending] of this.#pending) {
      this.#pending.delete(id);
      clearTimeout(pending.timeout);
      pending.reject(error);
    }
  }
}

export async function connectOpenBrowserUse(options: OpenBrowserUseBrowserOptions): Promise<OpenBrowserUseBrowser> {
  const browser = new OpenBrowserUseBrowser(options);
  await browser.connect();
  return browser;
}

export class OpenBrowserUseBrowser {
  readonly client: OpenBrowserUseClient;
  readonly cdp: OpenBrowserUseCdp;

  constructor(options: OpenBrowserUseBrowserOptions) {
    this.client = "client" in options ? options.client : new OpenBrowserUseClient(options);
    this.cdp = new OpenBrowserUseCdp(this.client);
  }

  async connect(): Promise<this> {
    await this.client.connect();
    return this;
  }

  close(): void {
    this.client.close();
  }

  async newTab(options: OpenBrowserUseGotoOptions & { url?: string } = {}): Promise<OpenBrowserUseTab> {
    const created = await this.client.createTab();
    const tabId = tabIdFromValue(created, "createTab response");
    const tab = this.tab(tabId);
    if (options.url) {
      await tab.goto(options.url, options);
    }
    return tab;
  }

  tab(tabId: number): OpenBrowserUseTab {
    return new OpenBrowserUseTab(this, tabId);
  }

  getTabs(): Promise<JsonValue> {
    return this.client.getTabs();
  }
}

export class OpenBrowserUseTab {
  readonly browser: OpenBrowserUseBrowser;
  readonly id: number;
  readonly playwright: OpenBrowserUseTabPlaywright;

  constructor(browser: OpenBrowserUseBrowser, id: number) {
    this.browser = browser;
    this.id = id;
    this.playwright = new OpenBrowserUseTabPlaywright(this);
  }

  goto(url: string, options: OpenBrowserUseGotoOptions = {}): Promise<JsonValue> {
    return this.browser.cdp.navigate(this.id, url, options);
  }

  waitForLoadState(options: OpenBrowserUseWaitForLoadStateOptions = {}): Promise<void> {
    return this.browser.cdp.waitForLoadState(this.id, options);
  }

  domSnapshot(): Promise<string> {
    return this.browser.cdp.evaluate(this.id, "document.body?.innerText ?? ''").then((value) => String(value ?? ""));
  }

  evaluate(expression: string, options?: EvaluateOptions): Promise<JsonValue>;
  evaluate<A, T>(pageFunction: EvaluatePageFunction<A, T>, args?: A, options?: EvaluateOptions): Promise<T>;
  evaluate(expressionOrPageFunction: string | Function, argsOrOptions?: unknown, options?: EvaluateOptions): Promise<unknown> {
    if (typeof expressionOrPageFunction === "function") {
      const expression = serializePageFunction(expressionOrPageFunction, argsOrOptions);
      return this.browser.cdp.evaluate(this.id, expression, options ?? {});
    }
    return this.browser.cdp.evaluate(this.id, expressionOrPageFunction, (argsOrOptions as EvaluateOptions | undefined) ?? {});
  }

  /** Evaluate a raw JavaScript expression. Prefer the function form {@link evaluate} when possible. */
  evaluateRaw(expression: string, options: EvaluateOptions = {}): Promise<JsonValue> {
    return this.browser.cdp.evaluate(this.id, expression, options);
  }

  waitFor(selector: string, options?: WaitForOptions): Promise<JsonValue>;
  waitFor<A, T>(pageFunction: EvaluatePageFunction<A, T>, args?: A, options?: WaitForOptions): Promise<T>;
  waitFor(condition: string | Function, argsOrOptions?: unknown, options?: WaitForOptions): Promise<unknown> {
    if (typeof condition === "function") {
      return this.browser.cdp.waitFor(this.id, condition, argsOrOptions, options);
    }
    return this.browser.cdp.waitFor(this.id, condition, argsOrOptions as WaitForOptions | undefined);
  }

  close(): Promise<JsonValue> {
    return this.browser.cdp.call(this.id, "Page.close");
  }
}

export class OpenBrowserUseTabPlaywright {
  readonly tab: OpenBrowserUseTab;

  constructor(tab: OpenBrowserUseTab) {
    this.tab = tab;
  }

  waitForLoadState(options: OpenBrowserUseWaitForLoadStateOptions = {}): Promise<void> {
    return this.tab.waitForLoadState(options);
  }

  domSnapshot(): Promise<string> {
    return this.tab.domSnapshot();
  }
}

export class OpenBrowserUseCdp {
  readonly client: OpenBrowserUseClient;
  #attachedTabIds = new Set<number>();

  constructor(client: OpenBrowserUseClient) {
    this.client = client;
  }

  async call(
    tabId: number,
    method: string,
    commandParams: BrowserUseRequestParams = {},
    options: { timeoutMs?: number; retried?: boolean } = {}
  ): Promise<CdpCommandResult> {
    await this.ensureAttached(tabId);
    try {
      return (await this.client.request("executeCdp", {
        target: { tabId },
        method,
        commandParams,
        ...(options.timeoutMs === undefined ? {} : { timeoutMs: options.timeoutMs })
      })) as CdpCommandResult;
    } catch (error) {
      // The debugger can be detached out from under us (finalize, navigation),
      // while our attach bookkeeping thinks the tab is still attached. Reset
      // the bookkeeping and retry once.
      if (!options.retried && isDebuggerDetachedError(error)) {
        this.#attachedTabIds.delete(tabId);
        return this.call(tabId, method, commandParams, { ...options, retried: true });
      }
      throw error;
    }
  }

  async evaluate(
    tabId: number,
    expression: string,
    options: EvaluateOptions = {}
  ): Promise<JsonValue> {
    const result = await this.call(tabId, "Runtime.evaluate", {
      expression,
      returnByValue: true,
      ...(options.awaitPromise === undefined ? {} : { awaitPromise: options.awaitPromise })
    });
    if (isObject(result) && isObject(result.exceptionDetails)) {
      throw new OpenBrowserUseEvaluateError(
        formatEvaluateError(result, `evaluation failed in tab ${tabId}`),
        { tabId, expression }
      );
    }
    return isObject(result) && isObject(result.result) ? (result.result.value ?? null) : null;
  }

  async navigate(tabId: number, url: string, options: OpenBrowserUseGotoOptions = {}): Promise<JsonValue> {
    if (!url) {
      throw new Error("goto requires a URL");
    }
    const waitUntil = options.waitUntil ?? "load";
    assertSupportedLoadState(waitUntil);
    const timeoutMs = options.timeoutMs ?? defaultNavigationTimeoutMs;
    await this.call(tabId, "Page.enable");
    const wait = this.waitForLoadEvent(tabId, waitUntil, timeoutMs);
    const result = await this.call(tabId, "Page.navigate", { url }, { timeoutMs });
    if (isObject(result) && typeof result.errorText === "string" && result.errorText) {
      throw new Error(`Browser failed to navigate tab ${tabId}: ${result.errorText}`);
    }
    await wait.catch(async (error) => {
      const state = await this.readDocumentState(tabId);
      if (documentStateMatches(state, waitUntil)) {
        return;
      }
      throw error;
    });
    return result;
  }

  async waitForLoadState(tabId: number, options: OpenBrowserUseWaitForLoadStateOptions = {}): Promise<void> {
    const state = options.state ?? "load";
    assertSupportedLoadState(state);
    await this.call(tabId, "Page.enable");
    const documentState = await this.readDocumentState(tabId);
    if (documentStateMatches(documentState, state)) {
      return;
    }
    await this.waitForLoadEvent(tabId, state, options.timeoutMs ?? defaultNavigationTimeoutMs);
  }

  async readDocumentState(tabId: number): Promise<{ href?: string; readyState?: string } | undefined> {
    try {
      const value = await this.evaluate(tabId, "({ href: window.location.href, readyState: document.readyState })");
      return isObject(value) ? { href: stringValue(value.href), readyState: stringValue(value.readyState) } : undefined;
    } catch {
      return undefined;
    }
  }

  waitForEvent(
    tabId: number,
    predicate: (notification: OpenBrowserUseNotification) => boolean,
    options: { timeoutMs?: number; timeoutMessage?: string } = {}
  ): Promise<OpenBrowserUseNotification> {
    const timeoutMs = options.timeoutMs ?? defaultNavigationTimeoutMs;
    return new Promise((resolve, reject) => {
      let settled = false;
      let removeHandler: (() => void) | null = null;
      let timer: NodeJS.Timeout;
      const finish = (): boolean => {
        if (settled) {
          return false;
        }
        settled = true;
        clearTimeout(timer);
        removeHandler?.();
        return true;
      };
      timer = setTimeout(() => {
        if (finish()) {
          reject(new Error(options.timeoutMessage ?? `Timed out waiting for tab ${tabId} event`));
        }
      }, timeoutMs);
      removeHandler = this.client.onNotification((notification) => {
        try {
          if (predicate(notification)) {
            if (finish()) {
              resolve(notification);
            }
          }
        } catch (error) {
          if (finish()) {
            reject(error instanceof Error ? error : new Error(String(error)));
          }
        }
      });
    });
  }

  waitForLoadEvent(tabId: number, state: OpenBrowserUseLoadState, timeoutMs: number): Promise<void> {
    return this.waitForEvent(
      tabId,
      (notification) => {
        const event = cdpEventForTab(notification, tabId);
        if (!event) {
          return false;
        }
        if (event.method === "Page.navigationBlocked") {
          throw new Error(`Navigation was blocked in tab ${tabId}`);
        }
        return state === "domcontentloaded"
          ? event.method === "Page.domContentEventFired"
          : event.method === "Page.loadEventFired";
      },
      {
        timeoutMs,
        timeoutMessage: `Timed out waiting for ${state} in tab ${tabId}`
      }
    ).then(() => undefined);
  }

  async ensureAttached(tabId: number): Promise<void> {
    if (this.#attachedTabIds.has(tabId)) {
      return;
    }
    await this.client.attach(tabId);
    this.#attachedTabIds.add(tabId);
  }

  async waitFor(tabId: number, selector: string, options?: WaitForOptions): Promise<JsonValue>;
  async waitFor<A>(tabId: number, pageFunction: Function, args?: A, options?: WaitForOptions): Promise<JsonValue>;
  async waitFor(
    tabId: number,
    condition: string | Function,
    argsOrOptions?: unknown,
    options?: WaitForOptions
  ): Promise<JsonValue> {
    const waitOptions: WaitForOptions =
      typeof condition === "function" ? (options ?? {}) : ((argsOrOptions as WaitForOptions | undefined) ?? {});
    const args = typeof condition === "function" ? argsOrOptions : undefined;
    const timeoutMs = waitOptions.timeoutMs ?? 10_000;
    const intervalMs = waitOptions.intervalMs ?? 500;
    const awaitPromise = waitOptions.awaitPromise;
    const expression =
      typeof condition === "function"
        ? serializePageFunction(condition, args)
        : `!!document.querySelector(${JSON.stringify(condition)})`;
    const conditionLabel = typeof condition === "function" ? condition.toString().slice(0, 200) : condition;
    const deadline = Date.now() + timeoutMs;
    let lastValue: JsonValue = null;
    for (;;) {
      lastValue = await this.evaluate(tabId, expression, { ...(awaitPromise === undefined ? {} : { awaitPromise }) });
      if (isTruthy(lastValue)) {
        return lastValue;
      }
      if (Date.now() >= deadline) {
        break;
      }
      await sleep(intervalMs);
    }
    throw new OpenBrowserUseWaitTimeoutError(
      `Timed out after ${timeoutMs}ms waiting on tab ${tabId} for: ${conditionLabel}`,
      { tabId, condition: conditionLabel, lastValue }
    );
  }
}

export function encodeFrame(value: JsonValue | Record<string, unknown>): Buffer {
  const payload = Buffer.from(JSON.stringify(value), "utf8");
  const frame = Buffer.alloc(headerBytes + payload.length);
  if (endianness() === "LE") {
    frame.writeUInt32LE(payload.length, 0);
  } else {
    frame.writeUInt32BE(payload.length, 0);
  }
  payload.copy(frame, headerBytes);
  return frame;
}

function isObject(value: JsonValue | unknown): value is { [key: string]: JsonValue } {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function tabIdFromValue(value: JsonValue, label: string): number {
  if (!isObject(value)) {
    throw new Error(`${label} did not include a tab object`);
  }
  const id = value.id;
  if (typeof id === "number" && Number.isInteger(id) && id > 0) {
    return id;
  }
  if (typeof id === "string") {
    const parsed = Number(id);
    if (Number.isInteger(parsed) && parsed > 0) {
      return parsed;
    }
  }
  throw new Error(`${label} did not include a numeric tab id`);
}

function assertSupportedLoadState(state: string): asserts state is OpenBrowserUseLoadState {
  if (state !== "domcontentloaded" && state !== "load") {
    throw new Error(`Unsupported load state "${state}". Use "domcontentloaded" or "load".`);
  }
}

function documentStateMatches(documentState: { readyState?: string } | undefined, state: OpenBrowserUseLoadState): boolean {
  if (documentState?.readyState === "complete") {
    return true;
  }
  return state === "domcontentloaded" && documentState?.readyState === "interactive";
}

function cdpEventForTab(
  notification: OpenBrowserUseNotification,
  tabId: number
): { method?: string } | null {
  if (notification.method !== "onCDPEvent" || !isObject(notification.params)) {
    return null;
  }
  const source = notification.params.source;
  if (!isObject(source) || source.tabId !== tabId) {
    return null;
  }
  return {
    method: stringValue(notification.params.method)
  };
}


function stringValue(value: JsonValue | undefined): string | undefined {
  return typeof value === "string" ? value : undefined;
}
function isTruthy(value: JsonValue): boolean {
  return value !== null && value !== false && value !== 0 && value !== "" && value !== undefined;
}

function isDebuggerDetachedError(error: unknown): boolean {
  return error instanceof Error && /Debugger unattached/i.test(error.message);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
