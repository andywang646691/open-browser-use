// Regression coverage for the existing extension-side target mapping used by
// Direct CDP extension lifecycle operations. The browser-root implementation
// is exercised by the dedicated Chrome smoke in the task history.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import test from "node:test";

const HERE = path.dirname(fileURLToPath(import.meta.url));

test("extension manifest retains the permissions required by OBU lifecycle control", async () => {
  const manifest = JSON.parse(await readFile(path.join(HERE, "manifest.json"), "utf8"));
  assert.equal(manifest.manifest_version, 3);
  assert.ok(manifest.permissions.includes("nativeMessaging"));
  assert.ok(manifest.permissions.includes("debugger"));
  assert.equal(manifest.background.service_worker, "background.js");
});

test("extension backend exposes exact target mapping for Direct CDP", async () => {
  const source = await readFile(path.join(HERE, "background.js"), "utf8");
  assert.match(source, /async getTargetMapping\(params\)/);
  assert.match(source, /targetId: match\.id/);
  assert.match(source, /this\.requireSessionTab\(params, "getTargetMapping"\)/);
});
