// session-inject-main.js — per-session MAIN-world, DOCUMENT_START injection.
//
// Registered at runtime by BrowserBackend.injectMainScript via
// chrome.scripting.registerContentScripts({ world: "MAIN", runAt:
// "DOCUMENT_START", matches: <session tab origins> }), and unregistered at
// finalize. It runs in the page's real JS world before any page script, so
// site code captures the patched APIs (e.g. a virtualized list stores our
// IntersectionObserver instead of the real one). Unlike
// Page.addScriptToEvaluateOnNewDocument it survives navigations naturally and
// is not subject to page CSP.
//
// MVP template ("visibility"): report the page as visible and make
// IntersectionObserver deliver immediate, always-intersecting callbacks, so
// visibility/IO-gated lazy loading works in the occluded/minimized session
// window without stealing focus.
(() => {
  const log = ["main-inject:v1"];
  const tryDef = (obj, prop, get) => {
    try {
      Object.defineProperty(obj, prop, { configurable: true, get });
      return true;
    } catch (e) {
      log.push(prop + ":" + e);
      return false;
    }
  };

  tryDef(Document.prototype, "visibilityState", () => "visible");
  tryDef(document, "visibilityState", () => "visible");

  try {
    window.IntersectionObserver = class {
      constructor(cb, opts) {
        this._cb = cb;
        this._opts = opts || {};
        this._targets = new Set();
      }
      observe(el) {
        this._targets.add(el);
        try {
          const r = el.getBoundingClientRect();
          this._cb(
            [
              {
                target: el,
                isIntersecting: true,
                intersectionRatio: 1,
                rootBounds: null,
                boundingClientRect: r,
                intersectionRect: r,
                time: performance.now()
              }
            ],
            this
          );
        } catch (e) {
          log.push("observe:" + e);
        }
      }
      unobserve(el) {
        this._targets.delete(el);
      }
      disconnect() {
        this._targets.clear();
      }
      takeRecords() {
        return [];
      }
    };
    log.push("io:mocked");
  } catch (e) {
    log.push("io:" + e);
  }

  window.__obuMainInject = { ok: true, log };
})();