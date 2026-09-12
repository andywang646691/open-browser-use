package cdp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const MaxLevelDBScanBytes = 32 << 20

type BrowserProfileRoot struct {
	BrowserID       string
	BrowserName     string
	BrowserInstance string
	Root            string
}

func SupportedBrowserProfileRoots() ([]BrowserProfileRoot, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	switch runtime.GOOS {
	case "darwin":
		roots := []BrowserProfileRoot{
			{
				BrowserID:   "chrome",
				BrowserName: "Google Chrome",
				Root:        filepath.Join(home, "Library/Application Support/Google/Chrome"),
			},
			{
				BrowserID:   "chrome-beta",
				BrowserName: "Google Chrome Beta",
				Root:        filepath.Join(home, "Library/Application Support/Google/Chrome Beta"),
			},
		}
		bitBrowserRoots, err := BitBrowserProfileRoots(home)
		if err == nil {
			roots = append(roots, bitBrowserRoots...)
		}
		return roots, nil
	case "linux":
		return []BrowserProfileRoot{{
			BrowserID:   "chrome",
			BrowserName: "Google Chrome",
			Root:        filepath.Join(home, ".config/google-chrome"),
		}}, nil
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if strings.TrimSpace(localAppData) == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return []BrowserProfileRoot{
			{
				BrowserID:   "chrome",
				BrowserName: "Google Chrome",
				Root:        filepath.Join(localAppData, "Google", "Chrome", "User Data"),
			},
			{
				BrowserID:   "chrome-beta",
				BrowserName: "Google Chrome Beta",
				Root:        filepath.Join(localAppData, "Google", "Chrome Beta", "User Data"),
			},
		}, nil
	default:
		return nil, fmt.Errorf("browser profile detection is not implemented for %s", runtime.GOOS)
	}
}

func BitBrowserProfileRoots(home string) ([]BrowserProfileRoot, error) {
	cacheRoot := filepath.Join(home, "Library/Application Support/BitBrowser/BrowserCache")
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var roots []BrowserProfileRoot
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(cacheRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(root, "Local State")); err != nil {
			continue
		}
		roots = append(roots, BrowserProfileRoot{
			BrowserID:       "bitbrowser",
			BrowserName:     "BitBrowser",
			BrowserInstance: entry.Name(),
			Root:            root,
		})
	}
	return roots, nil
}

func DefaultChromeUserDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library/Application Support/Google/Chrome"), nil
	case "linux":
		return filepath.Join(home, ".config/google-chrome"), nil
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if strings.TrimSpace(localAppData) == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "Google", "Chrome", "User Data"), nil
	default:
		return "", fmt.Errorf("Chrome extension detection is not implemented for %s", runtime.GOOS)
	}
}

func ChromeProfileDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "Default" || strings.HasPrefix(name, "Profile ") {
			profiles = append(profiles, filepath.Join(root, name))
		}
	}
	return profiles, nil
}

func ChromeProfileDisplayNames(root string) map[string]string {
	payload, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil
	}
	out := make(map[string]string, len(state.Profile.InfoCache))
	for dir, info := range state.Profile.InfoCache {
		if name := strings.TrimSpace(info.Name); name != "" {
			out[dir] = name
		}
	}
	return out
}

func ResolveProfileForInstanceID(extensionID string, instanceID string) (browserID string, browserName string, browserInstance string, directory string, displayName string, rootDir string, ok bool) {
	if extensionID == "" || instanceID == "" {
		return "", "", "", "", "", "", false
	}
	roots, err := SupportedBrowserProfileRoots()
	if err != nil {
		return "", "", "", "", "", "", false
	}
	needle := []byte(instanceID)
	for _, root := range roots {
		profiles, err := ChromeProfileDirs(root.Root)
		if err != nil {
			continue
		}
		names := ChromeProfileDisplayNames(root.Root)
		for _, profileDir := range profiles {
			storageDir := filepath.Join(profileDir, "Local Extension Settings", extensionID)
			entries, err := os.ReadDir(storageDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".ldb") {
					continue
				}
				info, err := entry.Info()
				if err != nil || info.Size() > MaxLevelDBScanBytes {
					continue
				}
				payload, err := os.ReadFile(filepath.Join(storageDir, name))
				if err != nil {
					continue
				}
				if !bytes.Contains(payload, needle) {
					continue
				}
				dir := filepath.Base(profileDir)
				return root.BrowserID, root.BrowserName, root.BrowserInstance, dir, names[dir], root.Root, true
			}
		}
	}
	return "", "", "", "", "", "", false
}

// ResolveUserDataDir locates the user data dir:
// 1. OBU_USER_DATA_DIR env override if set
// 2. Exact root from extension instanceID
// 3. Default Chrome user data dir
func ResolveUserDataDir(extensionID, instanceID string) (string, error) {
	if envDir := os.Getenv("OBU_USER_DATA_DIR"); strings.TrimSpace(envDir) != "" {
		return envDir, nil
	}

	if extensionID != "" && instanceID != "" {
		_, _, _, _, _, root, ok := ResolveProfileForInstanceID(extensionID, instanceID)
		if ok && root != "" {
			return root, nil
		}
	}

	return DefaultChromeUserDataDir()
}

func FormatProfileDirSortKey(dir string) string {
	if dir == "Default" {
		return "0"
	}
	if strings.HasPrefix(dir, "Profile ") {
		suffix := strings.TrimPrefix(dir, "Profile ")
		if n, err := strconv.Atoi(suffix); err == nil {
			return fmt.Sprintf("1:%010d", n)
		}
	}
	return "2:" + dir
}
