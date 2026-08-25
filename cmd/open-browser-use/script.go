// Script mode: real-Python heredoc browser scripting for `obu <<'PY' ... PY`.
//
// The CLI reads the script from stdin and executes it with a real python3
// interpreter (system python3 on macOS/Linux). The embedded prelude
// (script_helpers.py) pre-imports browser helpers whose API contract lives
// in their docstrings: inside a script, call help(Name) or _help() to
// introspect signatures and return shapes.
//
// The Go side resolves the target socket (--socket / --socket-dir / OBU_*
// env / active registry / profile routing) and hands the result to the
// prelude via OBU_SCRIPT_SOCKET; the prelude speaks the native Browser Use
// JSON-RPC frame protocol directly. stdout/stderr pass through untouched, so
// print() and tracebacks behave like plain Python, and the Python exit code
// is propagated to the CLI.

package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
)

//go:embed script_helpers.py
var scriptHelpers string

// scriptExitError carries the python3 exit code when the script exited
// nonzero; stderr was already passed through, so nothing is printed for it.
type scriptExitError struct {
	code int
}

func (e *scriptExitError) Error() string {
	return fmt.Sprintf("script exited with code %d", e.code)
}

// runPipedScript executes a Python-style script read from reader using a real
// python3 interpreter. It reports handled=false when the reader is an
// interactive terminal or has no content, so callers can fall back to their
// normal no-argument behavior.
func runPipedScript(reader io.Reader, options socketOptions, stdout io.Writer, stderr io.Writer) (bool, error) {
	if file, ok := reader.(*os.File); ok {
		stat, err := file.Stat()
		if err == nil && stat.Mode()&os.ModeCharDevice != 0 {
			return false, nil
		}
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(payload)) == "" {
		return false, nil
	}
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		return true, errors.New("heredoc script mode requires python3 on PATH (macOS/Linux ship it by default)")
	}
	socketPath, socketErr := resolveScriptSocket(options)
	script := scriptHelpers + "\n" + string(payload)
	cmd := exec.Command(pythonPath, "-u", "-")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Socket resolution reuses the CLI's discovery (registry, scan, profile
	// routing). Pass the resolved path to the prelude; a resolution error is
	// deferred so pure-Python scripts (print/arithmetic) still run and only
	// helper calls fail, with the original Go-side error message.
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("OBU_SCRIPT_TIMEOUT_SECONDS=%d", int(options.timeout.Seconds())),
	)
	if socketErr != nil {
		cmd.Env = append(cmd.Env, "OBU_SCRIPT_SOCKET=", "OBU_SCRIPT_SOCKET_ERROR="+socketErr.Error())
	} else {
		cmd.Env = append(cmd.Env, "OBU_SCRIPT_SOCKET="+socketPath)
	}
	if err := cmd.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return true, &scriptExitError{code: exitError.ExitCode()}
		}
		return true, err
	}
	return true, nil
}

// resolveScriptSocket reuses the CLI's existing socket discovery (explicit
// --socket, active registry, socket-dir scan, browser/profile routing) and
// returns the concrete Unix socket path for the Python prelude to connect to.
func resolveScriptSocket(options socketOptions) (string, error) {
	conn, err := dialBrowserSocketForProfile(options.socketPath, options.socketDir, options.browser, options.profile, options.timeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return "", fmt.Errorf("resolved connection is not a Unix socket: %T", conn)
	}
	return unixConn.RemoteAddr().String(), nil
}
