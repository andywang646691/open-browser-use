"""obu exec runner — executes a task script *file* with the prelude.

The Go CLI writes this runner and script_helpers.py into a temp dir and
executes `python3 -u <runner> <target.py>` with OBU_EXEC_PRELUDE_DIR set.
Unlike the heredoc path (stdin concat, which reports tracebacks as <stdin>
with prelude-shifted line numbers), the target file is compiled with its real
path, so SyntaxError/RuntimeError tracebacks point at the file's own line
numbers — the file is a first-class citizen, exactly what the task workspace
workflow needs.

The prelude module's globals are injected wholesale (excluding dunders) so
the script sees the same surface as heredoc mode, including single-underscore
names like _SESSION_ID and _help.
"""

import os
import sys

# argv: [runner, target, args...] -> [target, args...]
target = sys.argv[1]
sys.argv = [target] + sys.argv[2:]

prelude_dir = os.environ.get("OBU_EXEC_PRELUDE_DIR", "")
if not prelude_dir:
    sys.stderr.write("obu exec: OBU_EXEC_PRELUDE_DIR is empty; run through `obu exec`, not directly\n")
    sys.exit(1)

sys.path.insert(0, prelude_dir)
import script_helpers as _obu_helpers  # noqa: E402

for _name in list(vars(_obu_helpers)):
    if not _name.startswith("__"):
        globals()[_name] = vars(_obu_helpers)[_name]
del _name, _obu_helpers

with open(target, encoding="utf-8") as _fh:
    _source = _fh.read()
exec(compile(_source, target, "exec"), globals())