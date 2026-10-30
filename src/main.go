// Command cpa-auto-proxy-test is a CLIProxyAPI plugin that automatically routes
// credentials of the configured auth type to the best available proxy by
// observing request failures through the usage hook and rewriting the
// proxy_url member of the credential files.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/config"
	"github.com/ApliNi/CPA-Auto-Proxy-Test/src/plugin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const pluginIdentifier = "cpa-auto-proxy-test"

var pluginVersion = "0.2.1"

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
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
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
	defer engineMu.Unlock()
	if activeEngine == nil {
		created, errNew := plugin.New(cfg, activeHost)
		if errNew != nil {
			return errNew
		}
		activeEngine = created
		return nil
	}
	return activeEngine.Reconfigure(cfg)
}

func shutdownEngine() {
	engineMu.Lock()
	engine := activeEngine
	activeEngine = nil
	engineMu.Unlock()
	if engine != nil {
		engine.Close()
	}
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		req, errLifecycle := parseLifecycleRequest(request)
		if errLifecycle != nil {
			return nil, errLifecycle
		}
		if errConfigure := configure(req.ConfigYAML); errConfigure != nil {
			return nil, errConfigure
		}
		return okEnvelope(pluginRegistration(req.SchemaVersion))
	case pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		shutdownEngine()
		return okEnvelope(map[string]any{"ok": true})
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegister())
	case pluginabi.MethodManagementHandle:
		return managementHandle(request), nil
	case pluginabi.MethodUsageHandle:
		engine := currentEngine()
		if engine == nil {
			return okEnvelope(map[string]any{"ok": true})
		}
		var record pluginapi.UsageRecord
		if errUnmarshal := json.Unmarshal(request, &record); errUnmarshal != nil {
			return nil, fmt.Errorf("decode usage record: %w", errUnmarshal)
		}
		engine.HandleUsage(record)
		return okEnvelope(map[string]any{"ok": true})
	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
}

func parseLifecycleRequest(raw []byte) (lifecycleRequest, error) {
	var req lifecycleRequest
	if len(raw) == 0 {
		return req, nil
	}
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal == nil {
		return req, nil
	}
	var strReq struct {
		ConfigYAML    string `json:"config_yaml"`
		SchemaVersion uint32 `json:"schema_version"`
	}
	if errUnmarshal := json.Unmarshal(raw, &strReq); errUnmarshal == nil {
		return lifecycleRequest{
			ConfigYAML:    []byte(strReq.ConfigYAML),
			SchemaVersion: strReq.SchemaVersion,
		}, nil
	}
	return req, fmt.Errorf("decode lifecycle request: %s", string(raw))
}

func configure(configYAML []byte) error {
	cfg, errParse := config.Parse(configYAML)
	if errParse != nil {
		return fmt.Errorf("parse plugin config: %w", errParse)
	}
	return replaceEngine(cfg)
}

func pluginRegistration(schemaVersion uint32) registration {
	if schemaVersion == 0 || schemaVersion > pluginabi.SchemaVersion {
		schemaVersion = pluginabi.SchemaVersion
	}
	return registration{
		SchemaVersion: schemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginIdentifier,
			Version:          pluginVersion,
			Author:           "ApliNi",
			GitHubRepository: "https://github.com/ApliNi/CPA-Auto-Proxy-Test",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "auth-type", Type: pluginapi.ConfigFieldTypeString, Description: "Comma-separated credential types managed by this plugin (default empty, manages none)."},
				{Name: "proxies", Type: pluginapi.ConfigFieldTypeArray, Description: "Candidate proxy URLs, one full URL per entry."},
				{Name: "failure-patterns", Type: pluginapi.ConfigFieldTypeArray, Description: "Failure matchers: regular expressions for the response body or plain numeric status codes (empty matches all failures)."},
				{Name: "window-hours", Type: pluginapi.ConfigFieldTypeInteger, Description: "Length of the recent observation window in hours (default 24)."},
				{Name: "min-recent-samples", Type: pluginapi.ConfigFieldTypeInteger, Description: "Minimum recent observation samples required before recent success rate takes full weight (default 5)."},
				{Name: "failure-cooldown-sec", Type: pluginapi.ConfigFieldTypeInteger, Description: "Base failure cooldown duration in seconds with exponential backoff on consecutive failures (default 15)."},
				{Name: "switch-debounce-sec", Type: pluginapi.ConfigFieldTypeInteger, Description: "Switch debounce and in-flight failure isolation window in seconds to suppress rapid secondary switches and protect new proxy stats (default 10)."},
			},
		},
		Capabilities: registrationCapability{
			UsagePlugin:   true,
			ManagementAPI: true,
		},
	}
}

// managementRegister declares the plugin's Management API and resource
// routes.
//
// The host normalizes ManagementRoute paths under /v0/management and
// ResourceRoute paths under /v0/resource/plugins/<pluginID>.
//
// Resource routes provide unauthenticated browser UI shell only:
//   - GET /v0/resource/plugins/cpa-auto-proxy-test/page
//
// Management routes provide authenticated endpoints protected by the
// CPA Management Key:
//   - GET /v0/management/cpa-auto-proxy-test/status
//   - GET /v0/management/cpa-auto-proxy-test/export
//   - GET /v0/management/cpa-auto-proxy-test/page
func managementRegister() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: "GET", Path: "/" + pluginIdentifier + "/status"},
			{Method: "GET", Path: "/" + pluginIdentifier + "/export"},
			{Method: "GET", Path: "/" + pluginIdentifier + "/page"},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/page", Menu: "CPA-Auto-Proxy-Test", Description: "代理路由状态"},
			{Path: "/" + pluginIdentifier + "/page", Description: "代理路由状态 (兼容路径)"},
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

func cleanRoutePath(p string) string {
	if idx := strings.IndexAny(p, "?#"); idx >= 0 {
		p = p[:idx]
	}
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func isResourceRoute(cleanPath string) bool {
	lower := strings.ToLower(cleanPath)
	return strings.HasPrefix(lower, "/v0/resource/") || strings.HasPrefix(lower, "/resource/")
}

func isManagementRoute(cleanPath string) bool {
	lower := strings.ToLower(cleanPath)
	idPrefix := "/" + strings.ToLower(pluginIdentifier)
	return strings.HasPrefix(lower, "/v0/management/") ||
		strings.HasPrefix(lower, "/management/") ||
		strings.HasPrefix(lower, idPrefix+"/") ||
		lower == idPrefix
}

// managementHandle dispatches one Management API or resource request.
//
// In compliance with CPA Resource Route Boundary specifications:
//   - Resource routes (/v0/resource/plugins/<id>/...) only serve the static
//     HTML shell (e.g. /page, /index.html, or root) without branching on
//     runtime engine state. Status snapshots and export endpoints are rejected
//     with 404 to protect sensitive credentials and routing metadata.
//   - Management routes (/v0/management/<id>/...) serve status and export
//     under host management key authentication.
func managementHandle(raw []byte) []byte {
	req := parseManagementRequest(raw)
	cleanPath := cleanRoutePath(req.Path)

	tail := strings.TrimRight(cleanPath, "/")
	if idx := strings.LastIndex(tail, "/"); idx >= 0 {
		tail = tail[idx+1:]
	}

	if isResourceRoute(cleanPath) {
		switch {
		case strings.EqualFold(tail, "page"),
			strings.EqualFold(tail, "index.html"),
			strings.EqualFold(tail, pluginIdentifier),
			strings.EqualFold(tail, "cpa-auto-proxy-test"),
			tail == "":
			return envelopeResponse(plugin.HtmlManagementResponse(http.StatusOK, plugin.StatusPageHTML()))
		default:
			return envelopeResponse(plugin.JsonManagementResponse(http.StatusNotFound, []byte(`{"error":"not found"}`)))
		}
	}

	if !isManagementRoute(cleanPath) {
		return envelopeResponse(plugin.JsonManagementResponse(http.StatusNotFound, []byte(`{"error":"not found"}`)))
	}

	switch {
	case strings.EqualFold(tail, "page"),
		strings.EqualFold(tail, "index.html"),
		strings.EqualFold(tail, pluginIdentifier),
		strings.EqualFold(tail, "cpa-auto-proxy-test"),
		tail == "":
		return envelopeResponse(plugin.HtmlManagementResponse(http.StatusOK, plugin.StatusPageHTML()))
	case strings.EqualFold(tail, "status"):
		return envelopeResponse(statusDataResponse())
	case strings.EqualFold(tail, "export"):
		return envelopeResponse(exportResponse(req.Query))
	default:
		return envelopeResponse(plugin.JsonManagementResponse(http.StatusNotFound, []byte(`{"error":"not found"}`)))
	}
}

// statusDataResponse serves the JSON snapshot from the active engine.
func statusDataResponse() managementResponse {
	engine := currentEngine()
	if engine == nil {
		return plugin.JsonManagementResponse(http.StatusServiceUnavailable, []byte(`{"error":"plugin not configured"}`))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, errBuild := engine.StatusSnapshot(ctx)
	if errBuild != nil {
		return plugin.JsonManagementResponse(http.StatusInternalServerError, []byte(`{"error":`+strconv.Quote(errBuild.Error())+`}`))
	}
	raw, errMarshal := json.Marshal(snap)
	if errMarshal != nil {
		return plugin.JsonManagementResponse(http.StatusInternalServerError, []byte(`{"error":"marshal status snapshot"}`))
	}
	return plugin.JsonManagementResponse(http.StatusOK, raw)
}

// exportResponse serves the full export archive including raw observations
// and the event log.
func exportResponse(query map[string][]string) managementResponse {
	engine := currentEngine()
	if engine == nil {
		return plugin.JsonManagementResponse(http.StatusServiceUnavailable, []byte(`{"error":"plugin not configured"}`))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc, errBuild := engine.ExportDocument(ctx, exportLimit(query))
	if errBuild != nil {
		return plugin.JsonManagementResponse(http.StatusInternalServerError, []byte(`{"error":`+strconv.Quote(errBuild.Error())+`}`))
	}
	raw, errMarshal := json.Marshal(doc)
	if errMarshal != nil {
		return plugin.JsonManagementResponse(http.StatusInternalServerError, []byte(`{"error":"marshal export document"}`))
	}
	return plugin.JsonManagementResponse(http.StatusOK, raw)
}

// envelopeResponse wraps a management response in the plugin RPC envelope.
func envelopeResponse(resp managementResponse) []byte {
	raw, errMarshal := okEnvelope(resp)
	if errMarshal != nil {
		return errorEnvelope("marshal_error", errMarshal.Error())
	}
	return raw
}

// hostBridge adapts the C ABI host callbacks to the engine's HostAPI.
type hostCallbacks struct{}

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
	_, _ = b.call(pluginabi.MethodHostLog, map[string]any{
		"level":   level,
		"message": message,
		"fields":  fields,
	})
}

func okEnvelope(v any) ([]byte, error) {
	result, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func errorEnvelope(code string, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}
