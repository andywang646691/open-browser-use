(() => {
  const se = Application("System Events");
  const procs = se.applicationProcesses.whose({ name: "Google Chrome" });
  if (procs.length === 0) return "not-found";
  const proc = procs[0];

  function read(fn) {
    try { return String(fn() || "").toLowerCase(); } catch (e) { return ""; }
  }

  function walk(root, limit) {
    const queue = [root];
    const out = [];
    while (queue.length > 0 && out.length < limit) {
      const elem = queue.shift();
      out.push(elem);
      if (read(() => elem.role()) === "axwebarea") continue;
      let children = [];
      try { children = elem.uiElements(); } catch (e) {}
      for (const child of children) queue.push(child);
    }
    return out;
  }

  function press(btn) {
    try {
      btn.actions.byName("AXPress").perform();
      return true;
    } catch (e) {}
    try {
      btn.click();
      return true;
    } catch (e) {}
    return false;
  }

  for (const win of proc.windows()) {
    const elements = walk(win, 600);
    for (const dialog of elements) {
      if (read(() => dialog.subrole()) !== "axapplicationalertdialog") continue;
      const subtree = walk(dialog, 250);
      const text = subtree.map(elem =>
        read(() => elem.title()) + " " +
        read(() => elem.description()) + " " +
        read(() => elem.value())
      ).join(" ");
      if (!(text.includes("chrome 正受到自动测试软件的控制") ||
            text.includes("controlled by automated test software") ||
            text.includes("信息栏") || text.includes("infobar"))) {
        continue;
      }
      for (const btn of subtree) {
        if (read(() => btn.role()) !== "axbutton") continue;
        const combined = (read(() => btn.title()) + " " + read(() => btn.description())).trim();
        if (combined.includes("设置") || combined.includes("setting") ||
            combined.includes("disable") || combined.includes("turn off")) {
          continue;
        }
        const subrole = read(() => btn.subrole());
        if (subrole === "axclosebutton" || combined === "close" || combined === "关闭" ||
            combined === "x" || combined === "✕") {
          if (press(btn)) return "dismissed";
        }
      }
    }
  }
  return "not-found";
})()
