// Command CPA-Auto-Proxy-Test is a CLIProxyAPI plugin that automatically routes
// credentials of the configured auth type to the best available proxy by
// observing request failures through the usage hook and rewriting the
// proxy_url member of the credential files.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unsafe"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/config"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/plugin"
)

const pluginIdentifier = "CPA-Auto-Proxy-Test"

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	UsagePlugin   bool `json:"usage_plugin"`
	ManagementAPI bool `json:"management_api"`
}

// managementRegistrationResponse mirrors the host's
// rpcManagementRegistrationResponse schema: the RPC transport fills the
// route handlers host-side, so only method/path/menu/description travel over
// the wire.
type managementRegistrationResponse struct {
	Routes    []pluginapi.ManagementRoute `json:"routes,omitempty"`
	Resources []pluginapi.ResourceRoute   `json:"resources,omitempty"`
}

// managementResponse mirrors pluginapi.ManagementResponse for the RPC
// envelope; the plugin package owns the type so both sides share it.
type managementResponse = plugin.ManagementResponse

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, pluginApi *C.cliproxy_plugin_api) C.int {
	if pluginApi == nil {
		return 1
	}
	C.store_host_api(host)
	pluginApi.abi_version = C.uint32_t(pluginabi.ABIVersion)
	pluginApi.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	pluginApi.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	pluginApi.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

var (
	engineMu     sync.Mutex
	activeEngine *plugin.Engine
	activeHost   plugin.HostAPI = hostCallbacks{}
)

func currentEngine() *plugin.Engine {
	engineMu.Lock()
	defer engineMu.Unlock()
	return activeEngine
}

func replaceEngine(cfg config.Config) error {
	engineMu.Lock()
	engine := activeEngine
	engineMu.Unlock()
	if engine == nil {
		created, errNew := plugin.New(cfg, activeHost)
		if errNew != nil {
			return errNew
		}
		engineMu.Lock()
		activeEngine = created
		engineMu.Unlock()
		return nil
	}
	return engine.Reconfigure(cfg)
}

func shutdownEngine() {
	engineMu.Lock()
	engine := activeEngine
	activeEngine = nil
	engineMu.Unlock()
	if engine == nil {
		return
	}
	_ = engine.Close()
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	shutdownEngine()
}
func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if errConfigure := configure(request); errConfigure != nil {
			return nil, errConfigure
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodUsageHandle:
		engine := currentEngine()
		if engine == nil {
			return okEnvelope(map[string]any{})
		}
		var record pluginapi.UsageRecord
		if len(request) > 0 {
			if errUnmarshal := json.Unmarshal(request, &record); errUnmarshal != nil {
				return nil, fmt.Errorf("decode usage record: %w", errUnmarshal)
			}
		}
		engine.HandleUsage(record)
		return okEnvelope(map[string]any{})
	case pluginabi.MethodPluginQuiesce:
		shutdownEngine()
		return okEnvelope(map[string]any{})
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegister())
	case pluginabi.MethodManagementHandle:
		return managementHandle(request), nil
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return errUnmarshal
		}
	}
	cfg, errParse := config.Parse(req.ConfigYAML)
	if errParse != nil {
		return errParse
	}
	return replaceEngine(cfg)
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginIdentifier,
			Version:          "0.1.0",
			Author:           "ApliNi",
			GitHubRepository: "https://github.com/ApliNi/CPA-Auto-Proxy-Test",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "auth-type", Type: pluginapi.ConfigFieldTypeString, Description: "Credential type managed by this plugin (default Antigravity)."},
				{Name: "proxies", Type: pluginapi.ConfigFieldTypeArray, Description: "Candidate proxy URLs, one full URL per entry."},
				{Name: "failure-patterns", Type: pluginapi.ConfigFieldTypeArray, Description: "Failure matchers: regular expressions for the response body or plain numeric status codes (empty matches all failures)."},
				{Name: "window-hours", Type: pluginapi.ConfigFieldTypeInteger, Description: "Length of the recent observation window in hours (default 24)."},
			},
		},
		Capabilities: registrationCapability{
			UsagePlugin:   true,
			ManagementAPI: true,
		},
	}
}

// managementRegister declares the plugin's Management API and resource
// routes. The host normalizes ManagementRoute paths under /v0/management and
// resource paths under /v0/resource/plugins/<id> without adding a plugin-ID
// namespace to relative paths, so every path carries the plugin identifier to
// stay plugin-scoped and to avoid colliding with other plugins. Resource
// paths must not be "/" (the host trims trailing slashes and rejects the
// empty remainder), hence the named /page route for the status HTML.
func managementRegister() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: "GET", Path: "/" + pluginIdentifier + "/status"},
			{Method: "GET", Path: "/" + pluginIdentifier + "/export"},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/" + pluginIdentifier + "/page", Menu: "CPA-Auto-Proxy-Test", Description: "代理路由状态"},
			{Path: "/" + pluginIdentifier + "/data", Menu: "", Description: "状态 JSON 快照"},
			// No Menu label: reachable as a plain resource (the page's export
			// link and direct URL) but hidden from the panel's plugin menu.
			{Path: "/" + pluginIdentifier + "/export", Menu: "", Description: "完整观测与事件导出 JSON"},
		},
	}
}

// managementHandleRequest mirrors the host's rpcManagementRequest payload.
// The host serializes pluginapi.ManagementRequest with encoding/json default
// field names (Method/Path/Headers/Query/Body); a snake_case fallback keeps
// the parser tolerant to schema variations.
type managementHandleRequest struct {
	Method string
	Path   string
	Query  map[string][]string
}

func parseManagementRequest(raw []byte) managementHandleRequest {
	var req managementHandleRequest
	if len(raw) == 0 {
		return req
	}
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal == nil && req.Path != "" {
		return req
	}
	var snake struct {
		Method string              `json:"method"`
		Path   string              `json:"path"`
		Query  map[string][]string `json:"query"`
	}
	if errUnmarshal := json.Unmarshal(raw, &snake); errUnmarshal == nil {
		return managementHandleRequest{Method: snake.Method, Path: snake.Path, Query: snake.Query}
	}
	return req
}

// exportLimit reads the ?limit= query value for the export endpoint,
// clamped to [1, exportLimitMax], defaulting to exportLimitDefault.
const (
	exportLimitDefault = 50
	exportLimitMax     = 100
)

func exportLimit(query map[string][]string) int {
	limit := exportLimitDefault
	for _, raw := range query["limit"] {
		parsed, errParse := strconv.Atoi(strings.TrimSpace(raw))
		if errParse != nil || parsed <= 0 {
			continue
		}
		limit = parsed
		break
	}
	if limit > exportLimitMax {
		return exportLimitMax
	}
	return limit
}

// managementHandle dispatches one Management API or resource request.
//
// The host registers GET /status and GET /export under
// /v0/management/plugins/<id>/ (authenticated) and /, /data, /export under
// /v0/resource/plugins/<id>/ (unauthenticated). Both dispatches arrive here;
// the tail of the normalized path identifies the view.
func managementHandle(raw []byte) []byte {
	req := parseManagementRequest(raw)
	path := req.Path
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = path[:idx]
	}
	// Match on the last segment so both the management and resource prefixes
	// resolve to the same view.
	tail := strings.TrimRight(path, "/")
	if idx := strings.LastIndex(tail, "/"); idx >= 0 {
		tail = tail[idx+1:]
	}

	switch {
	case tail == "page" || tail == pluginIdentifier:
		// /v0/resource/plugins/<id>/page: the status HTML. The bare plugin-ID
		// tail stays reachable as a fallback for the legacy root resource.
		return envelopeResponse(plugin.HtmlManagementResponse(0, statusPageOrPlaceholder()))
	case tail == "data":
		return envelopeResponse(statusDataResponse())
	case tail == "export":
		return envelopeResponse(exportResponse(req.Query))
	case tail == "status":
		// Management API status returns the JSON snapshot directly.
		return envelopeResponse(statusDataResponse())
	default:
		return envelopeResponse(plugin.JsonManagementResponse(404, []byte(`{"error":"not found"}`)))
	}
}

// statusPageOrPlaceholder renders the HTML page, or a placeholder when no
// configuration has been delivered yet.
func statusPageOrPlaceholder() string {
	if currentEngine() == nil {
		return plugin.NotConfiguredHTML()
	}
	return plugin.StatusPageHTML()
}

// statusDataResponse serves the JSON snapshot from the active engine.
func statusDataResponse() managementResponse {
	engine := currentEngine()
	if engine == nil {
		return plugin.JsonManagementResponse(503, []byte(`{"error":"plugin not configured"}`))
	}
	snapshot, errBuild := engine.StatusSnapshot(context.Background())
	if errBuild != nil {
		return plugin.JsonManagementResponse(500, []byte(`{"error":`+strconv.Quote(errBuild.Error())+`}`))
	}
	raw, errMarshal := json.Marshal(snapshot)
	if errMarshal != nil {
		return plugin.JsonManagementResponse(500, []byte(`{"error":"marshal status snapshot"}`))
	}
	return plugin.JsonManagementResponse(0, raw)
}

// exportResponse serves the full export archive including raw observations
// and the event log.
func exportResponse(query map[string][]string) managementResponse {
	engine := currentEngine()
	if engine == nil {
		return plugin.JsonManagementResponse(503, []byte(`{"error":"plugin not configured"}`))
	}
	document, errBuild := engine.ExportDocument(context.Background(), exportLimit(query))
	if errBuild != nil {
		return plugin.JsonManagementResponse(500, []byte(`{"error":`+strconv.Quote(errBuild.Error())+`}`))
	}
	raw, errMarshal := json.Marshal(document)
	if errMarshal != nil {
		return plugin.JsonManagementResponse(500, []byte(`{"error":"marshal export document"}`))
	}
	return plugin.JsonManagementResponse(0, raw)
}

// envelopeResponse wraps a management response in the plugin RPC envelope.
func envelopeResponse(resp managementResponse) []byte {
	raw, errMarshal := okEnvelope(resp)
	if errMarshal != nil {
		return errorEnvelope("plugin_error", errMarshal.Error())
	}
	return raw
}

// hostBridge adapts the C ABI host callbacks to the engine's HostAPI.
type hostCallbacks struct{}

func (hostCallbacks) call(method string, payload any) (json.RawMessage, error) {
	rawPayload, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal host callback payload %s: %w", method, errMarshal)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var cResponse C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback payload %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &cResponse)
	var rawResponse []byte
	if cResponse.ptr != nil && cResponse.len > 0 {
		rawResponse = C.GoBytes(cResponse.ptr, C.int(cResponse.len))
	}
	if cResponse.ptr != nil {
		C.free_host_buffer(cResponse.ptr, cResponse.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(callCode))
	}
	var env envelope
	if errUnmarshal := json.Unmarshal(rawResponse, &env); errUnmarshal != nil {
		return nil, fmt.Errorf("decode host callback envelope %s: %w", method, errUnmarshal)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

type hostAuthListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

// AuthList returns the current credential snapshot.
func (b hostCallbacks) AuthList() ([]plugin.AuthEntry, error) {
	result, errCall := b.call(pluginabi.MethodHostAuthList, map[string]any{})
	if errCall != nil {
		return nil, errCall
	}
	var resp hostAuthListResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return nil, fmt.Errorf("decode host.auth.list result: %w", errUnmarshal)
	}
	entries := make([]plugin.AuthEntry, 0, len(resp.Files))
	for _, file := range resp.Files {
		entries = append(entries, plugin.AuthEntry{
			ID:          file.ID,
			Index:       file.AuthIndex,
			Type:        file.Type,
			Path:        file.Path,
			RuntimeOnly: file.RuntimeOnly,
		})
	}
	return entries, nil
}

// AuthRuntimeJSON returns the physical credential JSON and backing file path.
func (b hostCallbacks) AuthRuntimeJSON(authIndex string) ([]byte, string, error) {
	result, errCall := b.call(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if errCall != nil {
		return nil, "", errCall
	}
	var resp pluginapi.HostAuthGetResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return nil, "", fmt.Errorf("decode host.auth.get result: %w", errUnmarshal)
	}
	return resp.JSON, resp.Path, nil
}

// Log forwards a structured log entry to the host.
func (b hostCallbacks) Log(level string, message string, fields map[string]any) {
	_, errCall := b.call(pluginabi.MethodHostLog, map[string]any{
		"level":   level,
		"message": message,
		"fields":  fields,
	})
	if errCall != nil {
		_ = errCall
	}
}

func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code string, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
