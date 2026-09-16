package cdp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type TargetMapper interface {
	GetTargetMapping(ctx context.Context, sessionID, turnID string, tabID int) (targetID string, err error)
}

type EventBroadcaster interface {
	BroadcastNotification(method string, params any)
}

type SessionKey struct {
	SessionID string
	TabID     int
}

type DirectSession struct {
	SessionID    string
	TabID        int
	TargetID     string
	CDPSessionID string
	AttachedAt   time.Time
}

type ManagerConfig struct {
	WSURL              string
	Mapper             TargetMapper
	Broadcaster        EventBroadcaster
	ConnectionObserver func(connected bool)
	AttachTimeout      time.Duration
	WakeTimeout        time.Duration
	CDPTimeout         time.Duration
}

type eventWaiter struct {
	predicate func(RawEvent) bool
	ch        chan RawEvent
	done      chan struct{}
	once      sync.Once
}

type fileChooserWaiter struct {
	tabID   int
	ch      chan fileChooserEvent
	timeout time.Time
}

type fileChooserEvent struct {
	Mode          string
	BackendNodeID int
}

type activeFileChooser struct {
	tabID         int
	sessionID     string
	cdpSessionID  string
	backendNodeID int
	mode          string
	isMultiple    bool
}

type Manager struct {
	config         ManagerConfig
	client         *Client
	mu             sync.RWMutex
	sessions       map[SessionKey]*DirectSession
	byCDPSessID    map[string]*DirectSession
	closed         bool
	connStateMu    sync.Mutex
	connStateKnown bool
	connState      bool

	// Temporary browser-root target discovery waits (used by extension action
	// triggering to resolve Chrome's synthetic type=tab target).
	eventWaitMu   sync.Mutex
	eventWaiters  map[uint64]*eventWaiter
	nextEventWait uint64
	tabTargetsMu  sync.RWMutex
	tabTargets    map[string]struct{}
	tabSessions   map[string]string // CDP tab-session id -> synthetic tab target id
	pageToTab     map[string]string // page target id -> synthetic tab target id

	// File chooser handling
	fcMu          sync.Mutex
	fcWaiters     map[int]*fileChooserWaiter
	activeChooser map[string]*activeFileChooser
}

func NewManager(config ManagerConfig) *Manager {
	if config.AttachTimeout <= 0 {
		config.AttachTimeout = 6 * time.Second
	}
	if config.WakeTimeout <= 0 {
		config.WakeTimeout = 3 * time.Second
	}
	if config.CDPTimeout <= 0 {
		config.CDPTimeout = 10 * time.Second
	}
	m := &Manager{
		config:        config,
		sessions:      make(map[SessionKey]*DirectSession),
		byCDPSessID:   make(map[string]*DirectSession),
		fcWaiters:     make(map[int]*fileChooserWaiter),
		activeChooser: make(map[string]*activeFileChooser),
		eventWaiters:  make(map[uint64]*eventWaiter),
		tabTargets:    make(map[string]struct{}),
		tabSessions:   make(map[string]string),
		pageToTab:     make(map[string]string),
	}
	return m
}

// Connect dials the browser root WebSocket. For personal Chrome, the handshake
// may remain parked while Chrome waits for the per-session remote-debugging
// approval. The launcher/client owns that user-facing approval flow; the
// native host deliberately does not drive macOS Accessibility itself.
func (m *Manager) Connect(ctx context.Context) error {
	client, err := Dial(ctx, m.config.WSURL, m.handleCDPEvent)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.client = client
	m.closed = false
	m.mu.Unlock()
	m.reportConnectionState(true)
	go func(current *Client) {
		<-current.Done()
		m.mu.RLock()
		isCurrent := m.client == current
		m.mu.RUnlock()
		if isCurrent {
			m.reportConnectionState(false)
		}
	}(client)
	return nil
}

func (m *Manager) reportConnectionState(connected bool) {
	observer := m.config.ConnectionObserver
	if observer == nil {
		return
	}
	m.connStateMu.Lock()
	if m.connStateKnown && m.connState == connected {
		m.connStateMu.Unlock()
		return
	}
	m.connStateKnown = true
	m.connState = connected
	m.connStateMu.Unlock()
	observer(connected)
}

func (m *Manager) IsConnected() bool {
	m.mu.RLock()
	if m.closed || m.client == nil {
		m.mu.RUnlock()
		return false
	}
	client := m.client
	m.mu.RUnlock()
	return !client.IsClosed()
}

// SetClient allows injecting an existing client (useful in unit tests).
func (m *Manager) SetClient(client *Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = client
}

func (m *Manager) handleCDPEvent(raw RawEvent) {
	m.recordTabTargetEvent(raw)

	m.eventWaitMu.Lock()
	for id, waiter := range m.eventWaiters {
		if waiter != nil && waiter.predicate != nil && waiter.predicate(raw) {
			delete(m.eventWaiters, id)
			select {
			case waiter.ch <- raw:
			default:
			}
			break
		}
	}
	m.eventWaitMu.Unlock()

	m.mu.RLock()
	var session *DirectSession
	if raw.SessionID != "" {
		session = m.byCDPSessID[raw.SessionID]
	}
	m.mu.RUnlock()

	// Handle Page.fileChooserOpened
	if raw.Method == "Page.fileChooserOpened" && session != nil {
		var payload struct {
			Mode          string `json:"mode"`
			BackendNodeID int    `json:"backendNodeId"`
		}
		_ = json.Unmarshal(raw.Params, &payload)
		m.fcMu.Lock()
		waiter, ok := m.fcWaiters[session.TabID]
		if ok && waiter != nil {
			delete(m.fcWaiters, session.TabID)
			select {
			case waiter.ch <- fileChooserEvent{Mode: payload.Mode, BackendNodeID: payload.BackendNodeID}:
			default:
			}
		}
		m.fcMu.Unlock()
	}

	// If Page.frameNavigated arrived on a main frame, reapply background wake asynchronously
	if raw.Method == "Page.frameNavigated" && session != nil {
		if isMainFrameNavigated(raw.Params) {
			cdpSessID := session.CDPSessionID
			go func() {
				wakeCtx, cancel := context.WithTimeout(context.Background(), m.config.WakeTimeout)
				defer cancel()
				m.applyBackgroundWake(wakeCtx, cdpSessID)
			}()
		}
	}

	// Forward in existing onCDPEvent notification shape
	if m.config.Broadcaster != nil {
		source := map[string]any{}
		if session != nil {
			source["tabId"] = session.TabID
		}
		var parsedParams any
		if len(raw.Params) > 0 {
			_ = json.Unmarshal(raw.Params, &parsedParams)
		}
		m.config.Broadcaster.BroadcastNotification("onCDPEvent", map[string]any{
			"source": source,
			"method": raw.Method,
			"params": parsedParams,
		})
	}
}

func (m *Manager) registerEventWaiter(predicate func(RawEvent) bool) (uint64, *eventWaiter) {
	m.eventWaitMu.Lock()
	defer m.eventWaitMu.Unlock()
	m.nextEventWait++
	id := m.nextEventWait
	waiter := &eventWaiter{
		predicate: predicate,
		ch:        make(chan RawEvent, 1),
		done:      make(chan struct{}),
	}
	m.eventWaiters[id] = waiter
	return id, waiter
}

func (m *Manager) removeEventWaiter(id uint64) {
	m.eventWaitMu.Lock()
	waiter, ok := m.eventWaiters[id]
	if ok {
		delete(m.eventWaiters, id)
	}
	m.eventWaitMu.Unlock()
	if ok {
		waiter.once.Do(func() { close(waiter.done) })
	}
}

func (m *Manager) recordTabTargetEvent(raw RawEvent) {
	if raw.Method != "Target.targetCreated" && raw.Method != "Target.attachedToTarget" && raw.Method != "Target.detachedFromTarget" {
		return
	}
	var payload struct {
		TargetInfo *struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfo"`
		SessionID string `json:"sessionId"`
		TargetID  string `json:"targetId"`
	}
	if json.Unmarshal(raw.Params, &payload) != nil {
		return
	}

	m.tabTargetsMu.Lock()
	defer m.tabTargetsMu.Unlock()
	switch raw.Method {
	case "Target.targetCreated":
		if payload.TargetInfo != nil && payload.TargetInfo.Type == "tab" && payload.TargetInfo.TargetID != "" {
			m.tabTargets[payload.TargetInfo.TargetID] = struct{}{}
		}
	case "Target.attachedToTarget":
		if payload.TargetInfo == nil || payload.TargetInfo.TargetID == "" {
			return
		}
		if payload.TargetInfo.Type == "tab" && payload.SessionID != "" {
			m.tabTargets[payload.TargetInfo.TargetID] = struct{}{}
			m.tabSessions[payload.SessionID] = payload.TargetInfo.TargetID
		}
		if raw.SessionID != "" {
			if tabTargetID, ok := m.tabSessions[raw.SessionID]; ok && payload.TargetInfo.Type == "page" {
				m.pageToTab[payload.TargetInfo.TargetID] = tabTargetID
			}
		}

	case "Target.detachedFromTarget":
		if payload.SessionID == "" {
			return
		}
		if tabTargetID, ok := m.tabSessions[payload.SessionID]; ok {
			delete(m.tabSessions, payload.SessionID)
			for pageTargetID, candidate := range m.pageToTab {
				if candidate == tabTargetID {
					delete(m.pageToTab, pageTargetID)
				}
			}
		}
	}
}

func (m *Manager) knownTabTarget(targetID string) bool {
	m.tabTargetsMu.RLock()
	_, ok := m.tabTargets[targetID]
	m.tabTargetsMu.RUnlock()
	return ok
}

func (m *Manager) tabTargetForPage(pageTargetID string) string {
	m.tabTargetsMu.RLock()
	targetID := m.pageToTab[pageTargetID]
	m.tabTargetsMu.RUnlock()
	return targetID
}

func (m *Manager) waitForEvent(ctx context.Context, predicate func(RawEvent) bool) (RawEvent, error) {
	id, waiter := m.registerEventWaiter(predicate)
	defer m.removeEventWaiter(id)

	select {
	case event := <-waiter.ch:
		return event, nil
	case <-waiter.done:
		return RawEvent{}, errors.New("CDP manager closed while waiting for target event")
	case <-ctx.Done():
		return RawEvent{}, ctx.Err()
	}
}

func isMainFrameNavigated(paramsJSON json.RawMessage) bool {
	if len(paramsJSON) == 0 {
		return false
	}
	var payload struct {
		Frame struct {
			ParentID string `json:"parentId"`
		} `json:"frame"`
	}
	if err := json.Unmarshal(paramsJSON, &payload); err != nil {
		return false
	}
	return payload.Frame.ParentID == ""
}

// Attach attaches to the specified tab using Target.attachToTarget(flatten=true),
// applies background wake (Page.setWebLifecycleState + Emulation.setFocusEmulationEnabled),
// and returns wake status.
func (m *Manager) Attach(ctx context.Context, sessionID, turnID string, tabID int) (map[string]any, error) {
	key := SessionKey{SessionID: sessionID, TabID: tabID}

	m.mu.RLock()
	existing, ok := m.sessions[key]
	m.mu.RUnlock()
	if ok && existing != nil {
		return map[string]any{
			"wake": map[string]any{
				"focus":     map[string]any{"ok": true},
				"lifecycle": map[string]any{"ok": true},
			},
		}, nil
	}

	if m.config.Mapper == nil {
		return nil, errors.New("cannot attach direct CDP: target mapper is not configured")
	}

	// 1. Get exact targetId from extension (verifies ownership and reloads discarded tabs)
	targetID, err := m.config.Mapper.GetTargetMapping(ctx, sessionID, turnID, tabID)
	if err != nil {
		return nil, fmt.Errorf("target mapping for tab %d: %w", tabID, err)
	}

	// 2. Attach to target over root WebSocket
	cdpSessionID, err := m.attachToTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}

	session := &DirectSession{
		SessionID:    sessionID,
		TabID:        tabID,
		TargetID:     targetID,
		CDPSessionID: cdpSessionID,
		AttachedAt:   time.Now(),
	}

	m.mu.Lock()
	m.sessions[key] = session
	m.byCDPSessID[cdpSessionID] = session
	m.mu.Unlock()

	// 3. Apply background wake (lifecycle active first, focus emulation second)
	wakeResult := m.applyBackgroundWake(ctx, cdpSessionID)

	return map[string]any{"wake": wakeResult}, nil
}

func (m *Manager) attachToTarget(ctx context.Context, targetID string) (string, error) {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return "", errors.New("direct CDP client is not connected")
	}

	attachCtx, cancel := context.WithTimeout(ctx, m.config.AttachTimeout)
	defer cancel()

	type attachParams struct {
		TargetID string `json:"targetId"`
		Flatten  bool   `json:"flatten"`
	}

	lateHandler := func(result json.RawMessage, cdpErr *CDPError) {
		if cdpErr != nil || len(result) == 0 {
			return
		}
		var lateResp struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(result, &lateResp); err == nil && lateResp.SessionID != "" {
			go func(lateSessionID string) {
				detachCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, _ = client.Send(detachCtx, "Target.detachFromTarget", map[string]any{
					"sessionId": lateSessionID,
				}, "")
			}(lateResp.SessionID)
		}
	}

	respRaw, err := client.SendWithLateHandler(attachCtx, "Target.attachToTarget", attachParams{
		TargetID: targetID,
		Flatten:  true,
	}, "", lateHandler)

	if err != nil {
		return "", fmt.Errorf("attach to target %s timed out or failed: %w", targetID, err)
	}

	var attachResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(respRaw, &attachResp); err != nil || attachResp.SessionID == "" {
		return "", fmt.Errorf("invalid attach response for target %s: %w", targetID, err)
	}

	return attachResp.SessionID, nil
}

func (m *Manager) applyBackgroundWake(ctx context.Context, cdpSessionID string) map[string]any {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return map[string]any{"skipped": "not-connected"}
	}

	wakeCtx, cancel := context.WithTimeout(ctx, m.config.WakeTimeout)
	defer cancel()

	// 1. Page.setWebLifecycleState active first
	lifecycleRes := map[string]any{"ok": true}
	_, err := client.Send(wakeCtx, "Page.setWebLifecycleState", map[string]any{"state": "active"}, cdpSessionID)
	if err != nil {
		lifecycleRes = map[string]any{"ok": false, "error": err.Error()}
	}

	// 2. Emulation.setFocusEmulationEnabled second
	focusRes := map[string]any{"ok": true}
	_, err = client.Send(wakeCtx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true}, cdpSessionID)
	if err != nil {
		focusRes = map[string]any{"ok": false, "error": err.Error()}
	}

	return map[string]any{
		"lifecycle": lifecycleRes,
		"focus":     focusRes,
	}
}

// ExecuteCdp executes a CDP method. If tabID > 0, it routes to that tab's direct session,
// lazily attaching if not yet attached. If tabID <= 0, it executes over root browser session.
func (m *Manager) ExecuteCdp(ctx context.Context, sessionID, turnID string, tabID int, method string, commandParams any, timeout time.Duration) (json.RawMessage, error) {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil, errors.New("direct CDP client is not connected")
	}

	if timeout <= 0 {
		timeout = m.config.CDPTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Browser-wide commands go over root session
	if tabID <= 0 {
		return client.Send(cmdCtx, method, commandParams, "")
	}

	// Tab-specific command: ensure attached
	session, err := m.ensureSession(ctx, sessionID, turnID, tabID)
	if err != nil {
		return nil, err
	}

	return client.Send(cmdCtx, method, commandParams, session.CDPSessionID)
}

func (m *Manager) ensureSession(ctx context.Context, sessionID, turnID string, tabID int) (*DirectSession, error) {
	key := SessionKey{SessionID: sessionID, TabID: tabID}
	m.mu.RLock()
	session := m.sessions[key]
	m.mu.RUnlock()

	if session != nil {
		return session, nil
	}

	// Lazily attach
	_, err := m.Attach(ctx, sessionID, turnID, tabID)
	if err != nil {
		return nil, fmt.Errorf("lazy direct attach failed: %w", err)
	}
	m.mu.RLock()
	session = m.sessions[key]
	m.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("direct session for tab %d not found after attach", tabID)
	}
	return session, nil
}

// Detach detaches ONLY this manager's direct session for the given tab.
func (m *Manager) Detach(ctx context.Context, sessionID string, tabID int) error {
	key := SessionKey{SessionID: sessionID, TabID: tabID}
	m.mu.Lock()
	session, ok := m.sessions[key]
	if !ok || session == nil {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, key)
	delete(m.byCDPSessID, session.CDPSessionID)
	client := m.client
	m.mu.Unlock()

	m.cleanFileChoosersForTab(sessionID, tabID)

	if client != nil {
		detachCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, _ = client.Send(detachCtx, "Target.detachFromTarget", map[string]any{
			"sessionId": session.CDPSessionID,
		}, "")
	}
	return nil
}

// CleanSessionsForSession cleans up direct sessions for a given browser sessionID.
// If tabIDs is provided and non-empty, only cleans those tabs; otherwise cleans all tabs for that session.
func (m *Manager) CleanSessionsForSession(ctx context.Context, sessionID string, tabIDs []int) {
	m.mu.Lock()
	var toDetach []*DirectSession
	if len(tabIDs) > 0 {
		for _, tid := range tabIDs {
			k := SessionKey{SessionID: sessionID, TabID: tid}
			if s, ok := m.sessions[k]; ok {
				delete(m.sessions, k)
				delete(m.byCDPSessID, s.CDPSessionID)
				toDetach = append(toDetach, s)
			}
		}
	} else {
		for k, s := range m.sessions {
			if k.SessionID == sessionID {
				delete(m.sessions, k)
				delete(m.byCDPSessID, s.CDPSessionID)
				toDetach = append(toDetach, s)
			}
		}
	}
	client := m.client
	m.mu.Unlock()

	m.cleanFileChoosersForSession(sessionID, tabIDs)

	if client != nil && len(toDetach) > 0 {
		detachCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		for _, s := range toDetach {
			_, _ = client.Send(detachCtx, "Target.detachFromTarget", map[string]any{
				"sessionId": s.CDPSessionID,
			}, "")
		}
	}
}

// HasSession checks if a direct session exists for the given session and tab.
func (m *Manager) HasSession(sessionID string, tabID int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[SessionKey{SessionID: sessionID, TabID: tabID}]
	return ok
}

func parseEvaluateResponse(raw json.RawMessage) (any, error) {
	var evalResp struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &evalResp); err != nil {
		return nil, fmt.Errorf("unmarshal evaluate response: %w", err)
	}
	if evalResp.ExceptionDetails != nil {
		msg := evalResp.ExceptionDetails.Text
		if evalResp.ExceptionDetails.Exception != nil && evalResp.ExceptionDetails.Exception.Description != "" {
			msg = evalResp.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("JavaScript exception: %s", msg)
	}
	return evalResp.Result.Value, nil
}

// ReadClipboardText executes clipboard read on the direct CDP session.
func (m *Manager) ReadClipboardText(ctx context.Context, sessionID, turnID string, tabID int) (map[string]any, error) {
	res, err := m.ExecuteCdp(ctx, sessionID, turnID, tabID, "Runtime.evaluate", map[string]any{
		"expression":    "navigator.clipboard.readText()",
		"awaitPromise":  true,
		"returnByValue": true,
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	val, err := parseEvaluateResponse(res)
	if err != nil {
		return nil, err
	}
	text, _ := val.(string)
	return map[string]any{"text": text}, nil
}

// WriteClipboardText executes clipboard write on the direct CDP session.
func (m *Manager) WriteClipboardText(ctx context.Context, sessionID, turnID string, tabID int, text string) (map[string]any, error) {
	encodedText, _ := json.Marshal(text)
	expr := fmt.Sprintf("navigator.clipboard.writeText(%s)", string(encodedText))
	res, err := m.ExecuteCdp(ctx, sessionID, turnID, tabID, "Runtime.evaluate", map[string]any{
		"expression":   expr,
		"awaitPromise": true,
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if _, err := parseEvaluateResponse(res); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// ReadClipboard executes structured clipboard read on the direct CDP session.
func (m *Manager) ReadClipboard(ctx context.Context, sessionID, turnID string, tabID int) (map[string]any, error) {
	expr := `((async () => {
		const clipboardItems = await navigator.clipboard.read();
		return await Promise.all(clipboardItems.map(async (item) => {
			const entries = await Promise.all(item.types.map(async (type) => {
				const blob = await item.getType(type);
				if (type.startsWith("text/")) {
					return { mime_type: type, text: await blob.text() };
				}
				const base64 = await new Promise((resolve, reject) => {
					const reader = new FileReader();
					reader.onerror = () => reject(reader.error);
					reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
					reader.readAsDataURL(blob);
				});
				return { mime_type: type, base64 };
			}));
			return { entries, presentation_style: item.presentationStyle ?? "unspecified" };
		}));
	})())`
	res, err := m.ExecuteCdp(ctx, sessionID, turnID, tabID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"awaitPromise":  true,
		"returnByValue": true,
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	val, err := parseEvaluateResponse(res)
	if err != nil {
		return nil, err
	}
	items, _ := val.([]any)
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items}, nil
}

// WriteClipboard executes structured clipboard write on the direct CDP session.
func (m *Manager) WriteClipboard(ctx context.Context, sessionID, turnID string, tabID int, items any) (map[string]any, error) {
	encodedItems, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal clipboard items: %w", err)
	}
	expr := fmt.Sprintf(`((async (items) => {
		const clipboardItems = items.map((item) => {
			const entries = {};
			for (const entry of item.entries ?? []) {
				const mime = entry.mime_type;
				if (!mime) continue;
				if (typeof entry.text === "string") {
					entries[mime] = new Blob([entry.text], { type: mime });
				} else if (typeof entry.base64 === "string") {
					const bin = atob(entry.base64);
					const bytes = new Uint8Array(bin.length);
					for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
					entries[mime] = new Blob([bytes], { type: mime });
				}
			}
			return new ClipboardItem(entries);
		});
		await navigator.clipboard.write(clipboardItems);
	})(%s))`, string(encodedItems))

	res, err := m.ExecuteCdp(ctx, sessionID, turnID, tabID, "Runtime.evaluate", map[string]any{
		"expression":   expr,
		"awaitPromise": true,
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if _, err := parseEvaluateResponse(res); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// WaitForFileChooser sets up file chooser interception on the direct session and waits for dialog.
func (m *Manager) WaitForFileChooser(ctx context.Context, sessionID, turnID string, tabID int, timeout time.Duration) (map[string]any, error) {
	session, err := m.ensureSession(ctx, sessionID, turnID, tabID)
	if err != nil {
		return nil, err
	}

	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	waiterCh := make(chan fileChooserEvent, 1)
	m.fcMu.Lock()
	m.fcWaiters[tabID] = &fileChooserWaiter{
		tabID:   tabID,
		ch:      waiterCh,
		timeout: time.Now().Add(timeout),
	}
	m.fcMu.Unlock()

	defer func() {
		m.fcMu.Lock()
		delete(m.fcWaiters, tabID)
		m.fcMu.Unlock()
		// Best-effort disable file chooser interception on completion, timeout, or cancellation
		if m.client != nil {
			disableCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = m.client.Send(disableCtx, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": false}, session.CDPSessionID)
		}
	}()

	// Intercept file chooser
	_, err = m.client.Send(ctx, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}, session.CDPSessionID)
	if err != nil {
		return nil, fmt.Errorf("enable file chooser interception: %w", err)
	}

	select {
	case ev := <-waiterCh:
		chooserID := randomUUID()
		isMultiple := (ev.Mode == "selectMultiple")
		m.fcMu.Lock()
		m.activeChooser[chooserID] = &activeFileChooser{
			tabID:         tabID,
			sessionID:     sessionID,
			cdpSessionID:  session.CDPSessionID,
			backendNodeID: ev.BackendNodeID,
			mode:          ev.Mode,
			isMultiple:    isMultiple,
		}
		m.fcMu.Unlock()

		return map[string]any{
			"fileChooserId": chooserID,
			"mode":          ev.Mode,
		}, nil
	case <-time.After(timeout):
		return nil, errors.New("timeout waiting for file chooser dialog")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetFileChooserFiles writes file paths into the intercepted file chooser.
func (m *Manager) SetFileChooserFiles(ctx context.Context, sessionID, fileChooserID string, files []string) (map[string]any, error) {
	m.fcMu.Lock()
	chooser, ok := m.activeChooser[fileChooserID]
	if !ok || chooser == nil {
		m.fcMu.Unlock()
		return nil, fmt.Errorf("unknown file chooser id %q", fileChooserID)
	}
	if chooser.sessionID != sessionID {
		m.fcMu.Unlock()
		return nil, fmt.Errorf("file chooser %q belongs to session %s, not %s", fileChooserID, chooser.sessionID, sessionID)
	}
	m.fcMu.Unlock()

	if len(files) == 0 {
		return nil, errors.New("setFileChooserFiles requires at least one file")
	}
	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			return nil, errors.New("setFileChooserFiles files must be non-empty strings")
		}
	}
	if !chooser.isMultiple && len(files) > 1 {
		return nil, errors.New("file chooser does not accept multiple files")
	}

	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil, errors.New("direct CDP client is not connected")
	}

	params := map[string]any{
		"files": files,
	}
	if chooser.backendNodeID > 0 {
		params["backendNodeId"] = chooser.backendNodeID
	}

	_, err := client.Send(ctx, "DOM.setFileInputFiles", params, chooser.cdpSessionID)
	if err != nil {
		return nil, fmt.Errorf("set file input files: %w", err)
	}

	m.fcMu.Lock()
	delete(m.activeChooser, fileChooserID)
	m.fcMu.Unlock()
	return map[string]any{"ok": true}, nil
}

// HasActiveFileChooser checks if a file chooser ID is currently registered in this manager.
func (m *Manager) HasActiveFileChooser(fileChooserID string) bool {
	m.fcMu.Lock()
	defer m.fcMu.Unlock()
	_, ok := m.activeChooser[fileChooserID]
	return ok
}

func (m *Manager) cleanFileChoosersForTab(sessionID string, tabID int) {
	m.fcMu.Lock()
	delete(m.fcWaiters, tabID)
	for id, chooser := range m.activeChooser {
		if chooser.sessionID == sessionID && chooser.tabID == tabID {
			delete(m.activeChooser, id)
		}
	}
	m.fcMu.Unlock()
}

func (m *Manager) cleanFileChoosersForSession(sessionID string, tabIDs []int) {
	m.fcMu.Lock()
	if len(tabIDs) > 0 {
		for _, tid := range tabIDs {
			delete(m.fcWaiters, tid)
			for id, chooser := range m.activeChooser {
				if chooser.sessionID == sessionID && chooser.tabID == tid {
					delete(m.activeChooser, id)
				}
			}
		}
	} else {
		for tid := range m.fcWaiters {
			delete(m.fcWaiters, tid)
		}
		for id, chooser := range m.activeChooser {
			if chooser.sessionID == sessionID {
				delete(m.activeChooser, id)
			}
		}
	}
	m.fcMu.Unlock()
}

func (m *Manager) cleanAllFileChoosers() {
	m.fcMu.Lock()
	m.fcWaiters = make(map[int]*fileChooserWaiter)
	m.activeChooser = make(map[string]*activeFileChooser)
	m.fcMu.Unlock()
}

// Close closes the direct manager and detaches all active direct sessions.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	sessions := make([]*DirectSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[SessionKey]*DirectSession)
	m.byCDPSessID = make(map[string]*DirectSession)
	client := m.client
	m.mu.Unlock()

	m.cleanAllFileChoosers()
	m.eventWaitMu.Lock()
	waiters := m.eventWaiters
	m.eventWaiters = make(map[uint64]*eventWaiter)
	m.eventWaitMu.Unlock()
	m.tabTargetsMu.Lock()
	m.tabTargets = make(map[string]struct{})
	m.tabSessions = make(map[string]string)
	m.pageToTab = make(map[string]string)
	m.tabTargetsMu.Unlock()
	for _, waiter := range waiters {
		waiter.once.Do(func() { close(waiter.done) })
	}
	m.reportConnectionState(false)

	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for _, s := range sessions {
			_, _ = client.Send(ctx, "Target.detachFromTarget", map[string]any{
				"sessionId": s.CDPSessionID,
			}, "")
		}
		return client.Close()
	}
	return nil
}

func randomUUID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		bytes[0:4],
		bytes[4:6],
		bytes[6:8],
		bytes[8:10],
		bytes[10:16],
	)
}
