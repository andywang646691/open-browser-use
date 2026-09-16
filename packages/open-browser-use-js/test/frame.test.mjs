import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { endianness, tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  OpenBrowserUseClient,
  OpenBrowserUseEvaluateError,
  OpenBrowserUseWaitTimeoutError,
  connectOpenBrowserUse,
  encodeFrame,
  formatEvaluateError,
  serializePageFunction
} from "../dist/index.js";

test("encodes native-endian length-prefixed JSON frames", () => {
  const frame = encodeFrame({ id: 1, method: "getInfo" });
  const length = endianness() === "LE" ? frame.readUInt32LE(0) : frame.readUInt32BE(0);
  assert.equal(length, frame.length - 4);
  assert.deepEqual(JSON.parse(frame.subarray(4).toString("utf8")), {
    id: 1,
    method: "getInfo"
  });
});

test("dispatches JSON-RPC notifications from the native socket", async () => {
  const directory = await mkdtemp(join(tmpdir(), "obu-sdk-js-"));
  const socketPath = join(directory, "obu.sock");
  const server = createServer((socket) => {
    socket.write(
      encodeFrame({
        jsonrpc: "2.0",
        method: "onDownloadChange",
        params: { id: "1", status: "started" }
      })
    );
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });

  const client = new OpenBrowserUseClient({ socketPath });
  try {
    const notification = await new Promise((resolve) => {
      client.onNotification(resolve);
      void client.connect();
    });
    assert.deepEqual(notification, {
      method: "onDownloadChange",
      params: { id: "1", status: "started" }
    });
  } finally {
    client.close();
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
    await rm(directory, { recursive: true, force: true });
  }
});

test("createTab forwards windowMode onto the wire as window_mode", async () => {
  const directory = await mkdtemp(join(tmpdir(), "obu-sdk-js-"));
  const socketPath = join(directory, "obu.sock");
  const calls = [];
  const server = createServer((socket) => {
    let pending = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      pending = Buffer.concat([pending, chunk]);
      while (pending.length >= 4) {
        const length = endianness() === "LE" ? pending.readUInt32LE(0) : pending.readUInt32BE(0);
        if (pending.length < 4 + length) {
          return;
        }
        const request = JSON.parse(pending.subarray(4, 4 + length).toString("utf8"));
        pending = pending.subarray(4 + length);
        calls.push(request.params);
        socket.write(encodeFrame({ jsonrpc: "2.0", id: request.id, result: { id: 123 } }));
      }
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });

  const browser = await connectOpenBrowserUse({ socketPath });
  try {
    await browser.newTab({ windowMode: "user" });
    await browser.client.createTab({ windowMode: "isolated" });
    const params0 = calls[0];
    const params1 = calls[1];
    assert.equal(params0.window_mode, "user");
    assert.equal(params1.window_mode, "isolated");
  } finally {
    browser.close();
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
    await rm(directory, { recursive: true, force: true });
  }
});

test("high-level browser tabs can goto, wait for load state, and read a DOM snapshot", async () => {
  const directory = await mkdtemp(join(tmpdir(), "obu-sdk-js-"));
  const socketPath = join(directory, "obu.sock");
  const calls = [];
  const server = createServer((socket) => {
    let pending = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      pending = Buffer.concat([pending, chunk]);
      while (pending.length >= 4) {
        const length = endianness() === "LE" ? pending.readUInt32LE(0) : pending.readUInt32BE(0);
        if (pending.length < 4 + length) {
          return;
        }
        const request = JSON.parse(pending.subarray(4, 4 + length).toString("utf8"));
        pending = pending.subarray(4 + length);
        calls.push([request.method, request.params.method ?? null]);
        let result = {};
        if (request.method === "createTab") {
          result = { id: 123 };
        } else if (request.method === "executeCdp" && request.params.method === "Page.navigate") {
          socket.write(
            encodeFrame({
              jsonrpc: "2.0",
              method: "onCDPEvent",
              params: {
                source: { tabId: 123 },
                method: "Page.domContentEventFired",
                params: {}
              }
            })
          );
          result = { frameId: "frame-1" };
        } else if (request.method === "executeCdp" && request.params.method === "Runtime.evaluate") {
          const expression = request.params.commandParams.expression;
          result = expression.includes("readyState")
            ? { result: { value: { href: "https://example.test/issues", readyState: "interactive" } } }
            : { result: { value: "Open\nClosed\nIssues\nStarred" } };
        }
        socket.write(encodeFrame({ jsonrpc: "2.0", id: request.id, result }));
      }
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });

  const browser = await connectOpenBrowserUse({ socketPath });
  try {
    const tab = await browser.newTab();
    await tab.goto("https://example.test/issues", { waitUntil: "domcontentloaded", timeoutMs: 1000 });
    await tab.playwright.waitForLoadState({ state: "domcontentloaded", timeoutMs: 1000 });
    assert.equal(await tab.playwright.domSnapshot(), "Open\nClosed\nIssues\nStarred");
    assert.deepEqual(calls, [
      ["createTab", null],
      ["attach", null],
      ["executeCdp", "Page.enable"],
      ["executeCdp", "Page.navigate"],
      ["executeCdp", "Page.enable"],
      ["executeCdp", "Runtime.evaluate"],
      ["executeCdp", "Runtime.evaluate"]
    ]);
  } finally {
    browser.close();
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
    await rm(directory, { recursive: true, force: true });
  }
});

test("sends file chooser wrapper requests", async () => {
  const directory = await mkdtemp(join(tmpdir(), "obu-sdk-js-"));
  const socketPath = join(directory, "obu.sock");
  const server = createServer((socket) => {
    socket.once("data", (chunk) => {
      const length = endianness() === "LE" ? chunk.readUInt32LE(0) : chunk.readUInt32BE(0);
      const request = JSON.parse(chunk.subarray(4, 4 + length).toString("utf8"));
      assert.equal(request.method, "waitForFileChooser");
      assert.equal(request.params.tabId, 123);
      socket.write(
        encodeFrame({
          jsonrpc: "2.0",
          id: request.id,
          result: { fileChooserId: "chooser-1", isMultiple: false }
        })
      );
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });

  const client = new OpenBrowserUseClient({ socketPath });
  try {
    assert.deepEqual(await client.waitForFileChooser(123), {
      fileChooserId: "chooser-1",
      isMultiple: false
    });
  } finally {
    client.close();
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
    await rm(directory, { recursive: true, force: true });
  }
});

test("sends extension lifecycle wrapper requests", async () => {
  await withMockServer(
    (request) => {
      if (request.method === "listExtensions") {
        return { result: [{ id: "extension-id", name: "Test", version: "1", path: "/tmp/ext", enabled: true }] };
      }
      if (request.method === "installExtension") {
        assert.deepEqual(request.params, {
          session_id: request.params.session_id,
          turn_id: request.params.turn_id,
          path: "/tmp/ext",
          enableInIncognito: true
        });
        return { result: { id: "extension-id" } };
      }
      if (request.method === "reloadExtension") return { result: { id: "extension-id" } };
      if (request.method === "triggerExtensionAction") {
        assert.equal(request.params.id, "extension-id");
        assert.equal(request.params.tabId, 123);
        return { result: { ok: true } };
      }
      if (request.method === "uninstallExtension") return { result: { ok: true } };
      throw new Error(`unexpected method ${request.method}`);
    },
    async (socketPath) => {
      const client = new OpenBrowserUseClient({ socketPath });
      assert.deepEqual(await client.listExtensions(), [
        { id: "extension-id", name: "Test", version: "1", path: "/tmp/ext", enabled: true }
      ]);
      assert.deepEqual(await client.installExtension("/tmp/ext", true), { id: "extension-id" });
      assert.deepEqual(await client.reloadExtension("extension-id"), { id: "extension-id" });
      assert.deepEqual(await client.triggerExtensionAction("extension-id", 123), { ok: true });
      assert.deepEqual(await client.uninstallExtension("extension-id"), { ok: true });
      client.close();
    }
  );
  assert.throws(() => new OpenBrowserUseClient({ socketPath: "/tmp/unused" }).installExtension("relative"), /absolute/);
});

test("sends download and clipboard wrapper requests", async () => {
  const directory = await mkdtemp(join(tmpdir(), "obu-sdk-js-"));
  const socketPath = join(directory, "obu.sock");
  const expected = [
    ["waitForDownload", { tabId: 123, timeoutMs: 5000 }],
    ["downloadPath", { downloadId: "download-1" }],
    ["readClipboardText", { tabId: 123 }],
    ["writeClipboardText", { tabId: 123, text: "hello" }]
  ];
  const server = createServer((socket) => {
    let index = 0;
    socket.on("data", (chunk) => {
      const length = endianness() === "LE" ? chunk.readUInt32LE(0) : chunk.readUInt32BE(0);
      const request = JSON.parse(chunk.subarray(4, 4 + length).toString("utf8"));
      const [method, params] = expected[index++];
      assert.equal(request.method, method);
      assert.deepEqual(
        Object.fromEntries(Object.entries(params).map(([key, value]) => [key, request.params[key]])),
        params
      );
      socket.write(encodeFrame({ jsonrpc: "2.0", id: request.id, result: {} }));
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });

  const client = new OpenBrowserUseClient({ socketPath });
  try {
    await client.waitForDownload(123, 5000);
    await client.downloadPath("download-1");
    await client.readClipboardText(123);
    await client.writeClipboardText(123, "hello");
    assert.equal(expected.length, 4);
  } finally {
    client.close();
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
    await rm(directory, { recursive: true, force: true });
  }
});

async function withMockServer(handler, run) {
  const directory = await mkdtemp(join(tmpdir(), "obu-sdk-js-"));
  const socketPath = join(directory, "obu.sock");
  const server = createServer((socket) => {
    let pending = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      pending = Buffer.concat([pending, chunk]);
      while (pending.length >= 4) {
        const length = endianness() === "LE" ? pending.readUInt32LE(0) : pending.readUInt32BE(0);
        if (pending.length < 4 + length) {
          return;
        }
        const request = JSON.parse(pending.subarray(4, 4 + length).toString("utf8"));
        pending = pending.subarray(4 + length);
        const response = handler(request);
        socket.write(encodeFrame({ jsonrpc: "2.0", id: request.id, ...response }));
      }
    });
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(socketPath, resolve);
  });
  try {
    await run(socketPath);
  } finally {
    server.close();
    await new Promise((resolve) => server.once("close", resolve));
    await rm(directory, { recursive: true, force: true });
  }
}

test("serializePageFunction embeds source and JSON args", () => {
  const expression = serializePageFunction((args) => document.querySelector(args.selector).innerText, {
    selector: ".tweet"
  });
  assert.match(expression, /\(\(args\) => /);
  assert.match(expression, /document\.querySelector\(args\.selector\)\.innerText/);
  assert.ok(expression.endsWith('({"selector":".tweet"})'));
  assert.throws(() => serializePageFunction("not a function"), /pageFunction must be a function/);
  assert.throws(() => serializePageFunction(() => {}, { big: 1n }), /JSON-serializable/);
});

test("formatEvaluateError prefers exception description with stack", () => {
  const message = formatEvaluateError(
    {
      exceptionDetails: {
        text: "Uncaught",
        exception: { description: "ReferenceError: foo is not defined\n    at <anonymous>:1:13" }
      }
    },
    "evaluation failed"
  );
  assert.match(message, /ReferenceError: foo is not defined/);
  assert.match(message, /<anonymous>:1:13/);
  assert.equal(formatEvaluateError({ result: {} }, "fallback"), "fallback");
  assert.match(
    formatEvaluateError(
      { exceptionDetails: { text: "Uncaught", url: "https://x.test/a.js", lineNumber: 3 } },
      "boom"
    ),
    /Uncaught at https:\/\/x\.test\/a\.js:3/
  );
});

test("finalizeTabs validates keep locally with actionable messages", () => {
  const client = new OpenBrowserUseClient({ socketPath: "/nonexistent.sock" });
  assert.throws(() => client.finalizeTabs([123]), /expected \{tabId: number, status: "handoff" \| "deliverable"\}/);
  assert.throws(
    () => client.finalizeTabs([{ tabId: 1, status: "keep" }]),
    /invalid finalizeTabs keep status "keep"; expected "handoff" or "deliverable"/
  );
  assert.throws(() => client.finalizeTabs([{ tabId: 0, status: "handoff" }]), /positive integer/);
  assert.throws(() => client.finalizeTabs(null), /keep must be an array/);
});

test("tab.evaluate accepts a page function with args", async () => {
  let seen = null;
  await withMockServer(
    (request) => {
      if (request.method === "attach") {
        return { result: {} };
      }
      if (request.method === "executeCdp" && request.params.method === "Runtime.evaluate") {
        seen = request.params.commandParams.expression;
        return { result: { result: { value: 42 } } };
      }
      return { result: {} };
    },
    async (socketPath) => {
      const browser = await connectOpenBrowserUse({ socketPath });
      try {
        const tab = browser.tab(42);
        const value = await tab.evaluate((args) => args.a + args.b, { a: 40, b: 2 });
        assert.equal(value, 42);
        assert.ok(seen.endsWith('({"a":40,"b":2})'));
      } finally {
        browser.close();
      }
    }
  );
});

test("evaluate throws OpenBrowserUseEvaluateError with the page stack", async () => {
  await withMockServer(
    (request) => {
      if (request.method === "attach") {
        return { result: {} };
      }
      if (request.method === "executeCdp" && request.params.method === "Runtime.evaluate") {
        return {
          result: {
            exceptionDetails: {
              text: "Uncaught",
              exception: { description: "TypeError: x is not a function\n    at <anonymous>:1:1" }
            }
          }
        };
      }
      return { result: {} };
    },
    async (socketPath) => {
      const browser = await connectOpenBrowserUse({ socketPath });
      try {
        const tab = browser.tab(42);
        await assert.rejects(
          tab.evaluate("undefined.x()"),
          (error) =>
            error instanceof OpenBrowserUseEvaluateError &&
            /TypeError: x is not a function/.test(error.message) &&
            error.expression === "undefined.x()"
        );
        await assert.rejects(tab.evaluate(() => undefined.x()), OpenBrowserUseEvaluateError);
      } finally {
        browser.close();
      }
    }
  );
});

test("call retries once when the debugger was detached", async () => {
  let calls = 0;
  await withMockServer(
    (request) => {
      if (request.method === "attach") {
        return { result: {} };
      }
      if (request.method === "executeCdp" && request.params.method === "Runtime.evaluate") {
        calls += 1;
        if (calls === 1) {
          return { error: { message: "Debugger unattached" } };
        }
        return { result: { result: { value: "ok" } } };
      }
      return { result: {} };
    },
    async (socketPath) => {
      const browser = await connectOpenBrowserUse({ socketPath });
      try {
        const tab = browser.tab(42);
        assert.equal(await tab.evaluate("1 + 1"), "ok");
        assert.equal(calls, 2);
      } finally {
        browser.close();
      }
    }
  );
});

test("waitFor polls a selector until it appears and returns the value", async () => {
  let count = 0;
  await withMockServer(
    (request) => {
      if (request.method === "attach") {
        return { result: {} };
      }
      if (request.method === "executeCdp" && request.params.method === "Runtime.evaluate") {
        const expression = request.params.commandParams.expression;
        if (expression.includes("document.querySelector")) {
          count += 1;
          return { result: { result: { type: "boolean", value: count >= 2 } } };
        }
        return { result: { result: { type: "string", value: "page" } } };
      }
      return { result: {} };
    },
    async (socketPath) => {
      const browser = await connectOpenBrowserUse({ socketPath });
      try {
        const tab = browser.tab(42);
        const value = await tab.waitFor(".loaded", { intervalMs: 5, timeoutMs: 500 });
        assert.equal(value, true);
        assert.ok(count >= 2);
      } finally {
        browser.close();
      }
    }
  );
});

test("waitFor times out with the last observed value", async () => {
  await withMockServer(
    (request) => {
      if (request.method === "attach") {
        return { result: {} };
      }
      if (request.method === "executeCdp" && request.params.method === "Runtime.evaluate") {
        return { result: { result: { type: "boolean", value: false } } };
      }
      return { result: {} };
    },
    async (socketPath) => {
      const browser = await connectOpenBrowserUse({ socketPath });
      try {
        const tab = browser.tab(42);
        await assert.rejects(
          tab.waitFor(".never", { intervalMs: 5, timeoutMs: 50 }),
          (error) =>
            error instanceof OpenBrowserUseWaitTimeoutError &&
            /Timed out after 50ms/.test(error.message) &&
            error.lastValue === false &&
            error.condition === ".never"
        );
      } finally {
        browser.close();
      }
    }
  );
});
