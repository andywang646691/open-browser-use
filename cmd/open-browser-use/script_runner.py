"""Execute an OBU task as a Python file, with helpers pre-imported.

The Go CLI writes this runner and the prelude into a private temporary
folder. runpy supplies the task's __file__, __name__ and __main__ module,
while the task directory is first on sys.path for sibling imports. The
caller's working directory is intentionally unchanged.
"""

import os
import runpy
import sys


def main():
    # argv: [runner, target, args...] -> [target, args...]
    target = os.path.abspath(sys.argv[1])
    sys.argv = [target] + sys.argv[2:]
    prelude_dir = os.environ.get("OBU_EXEC_PRELUDE_DIR", "")
    if not prelude_dir:
        sys.exit("obu exec: OBU_EXEC_PRELUDE_DIR is empty; run through `obu exec`")

    sys.path.insert(0, prelude_dir)
    import script_helpers

    helpers = {name: value for name, value in vars(script_helpers).items()
               if not name.startswith("__")}
    sys.path.insert(0, os.path.dirname(target))
    runpy.run_path(target, init_globals=helpers, run_name="__main__")


if __name__ == "__main__":
    main()
