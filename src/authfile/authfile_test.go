package authfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
)

func writeTempAuthFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential.json")
	if errWrite := os.WriteFile(path, []byte(content), 0o600); errWrite != nil {
		t.Fatalf("write temp auth file: %v", errWrite)
	}
	return path
}

func TestReadProxyURLEmpty(t *testing.T) {
	path := writeTempAuthFile(t, `{"type":"Antigravity","access_token":"x"}`)
	got, err := ReadProxyURL(path)
	if err != nil {
		t.Fatalf("ReadProxyURL error = %v", err)
	}
	if got != "" {
		t.Fatalf("ReadProxyURL = %q, want empty", got)
	}
}

func TestReadProxyURLOnlyReadsTopLevel(t *testing.T) {
	path := writeTempAuthFile(t, `{"type":"Antigravity","nested":{"proxy_url":"http://inner"},"proxy_url":"http://outer"}`)
	got, err := ReadProxyURL(path)
	if err != nil {
		t.Fatalf("ReadProxyURL error = %v", err)
	}
	if got != "http://outer" {
		t.Fatalf("ReadProxyURL = %q, want top-level value", got)
	}
}

func TestApplyProxyPreservesOtherContent(t *testing.T) {
	path := writeTempAuthFile(t, "{\n  \"type\": \"Antigravity\",\n  \"access_token\": \"tok\",\n  \"refresh_token\": \"ref\"\n}")
	if errApply := ApplyProxy(path, "http://proxy-a:8080"); errApply != nil {
		t.Fatalf("ApplyProxy error = %v", errApply)
	}
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read back: %v", errRead)
	}
	content := string(raw)
	if !strings.Contains(content, `"access_token": "tok"`) || !strings.Contains(content, `"refresh_token": "ref"`) {
		t.Fatalf("other content lost: %s", content)
	}
	if gjson.GetBytes(raw, "proxy_url").String() != "http://proxy-a:8080" {
		t.Fatalf("proxy_url missing: %s", content)
	}
	if gjson.GetBytes(raw, "type").String() != "Antigravity" || gjson.GetBytes(raw, "access_token").String() != "tok" {
		t.Fatalf("parsed content broken: %s", content)
	}
	if !json.Valid(raw) {
		t.Fatalf("result is not valid JSON: %s", content)
	}
}

func TestApplyProxyUpdatesExistingValue(t *testing.T) {
	path := writeTempAuthFile(t, `{"type":"Antigravity","proxy_url":"http://old:1"}`)
	if errApply := ApplyProxy(path, "http://new:2"); errApply != nil {
		t.Fatalf("ApplyProxy error = %v", errApply)
	}
	got, err := ReadProxyURL(path)
	if err != nil {
		t.Fatalf("ReadProxyURL error = %v", err)
	}
	if got != "http://new:2" {
		t.Fatalf("ReadProxyURL = %q, want http://new:2", got)
	}
}

func TestApplyProxyNoopWhenSame(t *testing.T) {
	path := writeTempAuthFile(t, `{"type":"Antigravity","proxy_url":"http://same:1"}`)
	infoBefore, errStat := os.Stat(path)
	if errStat != nil {
		t.Fatalf("stat before: %v", errStat)
	}
	if errApply := ApplyProxy(path, "http://same:1"); errApply != nil {
		t.Fatalf("ApplyProxy error = %v", errApply)
	}
	infoAfter, errStat := os.Stat(path)
	if errStat != nil {
		t.Fatalf("stat after: %v", errStat)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Fatal("ApplyProxy with identical value must not rewrite the file")
	}
}

func TestClearProxyRemovesMember(t *testing.T) {
	path := writeTempAuthFile(t, `{"type":"Antigravity","proxy_url":"http://old:1","access_token":"x"}`)
	if errClear := ClearProxy(path); errClear != nil {
		t.Fatalf("ClearProxy error = %v", errClear)
	}
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read back: %v", errRead)
	}
	if strings.Contains(string(raw), "proxy_url") {
		t.Fatalf("proxy_url still present: %s", raw)
	}
	if errClear := ClearProxy(path); errClear != nil {
		t.Fatalf("ClearProxy noop error = %v", errClear)
	}
}

func TestApplyProxyErrorsOnMissingFile(t *testing.T) {
	if errApply := ApplyProxy(filepath.Join(t.TempDir(), "missing.json"), "http://a"); errApply == nil {
		t.Fatal("ApplyProxy expected error for missing file, got nil")
	}
}

func TestApplyProxyAtomicUnderConcurrency(t *testing.T) {
	path := writeTempAuthFile(t, `{"type":"Antigravity","access_token":"refreshed"}`)
	var wg sync.WaitGroup
	proxies := []string{"http://p1", "http://p2", "http://p3", "http://p4"}
	for _, proxy := range proxies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if errApply := ApplyProxy(path, proxy); errApply != nil {
				t.Errorf("ApplyProxy error = %v", errApply)
			}
		}()
	}
	wg.Wait()
	got, errRead := ReadProxyURL(path)
	if errRead != nil {
		t.Fatalf("ReadProxyURL error = %v", errRead)
	}
	found := false
	for _, proxy := range proxies {
		if got == proxy {
			found = true
		}
	}
	if !found {
		t.Fatalf("ReadProxyURL = %q, want one of the applied proxies", got)
	}
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read back: %v", errRead)
	}
	if !strings.Contains(string(raw), `"access_token":"refreshed"`) {
		t.Fatalf("concurrent writes lost other content: %s", raw)
	}
	entries, errReadDir := os.ReadDir(filepath.Dir(path))
	if errReadDir != nil {
		t.Fatalf("read dir: %v", errReadDir)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".cpa-auto-proxy-test-") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}
