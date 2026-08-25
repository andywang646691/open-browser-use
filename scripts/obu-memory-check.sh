#!/usr/bin/env bash
# 机械校验 site memory 存储的健康度。
# 用法: scripts/obu-memory-check.sh [根目录]
# 根目录缺省取 $OBU_MEMORY_DIR，其次 ~/.open-browser-use/memory。
set -uo pipefail

root="${1:-${OBU_MEMORY_DIR:-$HOME/.open-browser-use/memory}}"
if [[ ! -d "$root" ]]; then
  echo "memory 目录不存在: $root"
  exit 0
fi

errors=0
warn() { echo "✗ $*"; errors=$((errors + 1)); }

# 1) 原子行格式 + kind 合法
lines="$root/site-memory.lines"
if [[ -f "$lines" ]]; then
  while IFS= read -r line; do
    [[ -z "${line:-}" ]] && continue
    [[ "$line" != *"domain="* || "$line" != *"kind="* || "$line" != *"note="* ]] \
      && warn "site-memory.lines: 缺字段: $line"
    kind="$(printf '%s' "$line" | sed -n 's/.*|kind=\([^|]*\).*/\1/p')"
    case "$kind" in
      fail|surprise|tip) ;;
      *) warn "site-memory.lines: 非法 kind='${kind}': $line" ;;
    esac
  done < "$lines"
else
  warn "缺少一级文件 site-memory.lines"
fi

# 2) 临时 ref (@N / ref=) 不应留在任何存储文件里（易腐烂）
refs="$(grep -rEn '(@[0-9]+|\bref=[0-9]+)' "$root/site-memory.lines" \
  "$root/playbooks" "$root/techniques" "$root/runbooks" 2>/dev/null || true)"
if [[ -n "$refs" ]]; then
  warn "发现临时 ref（易腐烂）:"
  printf '%s\n' "$refs"
fi

# 3) 明显的密钥/私人链接
secrets="$(grep -rEni '(BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|password\s*=|secret\s*=|api[_-]?key\s*=|token\s*=)' \
  "$root/site-memory.lines" "$root/playbooks" "$root/techniques" "$root/runbooks" 2>/dev/null || true)"
if [[ -n "$secrets" ]]; then
  warn "可能泄露敏感信息:"
  printf '%s\n' "$secrets"
fi

# 4) 技术索引预算（全量召回成本）
n_tech="$(find "$root/techniques" -maxdepth 1 -name '*.md' 2>/dev/null | wc -l | tr -d ' ')"
if [[ -n "$n_tech" && "$n_tech" -gt 25 ]]; then
  warn "技术文件 $n_tech 个（预算 ≤25，超出会让全量索引召回变贵）"
fi

# 5) runbook header 必填（用途 + 验证）
if [[ -d "$root/runbooks" ]]; then
  for f in "$root"/runbooks/*.py; do
    [[ -e "$f" ]] || continue
    grep -qE '^# (用途|purpose|Verify|验证)' "$f" || warn "runbook $f 缺 header（用途必填）"
    if ! grep -qE '^# (验证|Verify)' "$f"; then
      warn "runbook $f 缺 header（验证判据必填）"
    fi
  done
fi

if [[ "$errors" -eq 0 ]]; then
  echo "✓ site memory 健康（techniques: ${n_tech:-0} 个模式）"
else
  echo "发现 $errors 处问题（用文件工具修复或按 references/memory.md 归档）"
  exit 1
fi
