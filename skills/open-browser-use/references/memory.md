# Site Memory

Site memory turns each OBU visit's hard-won experience into reusable data.
It lives as plain files — no memory commands. Read with your own file tools
(grep/find/read); write the atomic site row through `memory_put(...)` in a script.
All paths below are relative to the memory root
`~/.open-browser-use/memory/` (override with `$OBU_MEMORY_DIR`).

## Where it lives

```
~/.open-browser-use/memory/          # override root with $OBU_MEMORY_DIR
├── README.md                        # this rule, self-described at the store
├── site-memory.lines                # atomic rows (append-only, source of truth)
├── techniques/<name>.md             # cross-domain, reusable patterns
└── runbooks/<name>.py               # verified code, one-command rerun
```

Set `OBU_MEMORY_DIR` to a repo dir (e.g. `docs/wiki/sites/`) to share the
store with a team. The effective files are
`$OBU_MEMORY_DIR/site-memory.lines`, `$OBU_MEMORY_DIR/techniques/`, and
`$OBU_MEMORY_DIR/runbooks/`.

## Task workspace (parallel root)

Task scripts are a **parallel** workspace, not memory: every script for a
task lives at `~/.open-browser-use/tasks/<domain>/<slug>/` (`$OBU_TASK_DIR`
overrides) and is run with `obu exec <file>` (accurate tracebacks).
It's audit trail, not knowledge.
When a flow stabilizes across tasks, promote its verified script to a
runbook here (`runbooks/<name>.py`), then the task dir can be discarded.
`obu tasksizes` audits workspace growth (per-domain sizes, idle candidates).

## Core rule: fixed skill, growing data

Skill count stays 1. Experience grows only as data: site facts in atomic rows,
transferable patterns in techniques, and stable code in runbooks. Never create
a per-site skill, package, or new directory type. Write **only
"expensive" knowledge**: traps (`fail`), unexpected structure (`surprise`),
rare hard-won positive patterns (`tip`). A "map, not a diary": capture the
shape of the site, not your task narration.

## Tier 1 — atomic rows (site-memory.lines)

One labeled, pipe-separated row per fact. `|` is forbidden inside values.

```text
domain=x.com|ts=2026-08-25T10:00:00Z|kind=surprise|note=timeline is virtual-scroll, DOM renders only visible items|unlock=scrape network requests instead of scrolling|tech=virtual-scroll-throttle
domain=github.com|ts=2026-08-24T10:00:00Z|kind=fail|note=SSO second click redirected home|unlock=wait ~3s before clicking|selector=iframe[title="login"]
```

Fields (only `domain`/`kind`/`note` required): `domain` (normalized, `www.`
dropped) · `ts` · `kind` (`fail` | `surprise` | `tip`) · `note` · `unlock`
(required for `fail`/`surprise`) · `selector` · `tech` → techniques file ·
`runbook` → runbook file.

Lenient aliases (accepted instead of failing): `subject`/`body` → `note`,
`how` → `unlock`; unknown kwargs raise a TypeError that lists the valid
fields so the mistake is self-correcting.

### Write — inside a script

```python
memory_put("x.com", kind="surprise",
           note="timeline is virtual-scroll, DOM renders only visible items",
           unlock="scrape network requests instead of scrolling",
           tech="virtual-scroll-throttle")
```

- Python kwargs: no escaping; the helper validates `kind`, normalizes the
  domain, forbids `|`, and appends atomically (never overwrites).
- Don't remember the vocabulary — introspect: `help(memory_put)` at runtime.
- Outside a script (no OBU), append the same labeled line with your file
  tools; the reader is tolerant (field order/aliases/missing `ts` are OK).
- Never write credentials, private links, or anything covered by the OBU
  "don't touch cookies/passwords" rule.

## Tier 2 — techniques (cross-domain)

When an unlock transfers to other sites/tasks (background-tab throttling,
virtual scroll, iframes, anti-bot rhythm, network-scraping fallback), file it
as `techniques/<name>.md`: a named pattern with `关键词` (synonyms), 触发信号
(trigger signals), 成因 (cause), 解法 (solution), 来源站点 (origins).

One file per pattern. Before creating: compare against existing titles and
`关键词` once; if a same-name pattern exists, append only its origin. Pattern
count is bounded — that's what makes full recall cheap. The technique file is
separate from the site row; `tech=name` only links to it and does not create or
update it.

## Tier 3 — runbooks (verified code)

**Runbook**: code that took many lines to get right, saved because it
re-runs. Store the verified script as `runbooks/<name>.py` with a header:

  ```python
  # runbook: x-timeline-scrape
  # 用途: fetch latest OBU_MAX_POSTS timeline posts
  # 前置: logged in; needs a foreground tab
  # 验证: len(posts)==N and first ts is newest
  # 参数: OBU_MAX_POSTS (default 15)
  # 来源: 2026-08-25, switched from scrolling to network capture
  ```

  Rerun one-command: `obu exec runbooks/x-timeline-scrape.py`, then judge success
  against the `验证` line. A failing rerun means the runbook rotted — mark
  stale, fix, or delete. Only store code that actually ran through; don't
  store one-shot scripts (record a row / technique instead).

## Read — two triggers, native tools

1. **Before the first browser action for a domain in the current OBU task**:
   `grep "domain=<domain>|" <memory-root>/site-memory.lines` → its rows; read
   any linked `tech=` technique files and `runbook=` scripts.
2. **On repeated failure** (≥2 tries, or a silent failure: page normal but data
   missing / scrolling or events not firing): enumerate every
   `<memory-root>/techniques/*.md` and read its title + 关键词 + 触发信号
   before trying again, then do #1.
   Don't guess keywords — the bounded index is small enough to read fully.

## Mandatory memory checkpoint before the final response

Do not treat a prose explanation of difficulties as memory persistence. Before
writing the final response:

1. Review the task scripts and result for failed attempts, surprising structure,
   hard-won fixes, transferable techniques, or verified repeatable flows.
2. If none exists, write nothing. Otherwise persist each site-specific fact with
   `memory_put(...)`, and create/update the linked technique or runbook file.
3. Verify the resulting row/file with native file tools. Only persisted
   rows/files count as experience deposition.

Call `finalize_tabs()` exactly once as the last browser action. The checkpoint
may write/read memory files after finalization, but must not call browser helpers
again.

## Decision tree at wrap-up

```text
1. Any "expensive" knowledge?
   (trap / surprise / unlock / transferable fix / verified working code)
   no  → don't write (routine success isn't worth a row)
   yes → next
2. Which artifact?
   site-specific fact  → one atomic row in site-memory.lines
   transferable fix    → create/update techniques/<name>.md
   stable repeat flow  → create/update runbooks/<name>.py
3. Link artifacts with `tech=` or `runbook=` on the site row when applicable.
4. In an OBU script use `memory_put(...)`; elsewhere append a labeled line.
   `memory_put()` only writes site-memory.lines; use file tools for technique
   and runbook files. Unsure of format → read this file or the memory README.
```

## Anti-explosion & hygiene (mechanical)

- One domain = one key; never a per-site skill, package, or new directory type.
- `site-memory.lines` is the only atomic write entry; technique and runbook
  files are created only when their promotion criteria are met, after checking
  for duplicates.
- Stale: >90 days with no new rows/reads → archive. Runbook that fails rerun
  → mark stale.
- Optional check: `scripts/obu-memory-check.sh` (line format, `kind` validity,
  no temp refs `@N`/`ref=`, no secrets, techniques index budget, runbook
  headers). Tolerant reader + this check keep bad rows non-fatal.
