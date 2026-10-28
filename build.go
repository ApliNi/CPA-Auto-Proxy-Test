//go:build ignore

// Command build compiles the plugin for all supported platforms into build/
// and packages release assets matching the CLIProxyAPI Plugins Store specification.
//
// Output artifacts follow the format:
//   build/cpa-auto-proxy-test_<version>_<goos>_<goarch>.zip
// containing the dynamic library:
//   cpa-auto-proxy-test.<ext> (dll, so, dylib)
//
// Usage (from the repository root):
//
//	go run build.go
//	go run build.go -target windows/amd64
//	go run build.go -target windows/amd64 -keep
//	go run build.go -version 0.2.0
//	go run build.go -no-wsl
package main

import (
	"archive/zip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// target describes one build output.
type target struct {
	goos      string
	goarch    string
	libExt    string
	zigTarget string
	defaultCC string
}

func (t target) name() string {
	return t.goos + "/" + t.goarch
}

// libName returns the dynamic library filename inside the release zip root (cpa-auto-proxy-test.<ext>).
func (t target) libName(pluginID string) string {
	return pluginID + "." + t.libExt
}

// zipName returns the packaged asset name (cpa-auto-proxy-test_<version>_<goos>_<goarch>.zip).
func (t target) zipName(pluginID, version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.zip", pluginID, version, t.goos, t.goarch)
}

func allTargets() []target {
	return []target{
		{
			goos:      "windows",
			goarch:    "amd64",
			libExt:    "dll",
			zigTarget: "x86_64-windows-gnu",
			defaultCC: "x86_64-w64-mingw32-gcc",
		},
		{
			goos:      "windows",
			goarch:    "arm64",
			libExt:    "dll",
			zigTarget: "aarch64-windows-gnu",
			defaultCC: "aarch64-w64-mingw32-gcc",
		},
		{
			goos:      "linux",
			goarch:    "amd64",
			libExt:    "so",
			zigTarget: "x86_64-linux-gnu",
			defaultCC: "x86_64-linux-gnu-gcc",
		},
		{
			goos:      "linux",
			goarch:    "arm64",
			libExt:    "so",
			zigTarget: "aarch64-linux-gnu",
			defaultCC: "aarch64-linux-gnu-gcc",
		},
		{
			goos:      "darwin",
			goarch:    "amd64",
			libExt:    "dylib",
			zigTarget: "x86_64-macos",
			defaultCC: "o64-clang",
		},
		{
			goos:      "darwin",
			goarch:    "arm64",
			libExt:    "dylib",
			zigTarget: "aarch64-macos",
			defaultCC: "oa64-clang",
		},
	}
}

func targetNames(targets []target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.name()
	}
	return strings.Join(names, ", ")
}

func findZig() string {
	// 1. ZIG environment variable
	if envZig := os.Getenv("ZIG"); envZig != "" {
		if path, err := exec.LookPath(envZig); err == nil {
			return path
		}
		if fi, err := os.Stat(envZig); err == nil && !fi.IsDir() {
			return envZig
		}
	}

	// 2. PATH lookup
	if path, err := exec.LookPath("zig"); err == nil {
		return path
	}
	if runtime.GOOS == "windows" {
		if path, err := exec.LookPath("zig.exe"); err == nil {
			return path
		}
	}

	// 3. Common installation paths
	var searchDirs []string
	if runtime.GOOS == "windows" {
		home := os.Getenv("USERPROFILE")
		localApp := os.Getenv("LOCALAPPDATA")
		progFiles := os.Getenv("ProgramFiles")
		if home != "" {
			if matches, _ := filepath.Glob(filepath.Join(home, "zig*")); matches != nil {
				searchDirs = append(searchDirs, matches...)
			}
			searchDirs = append(searchDirs, filepath.Join(home, "scoop", "apps", "zig", "current"))
			if scoopMatches, _ := filepath.Glob(filepath.Join(home, "scoop", "apps", "zig", "*")); scoopMatches != nil {
				searchDirs = append(searchDirs, scoopMatches...)
			}
		}
		if localApp != "" {
			if matches, _ := filepath.Glob(filepath.Join(localApp, "zig*")); matches != nil {
				searchDirs = append(searchDirs, matches...)
			}
		}
		if progFiles != "" {
			if matches, _ := filepath.Glob(filepath.Join(progFiles, "zig*")); matches != nil {
				searchDirs = append(searchDirs, matches...)
			}
		}
		for _, dir := range searchDirs {
			candidate := filepath.Join(dir, "zig.exe")
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate
			}
		}
	} else {
		home := os.Getenv("HOME")
		if home != "" {
			if matches, _ := filepath.Glob(filepath.Join(home, "zig*")); matches != nil {
				searchDirs = append(searchDirs, matches...)
			}
			searchDirs = append(searchDirs, filepath.Join(home, ".local", "bin"))
		}
		searchDirs = append(searchDirs, "/usr/local/bin", "/opt")
		if optMatches, _ := filepath.Glob("/opt/zig*"); optMatches != nil {
			searchDirs = append(searchDirs, optMatches...)
		}
		for _, dir := range searchDirs {
			candidate := filepath.Join(dir, "zig")
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate
			}
		}
	}

	return ""
}

func findFallbackCompiler(t target) string {
	// If building for the current host, look for native gcc or clang
	if t.goos == runtime.GOOS && t.goarch == runtime.GOARCH {
		if runtime.GOOS == "darwin" {
			if path, err := exec.LookPath("clang"); err == nil {
				return path
			}
			if path, err := exec.LookPath("gcc"); err == nil {
				return path
			}
		} else {
			if path, err := exec.LookPath("gcc"); err == nil {
				return path
			}
			if path, err := exec.LookPath("clang"); err == nil {
				return path
			}
		}
	}

	// Check for target-specific cross compiler in PATH
	if t.defaultCC != "" {
		if path, err := exec.LookPath(t.defaultCC); err == nil {
			return path
		}
	}

	return ""
}

func canBuildLocally(targets []target, zigPath string) bool {
	if zigPath != "" {
		return true
	}
	for _, t := range targets {
		if findFallbackCompiler(t) == "" {
			return false
		}
	}
	return true
}

func printMissingCompilerError(targets []target) error {
	var missing []string
	for _, t := range targets {
		if findFallbackCompiler(t) == "" {
			missing = append(missing, t.name())
		}
	}
	return fmt.Errorf("no C compiler available for target(s): %s\n"+
		"CGO compilation requires a C compiler.\n"+
		"To compile CPA plugins across platforms, install Zig (recommended):\n"+
		"  - Windows: scoop install zig / winget install zig, or https://ziglang.org/download/\n"+
		"  - Linux:   apt/dnf install zig, or download from https://ziglang.org/download/\n"+
		"  - macOS:   brew install zig\n"+
		"Alternatively, install the target's native or cross GCC toolchain.",
		strings.Join(missing, ", "))
}

func setupZigWrapper(zigPath, tmpDir string) (string, error) {
	if runtime.GOOS == "windows" {
		wrapperPath := filepath.Join(tmpDir, "zig-cc-wrapper.bat")
		content := `@echo off
if "%~1"=="" exit /b 0
"%ZIG_BIN%" cc -target %ZIG_TARGET% %ZIG_FLAGS% %*
`
		if err := os.WriteFile(wrapperPath, []byte(content), 0755); err != nil {
			return "", err
		}
		return wrapperPath, nil
	}

	wrapperPath := filepath.Join(tmpDir, "zig-cc-wrapper.sh")
	content := `#!/bin/sh
[ $# -eq 0 ] && exit 0
exec "$ZIG_BIN" cc -target "$ZIG_TARGET" $ZIG_FLAGS "$@"
`
	if err := os.WriteFile(wrapperPath, []byte(content), 0755); err != nil {
		return "", err
	}
	return wrapperPath, nil
}

func setupDarwinStubs(tmpDir string) (string, error) {
	sdkDir := filepath.Join(tmpDir, "darwin_sdk")
	usrLib := filepath.Join(sdkDir, "usr", "lib")
	cfDir := filepath.Join(sdkDir, "System", "Library", "Frameworks", "CoreFoundation.framework")
	secDir := filepath.Join(sdkDir, "System", "Library", "Frameworks", "Security.framework")

	if err := os.MkdirAll(usrLib, 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfDir, 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(secDir, 0755); err != nil {
		return "", err
	}

	resolvTBD := `--- !tapi-tbd
tbd-version: 4
targets: [ x86_64-macos, arm64-macos ]
install-name: /usr/lib/libresolv.dylib
exports:
  - targets: [ x86_64-macos, arm64-macos ]
...
`
	cfTBD := `--- !tapi-tbd
tbd-version: 4
targets: [ x86_64-macos, arm64-macos ]
install-name: /System/Library/Frameworks/CoreFoundation.framework/CoreFoundation
exports:
  - targets: [ x86_64-macos, arm64-macos ]
...
`
	secTBD := `--- !tapi-tbd
tbd-version: 4
targets: [ x86_64-macos, arm64-macos ]
install-name: /System/Library/Frameworks/Security.framework/Security
exports:
  - targets: [ x86_64-macos, arm64-macos ]
...
`

	if err := os.WriteFile(filepath.Join(usrLib, "libresolv.tbd"), []byte(resolvTBD), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(cfDir, "CoreFoundation.tbd"), []byte(cfTBD), 0644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(secDir, "Security.tbd"), []byte(secTBD), 0644); err != nil {
		return "", err
	}

	return sdkDir, nil
}

type compilerConfig struct {
	cc       string
	extraEnv map[string]string
}

func (c *compilerConfig) makeEnv(t target) []string {
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	for k := range envMap {
		upper := strings.ToUpper(k)
		if upper == "GOOS" || upper == "GOARCH" || upper == "CGO_ENABLED" || upper == "CC" {
			delete(envMap, k)
		}
	}

	envMap["GOOS"] = t.goos
	envMap["GOARCH"] = t.goarch
	envMap["CGO_ENABLED"] = "1"
	envMap["CC"] = c.cc
	for k, v := range c.extraEnv {
		envMap[k] = v
	}

	res := make([]string, 0, len(envMap))
	for k, v := range envMap {
		res = append(res, k+"="+v)
	}
	return res
}

func packageZip(zipPath, srcFilePath, arcName string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip file %s: %w", zipPath, err)
	}
	defer zipFile.Close()

	srcFile, err := os.Open(srcFilePath)
	if err != nil {
		return fmt.Errorf("open source file %s: %w", srcFilePath, err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source file %s: %w", srcFilePath, err)
	}

	w := zip.NewWriter(zipFile)

	header, err := zip.FileInfoHeader(srcInfo)
	if err != nil {
		return fmt.Errorf("create zip header: %w", err)
	}
	header.Name = arcName
	header.Method = zip.Deflate

	writer, err := w.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create zip entry: %w", err)
	}

	if _, err := io.Copy(writer, srcFile); err != nil {
		return fmt.Errorf("write entry content: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close zip writer: %w", err)
	}

	return zipFile.Close()
}

func cleanBuildDir(keep bool) error {
	buildDir := "build"
	if !keep {
		if err := os.RemoveAll(buildDir); err != nil {
			return fmt.Errorf("clean %s: %w", buildDir, err)
		}
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", buildDir, err)
	}
	return nil
}

func generateChecksums(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", dir, err)
	}

	var zipFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".zip") {
			zipFiles = append(zipFiles, entry.Name())
		}
	}
	sort.Strings(zipFiles)

	if len(zipFiles) == 0 {
		return nil
	}

	checksumPath := filepath.Join(dir, "checksums.txt")
	f, err := os.Create(checksumPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", checksumPath, err)
	}
	defer f.Close()

	var lines []string
	for _, name := range zipFiles {
		p := filepath.Join(dir, name)
		zf, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("open %s: %w", p, err)
		}

		h := sha256.New()
		if _, err := io.Copy(h, zf); err != nil {
			zf.Close()
			return fmt.Errorf("hash %s: %w", p, err)
		}
		zf.Close()

		line := fmt.Sprintf("%x  %s", h.Sum(nil), name)
		lines = append(lines, line)
		if _, err := fmt.Fprintln(f, line); err != nil {
			return fmt.Errorf("write checksum: %w", err)
		}
	}

	fmt.Printf("\ngenerated %s:\n", checksumPath)
	for _, line := range lines {
		fmt.Println(line)
	}

	fmt.Printf("\n%s/ contents:\n", dir)
	allEntries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range allEntries {
			info, err := e.Info()
			if err == nil {
				fmt.Printf("  %-45s %10d bytes\n", e.Name(), info.Size())
			} else {
				fmt.Printf("  %s\n", e.Name())
			}
		}
	}

	return nil
}

func quote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func runInWSL(targetFlag, versionFlag string, keepFlag bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	out, err := exec.Command("wsl", "wslpath", "-u", filepath.ToSlash(cwd)).Output()
	if err != nil {
		return fmt.Errorf("convert working directory via wslpath: %w", err)
	}
	wslDir := strings.TrimSpace(string(out))

	args := []string{"run", "build.go", "-no-wsl", "-target", targetFlag, "-version", versionFlag}
	if keepFlag {
		args = append(args, "-keep")
	}

	wslScript := fmt.Sprintf("export PATH=\"$PATH:/usr/local/go/bin:$HOME/go/bin\"; cd %s && go %s",
		quote(wslDir), strings.Join(args, " "))

	cmd := exec.Command("wsl", "-e", "bash", "-l", "-c", wslScript)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WSL build failed: %w", err)
	}
	return nil
}

func buildTarget(pluginID, version string, t target, zigPath, tmpDir, darwinSDKDir string) error {
	var cc string
	var extraEnv map[string]string

	if zigPath != "" {
		wrapper, err := setupZigWrapper(zigPath, tmpDir)
		if err != nil {
			return fmt.Errorf("setup zig wrapper: %w", err)
		}
		cc = wrapper
		extraEnv = map[string]string{
			"ZIG_BIN":    zigPath,
			"ZIG_TARGET": t.zigTarget,
		}
		if t.goos == "darwin" && runtime.GOOS != "darwin" {
			frameworksDir := filepath.ToSlash(filepath.Join(darwinSDKDir, "System", "Library", "Frameworks"))
			libDir := filepath.ToSlash(filepath.Join(darwinSDKDir, "usr", "lib"))
			extraEnv["ZIG_FLAGS"] = fmt.Sprintf("-F %s -L %s", frameworksDir, libDir)
		}
	} else {
		fallback := findFallbackCompiler(t)
		if fallback == "" {
			return fmt.Errorf("no C compiler available for target %s", t.name())
		}
		cc = fallback
	}

	tmpLib := filepath.Join("build", fmt.Sprintf(".tmp_%s.%s", pluginID, t.libExt))
	cmd := exec.Command("go", "build",
		"-trimpath",
		"-buildmode=c-shared",
		fmt.Sprintf("-ldflags=-s -w -X main.pluginVersion=%s", version),
		"-o", tmpLib,
		"./src",
	)
	cfg := &compilerConfig{cc: cc, extraEnv: extraEnv}
	cmd.Env = cfg.makeEnv(t)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("building %s...\n", t.name())
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compile %s: %w", t.name(), err)
	}

	zipName := t.zipName(pluginID, version)
	zipPath := filepath.Join("build", zipName)
	arcName := t.libName(pluginID)

	if err := packageZip(zipPath, tmpLib, arcName); err != nil {
		return fmt.Errorf("package %s: %w", zipName, err)
	}
	fmt.Printf("packaged %s (contains %s)\n", zipPath, arcName)

	_ = os.Remove(tmpLib)
	tmpHdr := strings.TrimSuffix(tmpLib, filepath.Ext(tmpLib)) + ".h"
	_ = os.Remove(tmpHdr)
	if hdrMatches, err := filepath.Glob(filepath.Join("build", ".tmp_*.h")); err == nil {
		for _, h := range hdrMatches {
			_ = os.Remove(h)
		}
	}

	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "build: %v\n", err)
	os.Exit(1)
}

func main() {
	pluginID := "cpa-auto-proxy-test"
	defaultVersion := "0.2.0"

	targetFlag := flag.String("target", "all", "target to build (goos/goarch), or \"all\"")
	versionFlag := flag.String("version", defaultVersion, "plugin release version (without leading v, e.g. 0.1.0)")
	keepFlag := flag.Bool("keep", false, "keep existing build/ directory contents instead of clearing them")
	noWSLFlag := flag.Bool("no-wsl", false, "disable automatic WSL fallback on Windows")
	flag.Parse()

	all := allTargets()
	var targets []target
	if *targetFlag == "all" {
		targets = all
	} else {
		for _, t := range all {
			if t.name() == *targetFlag {
				targets = append(targets, t)
				break
			}
		}
		if len(targets) == 0 {
			fatal(fmt.Errorf("unknown target %q (available: all, %s)", *targetFlag, targetNames(all)))
		}
	}

	zigPath := findZig()

	if !canBuildLocally(targets, zigPath) {
		if runtime.GOOS == "windows" && !*noWSLFlag {
			if _, err := exec.LookPath("wsl"); err == nil {
				fmt.Println("No local C compiler found on Windows; falling back to WSL...")
				if err := runInWSL(*targetFlag, *versionFlag, *keepFlag); err != nil {
					fatal(err)
				}
				return
			}
		}
		fatal(printMissingCompilerError(targets))
	}

	tmpDir, err := os.MkdirTemp("", "cpa-build-*")
	if err != nil {
		fatal(fmt.Errorf("create temporary directory: %w", err))
	}
	defer os.RemoveAll(tmpDir)

	var darwinSDKDir string
	if zigPath != "" && runtime.GOOS != "darwin" {
		needsDarwin := false
		for _, t := range targets {
			if t.goos == "darwin" {
				needsDarwin = true
				break
			}
		}
		if needsDarwin {
			sdkDir, err := setupDarwinStubs(tmpDir)
			if err != nil {
				fatal(fmt.Errorf("setup darwin SDK stubs: %w", err))
			}
			darwinSDKDir = sdkDir
		}
	}

	if err := cleanBuildDir(*keepFlag); err != nil {
		fatal(err)
	}

	for _, t := range targets {
		if err := buildTarget(pluginID, *versionFlag, t, zigPath, tmpDir, darwinSDKDir); err != nil {
			fatal(err)
		}
	}

	if err := generateChecksums("build"); err != nil {
		fatal(err)
	}
}
