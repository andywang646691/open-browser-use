package diaglog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var mu sync.Mutex

// Event appends one structured, local-only diagnostic event. Logging is best
// effort: diagnostics must never change browser automation behavior.
func Event(name string, fields map[string]any) {
	if name == "" {
		return
	}

	record := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"pid":     os.Getpid(),
		"process": "open-browser-use",
		"event":   name,
	}
	for key, value := range fields {
		record[key] = value
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}

	path, err := logPath()
	if err != nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_ = file.Chmod(0o600)
	_, _ = file.Write(append(payload, '\n'))
}

// Path returns the default diagnostic log location used by the native host
// and launcher helpers. OBU_LOG_FILE is intended for local debugging and
// overrides the default path when set.
func Path() (string, error) {
	return logPath()
}

func logPath() (string, error) {
	if path := os.Getenv("OBU_LOG_FILE"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "OpenBrowserUse", "native-host.log"), nil
	}
	return filepath.Join(home, ".local", "state", "open-browser-use", "native-host.log"), nil
}
