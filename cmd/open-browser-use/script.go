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
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

//go:embed script_helpers.py
var scriptHelpers string

//go:embed script_runner.py
var scriptRunner string

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
	cmd.Env = append(os.Environ(), scriptSubprocessEnv(options, socketPath, socketErr)...)
	if err := cmd.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return true, &scriptExitError{code: exitError.ExitCode()}
		}
		return true, err
	}
	return true, nil
}

// scriptSubprocessEnv builds the env that reaches the python subprocess in
// both heredoc and exec modes: socket routing plus explicit session id.
// The session id is only injected when non-empty so an env-level
// OBU_SESSION_ID (or its absence) still flows through as before, and an
// explicit CLI flag wins over the inherited environment.
func scriptSubprocessEnv(options socketOptions, socketPath string, socketErr error) []string {
	timeout := options.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	scriptEnv := []string{
		fmt.Sprintf("OBU_SCRIPT_TIMEOUT_SECONDS=%g", timeout.Seconds()),
		"OBU_SCRIPT_SOCKET_ERROR=",
	}
	if options.sessionID != "" {
		scriptEnv = append(scriptEnv, "OBU_SESSION_ID="+options.sessionID)
	}
	if socketErr != nil {
		scriptEnv = append(scriptEnv, "OBU_SCRIPT_SOCKET=", "OBU_SCRIPT_SOCKET_ERROR="+socketErr.Error())
	} else {
		scriptEnv = append(scriptEnv, "OBU_SCRIPT_SOCKET="+socketPath)
	}
	return scriptEnv
}

// runExecFile executes one task-script file with the prelude as an
// imported module (not stdin concat), so tracebacks report the file's own
// path and line numbers instead of <stdin> plus the prelude offset. The
// prelude and runner are written into a per-invocation temp dir; socket
// resolution is shared with the heredoc path via the same env contract.
// extraEnv merges over the inherited environment and the script subprocess
// env (used for the task's .obu.env); values win over inherited env but
// explicit flags still win — the caller has already folded file values into
// options only where flags were not set.
func runExecFile(path string, options socketOptions, extraEnv map[string]string, stdout io.Writer, stderr io.Writer, scriptArgs ...string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("exec requires a script file path")
	}
	scriptPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	payload, err := os.ReadFile(scriptPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(payload)) == "" {
		return errors.New("exec: " + scriptPath + " is empty")
	}
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		return errors.New("exec mode requires python3 on PATH (macOS/Linux ship it by default)")
	}
	tmpDir, err := os.MkdirTemp("", "obu-exec-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "script_helpers.py"), []byte(scriptHelpers), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "script_runner.py"), []byte(scriptRunner), 0o600); err != nil {
		return err
	}
	socketPath, socketErr := resolveScriptSocket(options)
	args := append([]string{"-u", filepath.Join(tmpDir, "script_runner.py"), scriptPath}, scriptArgs...)
	cmd := exec.Command(pythonPath, args...)
	// A task workspace is the execution context for its scripts. Use an
	// absolute target path so changing the child cwd cannot double-prefix a
	// relative script path, and make relative file access/task-local imports
	// resolve from the workspace rather than the caller's directory.
	cmd.Dir = filepath.Dir(scriptPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = mergeEnvVars(append(os.Environ(),
		append(scriptSubprocessEnv(options, socketPath, socketErr), "OBU_EXEC_PRELUDE_DIR="+tmpDir)...), extraEnv)
	if err := cmd.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &scriptExitError{code: exitError.ExitCode()}
		}
		return err
	}
	return nil
}

// mergeEnvVars returns env with overrides applied: keys in extra replace any
// prior occurrence, preserving an explicit flag priority already baked into
// the caller-provided options.
func mergeEnvVars(env []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(extra))
	seen := map[string]bool{}
	for _, kv := range env {
		key := kv
		if eq := strings.Index(kv, "="); eq >= 0 {
			key = kv[:eq]
		}
		if seen[key] {
			continue
		}
		if value, ok := extra[key]; ok {
			out = append(out, key+"="+value)
			seen[key] = true
			continue
		}
		out = append(out, kv)
	}
	for key, value := range extra {
		if !seen[key] {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// newExecCommand runs one task-script file with the prelude imported as
// a module, so tracebacks carry the file's own path and line numbers (the
// heredoc path concatenates the prelude and reports <stdin> with shifted
// lines). Scripts in the task workspace are executed this way.
// renderAPISheet prints the one-line helper sheet (script_helpers.py) by
// spawning python3 and running only the prelude's _api_sheet(); it never
// touches a socket or browser. Shared by `obu api-sheet` and `obu taskinit`.
func renderAPISheet(writer io.Writer) error {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		return errors.New("api-sheet requires python3 on PATH (macOS/Linux ship it by default)")
	}
	script := scriptHelpers + "\n_api_sheet()\n"
	run := exec.Command(pythonPath, "-u", "-")
	run.Stdin = strings.NewReader(script)
	run.Stdout = writer
	run.Stderr = io.Discard
	if err := run.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &scriptExitError{code: exitError.ExitCode()}
		}
		return err
	}
	return nil
}

// newApiSheetCommand prints the one-line helper sheet (script_helpers.py)
// without touching a socket or a browser: it spawns python3 and runs only
// the prelude's _api_sheet(). This gives a fresh agent the whole helper
// surface in one short read instead of spend turns introspecting the API.
func newApiSheetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "api-sheet",
		Short: "Print one line per helper: signature + docstring head",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return renderAPISheet(cmd.OutOrStdout())
		},
	}
}

func newExecCommand() *cobra.Command {
	var options socketOptions
	var envFile string
	cmd := &cobra.Command{
		Use:   "exec <file.py> [-- script-args...]",
		Short: "Run a task script file with accurate tracebacks",
		Long: `Run a Python task-script file with the obu helpers pre-imported.

Unlike the heredoc path (obu <<'PY', or obu < file), the script is executed
by its real file path: syntax and runtime errors report the file's own line
numbers, matching what your editor shows. __file__ names the script, sibling
modules can be imported, and arguments after -- reach sys.argv. The working
directory is automatically changed to the script's task workspace (its parent
directory). Helpers are the same (cdp, evaluate, open_tab, memory_put, ...);
discover them with help(name)/_help().

Env: the task's .obu.env (same directory as the script, written by "obu
taskinit") is loaded unless --env-file is given or the flag overrides it.
Explicit flags beat .obu.env; .obu.env beats the inherited environment.

Example:
  open-browser-use exec ~/.open-browser-use/tasks/x.com/fetch-0827/01_probe.py`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scriptPath := args[0]
			// .obu.env: explicit --env-file wins; otherwise the same directory
			// as the script (never an upward search — task-scoped means
			// anchored to the task directory).
			var fileEnv map[string]string
			if envFile != "" {
				parsed, err := parseEnvFile(envFile)
				if err != nil {
					return err
				}
				fileEnv = parsed
			} else {
				loaded, err := loadTaskEnv(scriptPath)
				if err != nil {
					return err
				}
				fileEnv = loaded
			}
			// Fold file values into options only where the flag was not set;
			// everything from the file also reaches the subprocess env. Keys
			// covered by an explicit flag are dropped from the file env so the
			// flag value (already in options) is the one the prelude sees.
			if !cmd.Flags().Changed("session-id") && fileEnv["OBU_SESSION_ID"] != "" {
				options.sessionID = fileEnv["OBU_SESSION_ID"]
			} else if cmd.Flags().Changed("session-id") {
				delete(fileEnv, "OBU_SESSION_ID")
			}
			if !cmd.Flags().Changed("browser") && fileEnv["OBU_BROWSER"] != "" {
				options.browser = fileEnv["OBU_BROWSER"]
			} else if cmd.Flags().Changed("browser") {
				delete(fileEnv, "OBU_BROWSER")
			}
			if !cmd.Flags().Changed("profile") && fileEnv["OBU_PROFILE"] != "" {
				options.profile = fileEnv["OBU_PROFILE"]
			} else if cmd.Flags().Changed("profile") {
				delete(fileEnv, "OBU_PROFILE")
			}
			return runExecFile(scriptPath, options, fileEnv, cmd.OutOrStdout(), cmd.ErrOrStderr(), args[1:]...)
		},
	}
	addSocketFlags(cmd, &options)
	cmd.Flags().StringVar(&envFile, "env-file", "", "explicit task env file (defaults to .obu.env next to the script)")
	return cmd
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
