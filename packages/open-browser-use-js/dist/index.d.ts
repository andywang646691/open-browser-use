export type JsonValue = null | boolean | number | string | JsonValue[] | {
    [key: string]: JsonValue;
};
export type BrowserUseRequestParams = {
    [key: string]: JsonValue;
};
export type OpenBrowserUseClientOptions = {
    socketPath: string;
    sessionId?: string;
    turnId?: string;
    timeoutMs?: number;
};
export type OpenBrowserUseNotification = {
    method: string;
    params?: JsonValue;
};
export type NotificationHandler = (notification: OpenBrowserUseNotification) => void;
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
export type CdpCommandResult = {
    [key: string]: JsonValue;
};
export type OpenBrowserUseExtensionInfo = {
    id: string;
    name: string;
    version: string;
    path: string;
    enabled: boolean;
};
export type EvaluatePageFunction<A, T> = (args: A) => T | Promise<T>;
export type EvaluateOptions = {
    awaitPromise?: boolean;
};
export type WaitForOptions = {
    timeoutMs?: number;
    intervalMs?: number;
    awaitPromise?: boolean;
};
export declare class OpenBrowserUseEvaluateError extends Error {
    readonly tabId: number;
    readonly expression: string;
    constructor(message: string, details: {
        tabId: number;
        expression: string;
    });
}
export declare class OpenBrowserUseWaitTimeoutError extends Error {
    readonly tabId: number;
    readonly condition: string;
    readonly lastValue: JsonValue;
    constructor(message: string, details: {
        tabId: number;
        condition: string;
        lastValue: JsonValue;
    });
}
export declare function serializePageFunction<A>(pageFunction: Function, args?: A): string;
export declare function formatEvaluateError(response: {
    [key: string]: JsonValue;
} | null | undefined, fallback: string): string;
export declare function assertFinalizeKeep(keep: unknown): asserts keep is FinalizeKeepEntry[];
export type OpenBrowserUseBrowserOptions = OpenBrowserUseClientOptions | {
    client: OpenBrowserUseClient;
};
export declare class OpenBrowserUseClient {
    #private;
    readonly socketPath: string;
    readonly sessionId: string;
    readonly turnId: string;
    readonly timeoutMs: number;
    constructor(options: OpenBrowserUseClientOptions);
    connect(): Promise<this>;
    close(): void;
    onNotification(handler: NotificationHandler): () => void;
    request(method: string, params?: BrowserUseRequestParams): Promise<JsonValue>;
    getInfo(): Promise<JsonValue>;
    createTab(options?: {
        windowMode?: "isolated" | "user";
    }): Promise<JsonValue>;
    getTabs(): Promise<JsonValue>;
    getUserTabs(): Promise<JsonValue>;
    getUserHistory(params?: BrowserUseRequestParams): Promise<JsonValue>;
    claimUserTab(tabId: number): Promise<JsonValue>;
    finalizeTabs(keep?: FinalizeKeepEntry[]): Promise<JsonValue>;
    nameSession(name: string): Promise<JsonValue>;
    attach(tabId: number): Promise<JsonValue>;
    detach(tabId: number): Promise<JsonValue>;
    executeCdp(tabId: number, method: string, commandParams?: BrowserUseRequestParams): Promise<CdpCommandResult>;
    moveMouse(tabId: number, x: number, y: number, waitForArrival?: boolean): Promise<JsonValue>;
    waitForFileChooser(tabId: number, timeoutMs?: number): Promise<JsonValue>;
    setFileChooserFiles(fileChooserId: string, files: string[]): Promise<JsonValue>;
    waitForDownload(tabId: number, timeoutMs?: number): Promise<JsonValue>;
    downloadPath(downloadId: string, timeoutMs?: number): Promise<JsonValue>;
    browserUserHistory(params?: BrowserUseRequestParams): Promise<JsonValue>;
    readClipboardText(tabId: number): Promise<JsonValue>;
    writeClipboardText(tabId: number, text: string): Promise<JsonValue>;
    readClipboard(tabId: number): Promise<JsonValue>;
    writeClipboard(tabId: number, items: JsonValue[]): Promise<JsonValue>;
    turnEnded(): Promise<JsonValue>;
    listExtensions(): Promise<OpenBrowserUseExtensionInfo[]>;
    installExtension(path: string, enableInIncognito?: boolean): Promise<{
        id: string;
    }>;
    reloadExtension(id: string): Promise<{
        id: string;
    }>;
    triggerExtensionAction(id: string, tabId?: number): Promise<JsonValue>;
    uninstallExtension(id: string): Promise<JsonValue>;
}
export declare function connectOpenBrowserUse(options: OpenBrowserUseBrowserOptions): Promise<OpenBrowserUseBrowser>;
export declare class OpenBrowserUseBrowser {
    readonly client: OpenBrowserUseClient;
    readonly cdp: OpenBrowserUseCdp;
    constructor(options: OpenBrowserUseBrowserOptions);
    connect(): Promise<this>;
    close(): void;
    newTab(options?: OpenBrowserUseGotoOptions & {
        url?: string;
        windowMode?: "isolated" | "user";
    }): Promise<OpenBrowserUseTab>;
    tab(tabId: number): OpenBrowserUseTab;
    getTabs(): Promise<JsonValue>;
}
export declare class OpenBrowserUseTab {
    readonly browser: OpenBrowserUseBrowser;
    readonly id: number;
    readonly playwright: OpenBrowserUseTabPlaywright;
    constructor(browser: OpenBrowserUseBrowser, id: number);
    goto(url: string, options?: OpenBrowserUseGotoOptions): Promise<JsonValue>;
    waitForLoadState(options?: OpenBrowserUseWaitForLoadStateOptions): Promise<void>;
    domSnapshot(): Promise<string>;
    evaluate(expression: string, options?: EvaluateOptions): Promise<JsonValue>;
    evaluate<A, T>(pageFunction: EvaluatePageFunction<A, T>, args?: A, options?: EvaluateOptions): Promise<T>;
    /** Evaluate a raw JavaScript expression. Prefer the function form {@link evaluate} when possible. */
    evaluateRaw(expression: string, options?: EvaluateOptions): Promise<JsonValue>;
    waitFor(selector: string, options?: WaitForOptions): Promise<JsonValue>;
    waitFor<A, T>(pageFunction: EvaluatePageFunction<A, T>, args?: A, options?: WaitForOptions): Promise<T>;
    close(): Promise<JsonValue>;
}
export declare class OpenBrowserUseTabPlaywright {
    readonly tab: OpenBrowserUseTab;
    constructor(tab: OpenBrowserUseTab);
    waitForLoadState(options?: OpenBrowserUseWaitForLoadStateOptions): Promise<void>;
    domSnapshot(): Promise<string>;
}
export declare class OpenBrowserUseCdp {
    #private;
    readonly client: OpenBrowserUseClient;
    constructor(client: OpenBrowserUseClient);
    call(tabId: number, method: string, commandParams?: BrowserUseRequestParams, options?: {
        timeoutMs?: number;
        retried?: boolean;
    }): Promise<CdpCommandResult>;
    evaluate(tabId: number, expression: string, options?: EvaluateOptions): Promise<JsonValue>;
    navigate(tabId: number, url: string, options?: OpenBrowserUseGotoOptions): Promise<JsonValue>;
    waitForLoadState(tabId: number, options?: OpenBrowserUseWaitForLoadStateOptions): Promise<void>;
    readDocumentState(tabId: number): Promise<{
        href?: string;
        readyState?: string;
    } | undefined>;
    waitForEvent(tabId: number, predicate: (notification: OpenBrowserUseNotification) => boolean, options?: {
        timeoutMs?: number;
        timeoutMessage?: string;
    }): Promise<OpenBrowserUseNotification>;
    waitForLoadEvent(tabId: number, state: OpenBrowserUseLoadState, timeoutMs: number): Promise<void>;
    ensureAttached(tabId: number): Promise<void>;
    waitFor(tabId: number, selector: string, options?: WaitForOptions): Promise<JsonValue>;
    waitFor<A>(tabId: number, pageFunction: Function, args?: A, options?: WaitForOptions): Promise<JsonValue>;
}
export declare function encodeFrame(value: JsonValue | Record<string, unknown>): Buffer;
