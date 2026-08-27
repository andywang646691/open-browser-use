# Site Memory

Site memory turns each OBU visit's hard-won experience into reusable data.
It lives as plain files — no memory commands. Read with your own file tools
(grep/find/read); write through the `memory_put(...)` helper in a heredoc.

## Where it lives

```
~/.open-browser-use/memory/          # override root with $OBU_MEMORY_DIR
├── README.md                        # this rule, self-described at the store
├── site-memory.lines                # tier 1: atomic rows (append-only)
├── techniques/<name>.md             # cross-domain, reusable patterns
├── playbooks/<domain>.md            # tier 2: aggregated view (manual)
└── runbooks/<name>.py               # tier 3: verified code, one-command rerun
```

Set `OBU_MEMORY_DIR` to a repo dir (e.g. `docs/wiki/sites/`) to share the
store with a team.

## Core rule: fixed skill, growing data

Skill count stays 1. Experience grows only as data, keyed by domain (atomic
rows/playbooks) or by named pattern (techniques) or by stable task (runbooks).
Never create a per-site skill, package, or new directory type. Write **only
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

### Write — inside a heredoc

```python
memory_put("x.com", kind="surprise",
           note="timeline is virtual-scroll, DOM renders only visible items",
           unlock="scrape network requests instead of scrolling",
           tech="virtual-scroll-throttle")
```

- Python kwargs: no escaping; the helper validates `kind`, normalizes the
  domain, forbids `|`, and appends atomically (never overwrites).
- Don't remember the vocabulary — introspect: `help(memory_put)` at runtime.
- Outside a heredoc (no OBU), append the same labeled line with your file
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
count is bounded — that's what makes full recall cheap.

## Tier 3 — playbooks (aggregated) & runbooks (code)

- **Playbook**: once the same domain has ≥2 rows, the agent may aggregate them
  into `playbooks/<domain>.md` (entry/nav/selectors/traps/verify). Rows stay;
  the playbook is a view. Threshold keeps the doc set small.
- **Runbook**: code that took many lines to get right, saved because it
  re-runs. Store the verified script as `runbooks/<name>.py` with a header:

  ```python
  # runbook: x-timeline-scrape
  # 用途: fetch latest OBU_MAX_POSTS timeline posts
  # 前置: logged in; needs a foreground tab
  # 验证: len(posts)==N and first ts is newest
  # 参数: OBU_MAX_POSTS (default 15)
  # 来源: 2026-08-25, switched from scrolling to network capture
  ```

  Rerun one-command: `obu < runbooks/x-timeline-scrape.py`, then judge success
  against the `验证` line. A failing rerun means the runbook rotted — mark
  stale, fix, or delete. Only store code that actually ran through; don't
  store one-shot scripts (record a row / technique instead).

## Read — two triggers, native tools

1. **Before a domain new to this session**: `grep "domain=<domain>|" site-memory.lines`
   → its rows; read `playbooks/<domain>.md`, `runbooks/` hits, and any `tech=`
   linked techniques.
2. **On repeated failure** (≥2 tries, or a silent failure: page normal but data
   missing / scrolling or events not firing): unconditionally read the whole
   techniques index first (title + 关键词 + 触发信号 per file), then do #1.
   Don't guess keywords — the bounded index is small enough to read fully.

## Decision tree at wrap-up

```text
1. Any "expensive" knowledge? (trap / surprise / unlock / transferable fix / working code)
   no  → don't write (routine success isn't worth a row)
   yes → next
2. Which tier?
   site-specific fact  → atomic row (site-memory.lines)
   transferable fix    → techniques/<name>.md  (关键词+触发信号+成因+解法)
   stable repeat task  → runbooks/<name>.py    (verified code + header)
   domain ≥2 hits      → aggregate rows into playbooks/<domain>.md
3. How? In heredoc: memory_put(...). Elsewhere: append a labeled line.
   Unsure of format → read this file or the store's README.
```

## Anti-explosion & hygiene (mechanical)

- One domain = one key; never a per-site skill or new directory type.
- `site-memory.lines` is the only write entry; playbook/technique only via the
  templates above, with a ≥2 threshold / dedup check.
- Stale: >90 days with no new rows/reads → archive. Runbook that fails rerun
  → mark stale.
- Optional check: `scripts/obu-memory-check.sh` (line format, `kind` validity,
  no temp refs `@N`/`ref=`, no secrets, techniques index budget, runbook
  headers). Tolerant reader + this check keep bad rows non-fatal.
