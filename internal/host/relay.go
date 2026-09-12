package host

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ifuryst/open-browser-use/internal/cdp"
	"github.com/ifuryst/open-browser-use/internal/wire"
)

const NativeHostName = "com.ifuryst.open_browser_use.extension"

var DefaultSocketDir = defaultSocketDir()

type Config struct {
	SocketDir  string
	SocketPath string
	CDPMode    string // "auto", "direct", "extension"
	Logger     *slog.Logger
}

type Relay struct {
	config Config
	logger *slog.Logger

	stdin  io.Reader
	stdout io.Writer

	listener net.Listener

	mu        sync.Mutex
	clients   map[int64]*clientConn
	pending   map[string]pendingRequest
	nextID    atomic.Int64
	nextReqID atomic.Int64
	writeMu   sync.Mutex

	// Direct CDP integration
	cdpManager   *cdp.Manager
	cdpMode      string
	cdpInitMu    sync.Mutex
	cdpInitDone  chan struct{}
	cdpInitErr   error
	tabRoutesMu  sync.RWMutex
	tabRoutes    map[cdp.SessionKey]string // "direct" or "extension"
	extPendingMu sync.Mutex
	extPending   map[string]chan map[string]any
}

type clientConn struct {
	id   int64
	conn net.Conn
	mu   sync.Mutex
}

type pendingRequest struct {
	client     *clientConn
	originalID any
}

type ActiveSocketRecord struct {
	SocketPath string    `json:"socketPath"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
}

func NewRelay(config Config, stdin io.Reader, stdout io.Writer) *Relay {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	mode := strings.TrimSpace(strings.ToLower(config.CDPMode))
	if mode == "" {
		mode = strings.TrimSpace(strings.ToLower(os.Getenv("OBU_CDP_MODE")))
	}
	if mode == "" {
		mode = "auto"
	}
	return &Relay{
		config:     config,
		logger:     logger,
		stdin:      stdin,
		stdout:     stdout,
		clients:    make(map[int64]*clientConn),
		pending:    make(map[string]pendingRequest),
		cdpMode:    mode,
		tabRoutes:  make(map[cdp.SessionKey]string),
		extPending: make(map[string]chan map[string]any),
	}
}

// SetCDPManager allows injecting a custom or mock CDP Manager.
func (r *Relay) SetCDPManager(mgr *cdp.Manager) {
	r.cdpInitMu.Lock()
	r.mu.Lock()
	r.cdpManager = mgr
	r.mu.Unlock()
	r.cdpInitErr = nil
	r.cdpInitMu.Unlock()
}

func (r *Relay) getExistingCDPManager() *cdp.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cdpManager
}

func (r *Relay) SocketPath() string {
	if r.config.SocketPath != "" {
		return r.config.SocketPath
	}
	return filepath.Join(socketDir(r.config.SocketDir), fmt.Sprintf("%s.sock", randomID()))
}

func (r *Relay) Serve(ctx context.Context) error {
	socketPath := r.SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen unix socket %q (path length %d): %w", socketPath, len(socketPath), err)
	}
	r.listener = listener
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return err
	}
	if err := WriteActiveSocketRecord(r.config.SocketDir, socketPath); err != nil {
		_ = listener.Close()
		return err
	}
	r.logger.Info("open-browser-use native host listening", "socket", socketPath, "cdpMode", r.cdpMode)

	errCh := make(chan error, 2)
	go func() { errCh <- r.acceptLoop(ctx) }()
	go func() { errCh <- r.readExtensionLoop() }()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
		r.closeClients()
		if r.cdpManager != nil {
			_ = r.cdpManager.Close()
		}
	}()

	err = <-errCh
	_ = listener.Close()
	_ = os.Remove(socketPath)
	_ = RemoveActiveSocketRecord(r.config.SocketDir, socketPath)
	r.closeClients()
	if r.cdpManager != nil {
		_ = r.cdpManager.Close()
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (r *Relay) acceptLoop(ctx context.Context) error {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		id := r.nextID.Add(1)
		client := &clientConn{id: id, conn: conn}
		r.mu.Lock()
		r.clients[id] = client
		r.mu.Unlock()
		go r.readClientLoop(client)
	}
}

func (r *Relay) readClientLoop(client *clientConn) {
	defer func() {
		_ = client.conn.Close()
		r.mu.Lock()
		delete(r.clients, client.id)
		for requestID, pending := range r.pending {
			if pending.client == client {
				delete(r.pending, requestID)
			}
		}
		r.mu.Unlock()
	}()

	for {
		payload, err := wire.ReadFrame(client.conn, wire.DefaultMaxFrame)
		if err != nil {
			return
		}

		// Check if request should be intercepted for direct CDP
		handled, err := r.maybeHandleDirectCDP(client, payload)
		if handled {
			if err != nil {
				// Handled and produced an error (e.g. invalid json)
				r.writeClientError(client, nil, err)
			}
			continue
		}

		forwarded, hostID, originalID, err := r.rewriteClientRequest(payload)
		if err != nil {
			r.writeClientError(client, nil, err)
			continue
		}
		if hostID != "" {
			r.mu.Lock()
			r.pending[hostID] = pendingRequest{client: client, originalID: originalID}
			r.mu.Unlock()
		}
		if err := r.writeExtension(forwarded); err != nil {
			if hostID != "" {
				r.mu.Lock()
				delete(r.pending, hostID)
				r.mu.Unlock()
			}
			r.writeClientError(client, originalID, err)
		}
	}
}

func (r *Relay) readExtensionLoop() error {
	for {
		payload, err := wire.ReadFrame(r.stdin, wire.DefaultMaxFrame)
		if err != nil {
			return err
		}
		if err := r.dispatchExtensionPayload(payload); err != nil {
			r.logger.Warn("failed to dispatch extension payload", "error", err)
		}
	}
}

func (r *Relay) rewriteClientRequest(payload []byte) ([]byte, string, any, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, "", nil, err
	}
	id, hasID := message["id"]
	if !hasID {
		return payload, "", nil, nil
	}
	hostID := "obu:" + strconv.FormatInt(r.nextReqID.Add(1), 10)
	message["id"] = hostID
	forwarded, err := json.Marshal(message)
	return forwarded, hostID, id, err
}

func (r *Relay) dispatchExtensionPayload(payload []byte) error {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return err
	}
	id, hasID := message["id"].(string)
	if hasID {
		// Internal host-to-extension request
		if strings.HasPrefix(id, "obu:internal:") {
			r.extPendingMu.Lock()
			ch, ok := r.extPending[id]
			if ok {
				delete(r.extPending, id)
			}
			r.extPendingMu.Unlock()
			if ok && ch != nil {
				ch <- message
			}
			return nil
		}

		r.mu.Lock()
		pending, ok := r.pending[id]
		if ok {
			delete(r.pending, id)
		}
		r.mu.Unlock()
		if ok {
			message["id"] = pending.originalID
			rewritten, err := json.Marshal(message)
			if err != nil {
				return err
			}
			return pending.client.write(rewritten)
		}
	}
	r.broadcast(payload)
	return nil
}

func (r *Relay) writeExtension(payload []byte) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return wire.WriteFrame(r.stdout, payload)
}

func (r *Relay) writeClientError(client *clientConn, id any, err error) {
	if id == nil {
		id = nil
	}
	_ = client.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32000,
			"message": err.Error(),
		},
	})
}

func (r *Relay) broadcast(payload []byte) {
	r.mu.Lock()
	clients := make([]*clientConn, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	r.mu.Unlock()
	for _, client := range clients {
		_ = client.write(payload)
	}
}

// BroadcastNotification implements cdp.EventBroadcaster.
func (r *Relay) BroadcastNotification(method string, params any) {
	notification := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	payload, err := json.Marshal(notification)
	if err == nil {
		r.broadcast(payload)
	}
}

func (r *Relay) notifyExtensionDirectCDPState(connected bool) {
	notification := map[string]any{
		"jsonrpc": "2.0",
		"method":  "setDirectCdpConnectionState",
		"params": map[string]any{
			"connected": connected,
			"transport": "direct",
		},
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		return
	}
	if err := r.writeExtension(payload); err != nil {
		r.logger.Debug("failed to publish direct CDP state to extension", "connected", connected, "error", err)
	}
}

// GetTargetMapping implements cdp.TargetMapper to request exact tabId->targetId from extension.
func (r *Relay) GetTargetMapping(ctx context.Context, sessionID, turnID string, tabID int) (string, error) {
	reqID := "obu:internal:map:" + strconv.FormatInt(r.nextReqID.Add(1), 10)
	ch := make(chan map[string]any, 1)

	r.extPendingMu.Lock()
	r.extPending[reqID] = ch
	r.extPendingMu.Unlock()

	defer func() {
		r.extPendingMu.Lock()
		delete(r.extPending, reqID)
		r.extPendingMu.Unlock()
	}()

	mappingParams := map[string]any{
		"session_id": sessionID,
		"turn_id":    turnID,
		"tabId":      tabID,
	}
	if dl, ok := ctx.Deadline(); ok {
		mappingParams["_deadlineUnixMs"] = dl.UnixMilli()
	}

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "getTargetMapping",
		"params":  mappingParams,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	if err := r.writeExtension(payload); err != nil {
		return "", fmt.Errorf("send getTargetMapping to extension: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return "", errors.New("extension disconnected while awaiting target mapping")
		}
		if errObj, ok := resp["error"].(map[string]any); ok {
			msg, _ := errObj["message"].(string)
			return "", fmt.Errorf("extension getTargetMapping error: %s", msg)
		}
		result, ok := resp["result"].(map[string]any)
		if !ok {
			return "", errors.New("invalid getTargetMapping response from extension")
		}
		targetID, ok := result["targetId"].(string)
		if !ok || targetID == "" {
			return "", errors.New("getTargetMapping returned empty targetId")
		}
		return targetID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (r *Relay) getExtensionInfo(ctx context.Context) (extensionID, instanceID string, err error) {
	reqID := "obu:internal:info:" + strconv.FormatInt(r.nextReqID.Add(1), 10)
	ch := make(chan map[string]any, 1)

	r.extPendingMu.Lock()
	r.extPending[reqID] = ch
	r.extPendingMu.Unlock()

	defer func() {
		r.extPendingMu.Lock()
		delete(r.extPending, reqID)
		r.extPendingMu.Unlock()
	}()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "getInfo",
		"params":  map[string]any{},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}
	if err := r.writeExtension(payload); err != nil {
		return "", "", fmt.Errorf("send getInfo to extension: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return "", "", errors.New("extension disconnected while awaiting getInfo")
		}
		result, _ := resp["result"].(map[string]any)
		meta, _ := result["metadata"].(map[string]any)
		extID, _ := meta["extensionId"].(string)
		instID, _ := meta["extensionInstanceId"].(string)
		if extID == "" || instID == "" {
			return "", "", errors.New("extension getInfo returned incomplete metadata")
		}
		return extID, instID, nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

func (r *Relay) ensureCDPManager(ctx context.Context) (*cdp.Manager, error) {
	if r.cdpMode == "extension" {
		return nil, errors.New("CDP mode is explicit extension")
	}

	r.cdpInitMu.Lock()
	r.mu.Lock()
	if r.cdpManager != nil {
		mgr := r.cdpManager
		if mgr.IsConnected() {
			r.mu.Unlock()
			r.cdpInitMu.Unlock()
			return mgr, nil
		}
		// Chrome/browser-root WebSocket died (for example after a browser
		// restart). Drop only the transport manager; per-tab route choice stays
		// sticky and will lazily attach to the replacement manager.
		r.cdpManager = nil
		r.mu.Unlock()
		r.cdpInitDone = nil
		r.cdpInitErr = nil
		_ = mgr.Close()
	} else {
		r.mu.Unlock()
	}

	// A failed completed attempt is retryable on the next request. A still-
	// pending attempt is deliberately shared: personal Chrome may keep one
	// WebSocket Upgrade parked on its approval sheet for an arbitrary amount
	// of time, and creating a second connection would create a second prompt.
	if r.cdpInitDone != nil {
		select {
		case <-r.cdpInitDone:
			if r.cdpInitErr != nil {
				r.cdpInitDone = nil
				r.cdpInitErr = nil
			}
		default:
		}
	}
	if r.cdpInitDone == nil {
		done := make(chan struct{})
		r.cdpInitDone = done
		go r.initCDPManager(done)
	}
	done := r.cdpInitDone
	r.cdpInitMu.Unlock()

	select {
	case <-done:
		r.cdpInitMu.Lock()
		err := r.cdpInitErr
		r.mu.Lock()
		mgr := r.cdpManager
		r.mu.Unlock()
		r.cdpInitMu.Unlock()
		if mgr != nil {
			return mgr, nil
		}
		if err == nil {
			err = errors.New("direct CDP initialization completed without a manager")
		}
		return nil, err
	case <-ctx.Done():
		// Do not cancel the shared browser handshake. A later approval should
		// still complete the one daemon-wide connection for subsequent calls.
		return nil, ctx.Err()
	}
}

func (r *Relay) initCDPManager(done chan struct{}) {
	mgr, err := r.createCDPManager()

	r.cdpInitMu.Lock()
	defer r.cdpInitMu.Unlock()
	if err == nil && mgr != nil {
		r.mu.Lock()
		r.cdpManager = mgr
		r.mu.Unlock()
	}
	r.cdpInitErr = err
	close(done)
}

func (r *Relay) createCDPManager() (*cdp.Manager, error) {
	var userDataDir string
	if envDir := strings.TrimSpace(os.Getenv("OBU_USER_DATA_DIR")); envDir != "" {
		userDataDir = envDir
	} else {
		infoCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		extID, instID, err := r.getExtensionInfo(infoCtx)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("query extension info for exact profile resolution: %w", err)
		}

		_, _, _, _, _, root, ok := cdp.ResolveProfileForInstanceID(extID, instID)
		if !ok || root == "" {
			return nil, fmt.Errorf("could not resolve exact profile root for extension %s (instance %s); no default guessing allowed", extID, instID)
		}
		userDataDir = root
	}

	portInfo, err := cdp.ReadDevToolsActivePort(userDataDir)
	if err != nil {
		return nil, fmt.Errorf("read DevToolsActivePort in %s: %w", userDataDir, err)
	}

	cfg := cdp.ManagerConfig{
		WSURL:              portInfo.WebSocketURL(),
		Mapper:             r,
		Broadcaster:        r,
		ConnectionObserver: r.notifyExtensionDirectCDPState,
	}
	mgr := cdp.NewManager(cfg)
	// Local personal Chrome can park this Upgrade indefinitely while waiting
	// for its per-session approval. The relay owns one patient pending
	// connection; individual caller deadlines only bound how long callers wait.
	if err := mgr.Connect(context.Background()); err != nil {
		return nil, fmt.Errorf("connect direct CDP at %s: %w", portInfo.WebSocketURL(), err)
	}
	r.logger.Info("direct CDP connected for profile", "dir", userDataDir, "ws", portInfo.WebSocketURL())
	return mgr, nil
}

func (r *Relay) resolveTabRoute(ctx context.Context, sessionID, turnID string, tabID int, mode string) (string, *cdp.Manager, error) {
	key := cdp.SessionKey{SessionID: sessionID, TabID: tabID}
	r.tabRoutesMu.RLock()
	route := r.tabRoutes[key]
	r.tabRoutesMu.RUnlock()

	if route == "extension" {
		return "extension", nil, nil
	}
	if route == "direct" {
		mgr, err := r.ensureCDPManager(ctx)
		return "direct", mgr, err
	}

	// Unrouted tab: decide and record sticky route
	if mode == "extension" {
		r.tabRoutesMu.Lock()
		r.tabRoutes[key] = "extension"
		r.tabRoutesMu.Unlock()
		return "extension", nil, nil
	}

	if mode == "direct" {
		mgr, err := r.ensureCDPManager(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("direct CDP mode requested but manager is unavailable: %w", err)
		}
		_, err = mgr.Attach(ctx, sessionID, turnID, tabID)
		if err != nil {
			return "", nil, fmt.Errorf("direct attach failed: %w", err)
		}
		r.tabRoutesMu.Lock()
		r.tabRoutes[key] = "direct"
		r.tabRoutesMu.Unlock()
		return "direct", mgr, nil
	}

	// Auto mode
	mgr, err := r.ensureCDPManager(ctx)
	if err == nil && mgr != nil {
		_, err = mgr.Attach(ctx, sessionID, turnID, tabID)
		if err == nil {
			r.tabRoutesMu.Lock()
			r.tabRoutes[key] = "direct"
			r.tabRoutesMu.Unlock()
			return "direct", mgr, nil
		}
		r.logger.Debug("auto mode direct attach failed, choosing extension route", "tabId", tabID, "error", err)
	}

	r.tabRoutesMu.Lock()
	r.tabRoutes[key] = "extension"
	r.tabRoutesMu.Unlock()
	return "extension", nil, nil
}

// maybeHandleDirectCDP intercepts CDP-related requests and routes them between direct CDP and extension.
func (r *Relay) maybeHandleDirectCDP(client *clientConn, payload []byte) (bool, error) {
	var msg map[string]any
	if err := json.Unmarshal(payload, &msg); err != nil {
		return false, err
	}

	method, _ := msg["method"].(string)
	params, _ := msg["params"].(map[string]any)
	if params == nil {
		params = make(map[string]any)
	}
	id := msg["id"]

	// Resolve caller-specified mode or host mode
	mode := r.cdpMode
	if m, ok := params["_cdpMode"].(string); ok {
		m = strings.TrimSpace(strings.ToLower(m))
		if m == "auto" || m == "direct" || m == "extension" {
			mode = m
		}
	}

	sessionID, _ := params["session_id"].(string)
	if sessionID == "" {
		sessionID = "obu-cli"
	}
	turnID, _ := params["turn_id"].(string)
	if turnID == "" {
		turnID = "direct-cdp"
	}

	var reqCtx context.Context
	var cancel context.CancelFunc
	if deadlineMs, err := toInt64(params["_deadlineUnixMs"]); err == nil && deadlineMs > 0 {
		deadlineTime := time.UnixMilli(deadlineMs)
		reqCtx, cancel = context.WithDeadline(context.Background(), deadlineTime)
	} else {
		reqCtx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	}
	defer cancel()

	// Intercept session cleanup calls to clean up direct sessions before/with forwarding
	switch method {
	case "releaseTabs":
		var tabIDs []int
		if rawIDs, ok := params["tabIds"].([]any); ok && len(rawIDs) > 0 {
			for _, item := range rawIDs {
				if n, err := toInt(item); err == nil && n > 0 {
					tabIDs = append(tabIDs, n)
				}
			}
		}
		r.tabRoutesMu.Lock()
		if len(tabIDs) > 0 {
			for _, tid := range tabIDs {
				delete(r.tabRoutes, cdp.SessionKey{SessionID: sessionID, TabID: tid})
			}
		} else {
			for k := range r.tabRoutes {
				if k.SessionID == sessionID {
					delete(r.tabRoutes, k)
				}
			}
		}
		r.tabRoutesMu.Unlock()

		if mgr := r.getExistingCDPManager(); mgr != nil {
			mgr.CleanSessionsForSession(reqCtx, sessionID, tabIDs)
		}
		// Always forward releaseTabs to extension so tab origins/groups are updated
		return false, nil

	case "closeUserTab":
		tabID, _ := toInt(params["tabId"])
		r.tabRoutesMu.Lock()
		delete(r.tabRoutes, cdp.SessionKey{SessionID: sessionID, TabID: tabID})
		r.tabRoutesMu.Unlock()

		if mgr := r.getExistingCDPManager(); mgr != nil && tabID > 0 {
			mgr.CleanSessionsForSession(reqCtx, sessionID, []int{tabID})
		}
		// Forward to extension to close the Chrome tab
		return false, nil

	case "finalizeTabs", "turnEnded":
		r.tabRoutesMu.Lock()
		for k := range r.tabRoutes {
			if k.SessionID == sessionID {
				delete(r.tabRoutes, k)
			}
		}
		r.tabRoutesMu.Unlock()

		if mgr := r.getExistingCDPManager(); mgr != nil {
			mgr.CleanSessionsForSession(reqCtx, sessionID, nil)
		}
		return false, nil
	}

	// Direct-route interception for attach, executeCdp, detach, clipboard, file chooser
	switch method {
	case "attach":
		tabID, err := toInt(params["tabId"])
		if err != nil || tabID <= 0 {
			r.writeClientError(client, id, errors.New("attach requires positive integer tabId"))
			return true, nil
		}
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			wakeRes, err := mgr.Attach(reqCtx, sessionID, turnID, tabID)
			if err != nil {
				r.writeClientError(client, id, fmt.Errorf("direct attach failed: %w", err))
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": wakeRes})
			return true, nil
		}
		return false, nil

	case "executeCdp":
		tabID := extractTabIDFromExecuteCdp(params)
		timeout := 10 * time.Second
		if t, ok := params["timeoutMs"].(float64); ok && t > 0 {
			timeout = time.Duration(t) * time.Millisecond
		}

		// Browser-wide commands (tabID <= 0)
		if tabID <= 0 {
			if mode == "extension" {
				return false, nil
			}
			cdpMethod, _ := params["method"].(string)
			cmdParams := params["commandParams"]

			if mode == "direct" {
				mgr, err := r.ensureCDPManager(reqCtx)
				if err != nil {
					r.writeClientError(client, id, fmt.Errorf("direct CDP mode requested but manager is unavailable: %w", err))
					return true, nil
				}
				res, err := mgr.ExecuteCdp(reqCtx, sessionID, turnID, 0, cdpMethod, cmdParams, timeout)
				if err != nil {
					r.writeClientError(client, id, err)
					return true, nil
				}
				var parsedRes any
				_ = json.Unmarshal(res, &parsedRes)
				_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": parsedRes})
				return true, nil
			}
			// Auto: try direct, fallback to extension
			mgr, err := r.ensureCDPManager(reqCtx)
			if err == nil && mgr != nil {
				res, err := mgr.ExecuteCdp(reqCtx, sessionID, turnID, 0, cdpMethod, cmdParams, timeout)
				if err == nil {
					var parsedRes any
					_ = json.Unmarshal(res, &parsedRes)
					_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": parsedRes})
					return true, nil
				}
			}
			return false, nil
		}

		// Tab-scoped commands (tabID > 0)
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			cdpMethod, _ := params["method"].(string)
			cmdParams := params["commandParams"]
			res, err := mgr.ExecuteCdp(reqCtx, sessionID, turnID, tabID, cdpMethod, cmdParams, timeout)
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			var parsedRes any
			_ = json.Unmarshal(res, &parsedRes)
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": parsedRes})
			return true, nil
		}
		return false, nil

	case "detach":
		tabID, err := toInt(params["tabId"])
		if err != nil || tabID <= 0 {
			return false, nil
		}
		key := cdp.SessionKey{SessionID: sessionID, TabID: tabID}
		r.tabRoutesMu.RLock()
		route := r.tabRoutes[key]
		r.tabRoutesMu.RUnlock()
		// Route remains sticky for (sessionID, tabID) until releaseTabs/closeUserTab/finalizeTabs/turnEnded

		if route == "direct" {
			if mgr := r.getExistingCDPManager(); mgr != nil {
				_ = mgr.Detach(reqCtx, sessionID, tabID)
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"ok": true}})
			return true, nil
		}
		return false, nil

	case "readClipboardText":
		tabID, _ := toInt(params["tabId"])
		if tabID <= 0 {
			return false, nil
		}
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			res, err := mgr.ReadClipboardText(reqCtx, sessionID, turnID, tabID)
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			return true, nil
		}
		return false, nil

	case "writeClipboardText":
		tabID, _ := toInt(params["tabId"])
		if tabID <= 0 {
			return false, nil
		}
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			text, _ := params["text"].(string)
			res, err := mgr.WriteClipboardText(reqCtx, sessionID, turnID, tabID, text)
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			return true, nil
		}
		return false, nil

	case "readClipboard":
		tabID, _ := toInt(params["tabId"])
		if tabID <= 0 {
			return false, nil
		}
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			res, err := mgr.ReadClipboard(reqCtx, sessionID, turnID, tabID)
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			return true, nil
		}
		return false, nil

	case "writeClipboard":
		tabID, _ := toInt(params["tabId"])
		if tabID <= 0 {
			return false, nil
		}
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			res, err := mgr.WriteClipboard(reqCtx, sessionID, turnID, tabID, params["items"])
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			return true, nil
		}
		return false, nil

	case "waitForFileChooser":
		tabID, _ := toInt(params["tabId"])
		if tabID <= 0 {
			return false, nil
		}
		route, mgr, err := r.resolveTabRoute(reqCtx, sessionID, turnID, tabID, mode)
		if err != nil {
			r.writeClientError(client, id, err)
			return true, nil
		}
		if route == "direct" {
			timeout := 30 * time.Second
			if t, ok := params["timeoutMs"].(float64); ok && t > 0 {
				timeout = time.Duration(t) * time.Millisecond
			}
			res, err := mgr.WaitForFileChooser(reqCtx, sessionID, turnID, tabID, timeout)
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			return true, nil
		}
		return false, nil

	case "setFileChooserFiles":
		chooserID, _ := params["fileChooserId"].(string)
		mgr := r.getExistingCDPManager()
		if mgr != nil && mgr.HasActiveFileChooser(chooserID) {
			var files []string
			if rawFiles, ok := params["files"].([]any); ok {
				for _, f := range rawFiles {
					if s, ok := f.(string); ok {
						files = append(files, s)
					}
				}
			}
			res, err := mgr.SetFileChooserFiles(reqCtx, sessionID, chooserID, files)
			if err != nil {
				r.writeClientError(client, id, err)
				return true, nil
			}
			_ = client.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": res})
			return true, nil
		}
		if mode == "direct" {
			r.writeClientError(client, id, fmt.Errorf("direct mode: file chooser %q not found or direct manager unavailable", chooserID))
			return true, nil
		}
		return false, nil
	}

	return false, nil
}

func extractTabIDFromExecuteCdp(params map[string]any) int {
	if target, ok := params["target"].(map[string]any); ok {
		if tid, err := toInt(target["tabId"]); err == nil && tid > 0 {
			return tid
		}
	}
	if tid, err := toInt(params["tabId"]); err == nil && tid > 0 {
		return tid
	}
	return 0
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(n))
	default:
		return 0, errors.New("not an integer")
	}
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(n), 10, 64)
	default:
		return 0, errors.New("not an integer")
	}
}

func (r *Relay) closeClients() {
	r.mu.Lock()
	clients := make([]*clientConn, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	r.clients = make(map[int64]*clientConn)
	r.pending = make(map[string]pendingRequest)
	r.mu.Unlock()
	for _, client := range clients {
		_ = client.conn.Close()
	}
}

func (c *clientConn) writeJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.write(payload)
}

func (c *clientConn) write(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return wire.WriteFrame(c.conn, payload)
}

func socketDir(configured string) string {
	if configured != "" {
		return configured
	}
	return DefaultSocketDir
}

func defaultSocketDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "open-browser-use")
	}
	return "/tmp/open-browser-use"
}

func ActiveSocketRecordPath(configuredDir string) string {
	return filepath.Join(socketDir(configuredDir), "active.json")
}

func WriteActiveSocketRecord(configuredDir string, socketPath string) error {
	dir := socketDir(configuredDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	record := ActiveSocketRecord{
		SocketPath: socketPath,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := ActiveSocketRecordPath(configuredDir)
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func ReadActiveSocketRecord(configuredDir string) (ActiveSocketRecord, error) {
	payload, err := os.ReadFile(ActiveSocketRecordPath(configuredDir))
	if err != nil {
		return ActiveSocketRecord{}, err
	}
	var record ActiveSocketRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return ActiveSocketRecord{}, err
	}
	if record.SocketPath == "" {
		return ActiveSocketRecord{}, errors.New("active socket record has empty socketPath")
	}
	return record, nil
}

func RemoveActiveSocketRecord(configuredDir string, socketPath string) error {
	record, err := ReadActiveSocketRecord(configuredDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if record.SocketPath != socketPath {
		return nil
	}
	return os.Remove(ActiveSocketRecordPath(configuredDir))
}

func randomID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	}
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
