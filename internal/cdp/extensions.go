package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ExtensionInfo describes an unpacked Chrome extension reported by the CDP
// Extensions domain. Chrome does not expose Web Store extensions through this
// management API, so this type deliberately models unpacked extensions only.
type ExtensionInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// ListExtensions returns the unpacked extensions visible to the browser-root
// CDP connection.
func (m *Manager) ListExtensions(ctx context.Context, timeout time.Duration) ([]ExtensionInfo, error) {
	raw, err := m.ExecuteCdp(ctx, "", "", 0, "Extensions.getExtensions", map[string]any{}, timeout)
	if err != nil {
		return nil, err
	}
	var response struct {
		Extensions []ExtensionInfo `json:"extensions"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode Extensions.getExtensions response: %w", err)
	}
	return response.Extensions, nil
}

// LoadUnpackedExtension installs an unpacked extension from an absolute local
// directory and returns the extension id assigned by Chrome.
func (m *Manager) LoadUnpackedExtension(ctx context.Context, path string, enableInIncognito bool, timeout time.Duration) (string, error) {
	params := map[string]any{"path": path}
	if enableInIncognito {
		params["enableInIncognito"] = true
	}
	raw, err := m.ExecuteCdp(ctx, "", "", 0, "Extensions.loadUnpacked", params, timeout)
	if err != nil {
		return "", err
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("decode Extensions.loadUnpacked response: %w", err)
	}
	if strings.TrimSpace(response.ID) == "" {
		return "", errors.New("Extensions.loadUnpacked returned an empty extension id")
	}
	return response.ID, nil
}

// UninstallExtension removes an unpacked extension by id.
func (m *Manager) UninstallExtension(ctx context.Context, id string, timeout time.Duration) error {
	_, err := m.ExecuteCdp(ctx, "", "", 0, "Extensions.uninstall", map[string]any{"id": id}, timeout)
	return err
}

// TriggerExtensionAction runs an extension's default action for a page target.
// Chrome's Extensions.triggerAction requires a synthetic type=tab target,
// while OBU's exact tab mapping identifies the child page target. The manager
// enables target discovery, attaches to each discovered tab target, and uses
// the resulting flat CDP session event to correlate the page target by id.
func (m *Manager) TriggerExtensionAction(ctx context.Context, id, pageTargetID string, timeout time.Duration) error {
	if strings.TrimSpace(pageTargetID) == "" {
		return errors.New("trigger extension action requires a target id")
	}
	if timeout <= 0 {
		timeout = m.config.CDPTimeout
	}
	deadline := time.Now().Add(timeout)

	// A previous action on this manager may already have learned the exact
	// page->tab relationship. Reuse it without opening another tab session.
	if tabTargetID := m.tabTargetForPage(pageTargetID); tabTargetID != "" {
		return m.triggerExtensionActionWithTarget(ctx, id, tabTargetID, time.Until(deadline))
	}

	if _, err := m.sendBrowserCommand(ctx, "Target.setDiscoverTargets", map[string]any{"discover": true}, "", time.Until(deadline)); err != nil {
		return fmt.Errorf("enable target discovery for extension action: %w", err)
	}

	// Target.getTargets does not consistently include the synthetic tab target
	// until discovery has had a chance to publish it. Retry the exact lookup
	// while target events are being processed by Manager.handleCDPEvent.
	attemptedTabs := make(map[string]bool)
	var temporaryTabSessions []string
	defer func() {
		for _, sessionID := range temporaryTabSessions {
			m.detachTemporaryTabSession(sessionID)
		}
	}()

	for time.Now().Before(deadline) {
		if tabTargetID := m.tabTargetForPage(pageTargetID); tabTargetID != "" {
			return m.triggerExtensionActionWithTarget(ctx, id, tabTargetID, time.Until(deadline))
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		raw, err := m.sendBrowserCommand(ctx, "Target.getTargets", map[string]any{"filter": []any{map[string]any{}}}, "", remaining)
		if err != nil {
			return fmt.Errorf("find tab target for extension action: %w", err)
		}
		var response struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
			} `json:"targetInfos"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("decode Target.getTargets response: %w", err)
		}

		for _, candidate := range response.TargetInfos {
			if candidate.Type != "tab" || candidate.TargetID == "" || attemptedTabs[candidate.TargetID] {
				continue
			}
			attemptedTabs[candidate.TargetID] = true
			remaining = time.Until(deadline)
			if remaining <= 0 {
				break
			}

			attachRaw, err := m.sendBrowserCommand(ctx, "Target.attachToTarget", map[string]any{
				"targetId": candidate.TargetID,
				"flatten":  true,
			}, "", remaining)
			if err != nil {
				continue
			}
			var attachResponse struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(attachRaw, &attachResponse); err != nil || attachResponse.SessionID == "" {
				continue
			}
			temporaryTabSessions = append(temporaryTabSessions, attachResponse.SessionID)
			m.rememberTabSession(attachResponse.SessionID, candidate.TargetID)

			if _, err := m.sendBrowserCommand(ctx, "Target.setAutoAttach", map[string]any{
				"autoAttach":             true,
				"waitForDebuggerOnStart": false,
				"flatten":                true,
			}, attachResponse.SessionID, time.Until(deadline)); err != nil {
				continue
			}
			if tabTargetID := m.tabTargetForPage(pageTargetID); tabTargetID != "" {
				return m.triggerExtensionActionWithTarget(ctx, id, tabTargetID, time.Until(deadline))
			}
		}

		// A child page attachment may arrive just after the root response. Wait
		// for a target event and then repeat the exact lookup. The normal event
		// broadcaster still receives every event independently.
		remaining = time.Until(deadline)
		if remaining <= 0 {
			break
		}
		waitCtx, cancel := context.WithTimeout(ctx, minDuration(remaining, 100*time.Millisecond))
		_, waitErr := m.waitForEvent(waitCtx, func(event RawEvent) bool {
			return event.Method == "Target.attachedToTarget" || event.Method == "Target.targetCreated"
		})
		cancel()
		if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
			return waitErr
		}
	}
	return fmt.Errorf("no tab target found for page target %q", pageTargetID)
}

func (m *Manager) sendBrowserCommand(ctx context.Context, method string, params any, sessionID string, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil, errors.New("direct CDP client is not connected")
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return client.Send(commandCtx, method, params, sessionID)
}

func (m *Manager) rememberTabSession(sessionID, tabTargetID string) {
	m.tabTargetsMu.Lock()
	m.tabTargets[tabTargetID] = struct{}{}
	m.tabSessions[sessionID] = tabTargetID
	m.tabTargetsMu.Unlock()
}

func (m *Manager) detachTemporaryTabSession(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = m.sendBrowserCommand(ctx, "Target.detachFromTarget", map[string]any{"sessionId": sessionID}, "", time.Second)
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func (m *Manager) triggerExtensionActionWithTarget(ctx context.Context, id, tabTargetID string, timeout time.Duration) error {
	_, err := m.ExecuteCdp(ctx, "", "", 0, "Extensions.triggerAction", map[string]any{
		"id":       id,
		"targetId": tabTargetID,
	}, timeout)
	return err
}

// ReloadExtension finds an installed unpacked extension and loads its current
// directory again, returning the id reported by Chrome.
func (m *Manager) ReloadExtension(ctx context.Context, id string, timeout time.Duration) (string, error) {
	extensions, err := m.ListExtensions(ctx, timeout)
	if err != nil {
		return "", err
	}
	for _, extension := range extensions {
		if extension.ID == id {
			if strings.TrimSpace(extension.Path) == "" {
				return "", fmt.Errorf("extension %q has no unpacked path", id)
			}
			return m.LoadUnpackedExtension(ctx, extension.Path, false, timeout)
		}
	}
	return "", fmt.Errorf("extension %q not found", id)
}
