// Package authfile applies the plugin-chosen proxy to credential files.
//
// Every write re-reads the file from disk, replaces only the top-level
// proxy_url member, and replaces the file through a temporary file plus an
// atomic rename so concurrent token refreshes are never clobbered with a
// stale full-file rewrite.
package authfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ProxyURLField is the top-level credential JSON member carrying the
// per-credential proxy. It takes precedence over the global proxy and is
// hot-reloaded by the CPA watcher.
const ProxyURLField = "proxy_url"

// ReadProxyURL returns the current top-level proxy_url value of the
// credential file. Missing members read as empty.
func ReadProxyURL(path string) (string, error) {
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return "", fmt.Errorf("read auth file %s: %w", path, errRead)
	}
	return extractProxyURL(raw), nil
}

// ApplyProxy rewrites only proxy_url in the credential file at path.
//
// The file is re-read immediately before the write so changes made by other
// writers (for example token refresh) survive; the result lands on disk via
// a same-directory temporary file and an atomic rename.
func ApplyProxy(path string, proxyURL string) error {
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return fmt.Errorf("read auth file %s: %w", path, errRead)
	}
	if extractProxyURL(raw) == proxyURL {
		return nil
	}
	updated, errSet := sjson.SetBytes(raw, ProxyURLField, proxyURL)
	if errSet != nil {
		return fmt.Errorf("set %s in auth file %s: %w", ProxyURLField, path, errSet)
	}
	return writeFileAtomic(path, updated)
}

// ClearProxy removes the top-level proxy_url member from the credential file.
func ClearProxy(path string) error {
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return fmt.Errorf("read auth file %s: %w", path, errRead)
	}
	if extractProxyURL(raw) == "" {
		return nil
	}
	updated, errDelete := sjson.DeleteBytes(raw, ProxyURLField)
	if errDelete != nil {
		return fmt.Errorf("delete %s in auth file %s: %w", ProxyURLField, path, errDelete)
	}
	return writeFileAtomic(path, updated)
}

func extractProxyURL(raw []byte) string {
	return gjson.GetBytes(raw, ProxyURLField).String()
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, errCreate := os.CreateTemp(dir, filepath.Base(path)+".cpa-auto-proxy-test-*")
	if errCreate != nil {
		return fmt.Errorf("create temp file for %s: %w", path, errCreate)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, errWrite := tmp.Write(data); errWrite != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file for %s: %w", path, errWrite)
	}
	if errChmod := tmp.Chmod(0o600); errChmod != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file for %s: %w", path, errChmod)
	}
	if errSync := tmp.Sync(); errSync != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file for %s: %w", path, errSync)
	}
	if errClose := tmp.Close(); errClose != nil {
		return fmt.Errorf("close temp file for %s: %w", path, errClose)
	}
	if errRename := renameWithRetry(tmpName, path); errRename != nil {
		return errRename
	}
	tmpName = ""
	return nil
}

// renameWithRetry replaces the destination through an atomic rename. Windows
// briefly locks the destination during a replace, so a concurrent writer may
// cause a transient access-denied error; a short bounded retry keeps the
// write atomic while absorbing the contention window.
func renameWithRetry(tmpName string, dst string) error {
	const attempts = 5
	const delay = 20 * time.Millisecond
	var errRename error
	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(delay)
		}
		errRename = os.Rename(tmpName, dst)
		if errRename == nil {
			return nil
		}
	}
	return fmt.Errorf("rename auth file %s: %w", dst, errRename)
}
