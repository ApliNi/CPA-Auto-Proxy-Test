//go:build ignore

// Command build compiles the plugin for all supported platforms into build/
// and packages release assets matching the CLIProxyAPI Plugins Store specification.
//
// Usage (from the repository root):
//
//	go run build.go            # build and package all targets into build/
//	go run build.go -target windows/amd64
//	go run build.go -target windows/amd64 -keep
//	go run build.go -version 0.2.0
//
// The //go:build ignore tag keeps this script out of the plugin package (its
// main would collide with main.go); "go run build.go" still works because the
// file is named explicitly. Windows AMD64 needs a Windows C compiler and
// therefore runs under WSL with the mingw-w64 cross toolchain; the other
// targets cross-compile from Linux with the native gcc or zig cc (Windows
// ARM64).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// target describes one build output.
type target struct {
	// goos/goarch select the Go toolchain target.
	goos, goarch string
	// libExt is the dynamic library extension ("dll" or "so").
	libExt string
	// zigTarget is the zig cc target triple when zig is required.
	// Empty means the plain gcc named by cc.
	zigTarget string
	// cc is the C compiler command for CGO; it may reference environment
	// entries prepared by the caller (see buildInWSL).
	cc string
}

// name returns the "goos/goarch" identifier of the target.
func (t target) name() string { return t.goos + "/" + t.goarch }

// libName returns the dynamic library filename inside the release zip root (<pluginID>.<ext>).
func (t target) libName(pluginID string) string {
	return pluginID + "." + t.libExt
}

// zipName returns the packaged asset name (<pluginID>_<version>_<goos>_<goarch>.zip).
func (t target) zipName(pluginID, version string) string {
	return fmt.Sprintf("%s_%s_%s_%s.zip", pluginID, version, t.goos, t.goarch)
}

// allTargets lists every supported build output.
func allTargets() []target {
	return []target{
		{goos: "windows", goarch: "amd64", libExt: "dll",
			cc: "x86_64-w64-mingw32-gcc"},
		{goos: "windows", goarch: "arm64", libExt: "dll",
			zigTarget: "aarch64-windows-gnu"},
		{goos: "linux", goarch: "amd64", libExt: "so",
			cc: "gcc"},
		{goarch: "arm64", goos: "linux", libExt: "so",
			cc: "aarch64-linux-gnu-gcc"},
	}
}

// wslHeader generates the shell prologue executed inside WSL. It performs
// toolchain discovery so the Windows host does not need a C compiler. build/
// is cleared first unless keep is set (used for partial -target builds).
func wslHeader(keep bool) string {
	header := `set -e
cd /mnt/c/ApliNi/__/AutoProxy
mkdir -p build
`
	if keep {
		header += "# -keep: preserving existing build/ contents\n"
	} else {
		header += "rm -rf build/*\n"
	}
	header += `ZIG=""
for d in "$HOME"/zig-x86_64-linux-*; do
	[ -x "$d/zig" ] && ZIG="$d/zig" && break
done
mkdir -p "$HOME/bin"
printf '#!/bin/bash\n[ $# -eq 0 ] && exit 0\nexec "%s" cc -target "$ZIG_TARGET" "$@"\n' "$ZIG" > "$HOME/bin/zig-cc-wrapper"
chmod +x "$HOME/bin/zig-cc-wrapper"
build() { # goos goarch lib_name zip_name cc zigtarget
	local goos="$1" goarch="$2" lib="$3" zip="$4" cc="$5" zig="$6"
	local tmplib="build/.tmp_$lib"
	if [ -n "$zig" ]; then
		if [ -z "$ZIG" ]; then echo "zig not found for $goos/$goarch" >&2; return 1; fi
		ZIG_TARGET="$zig" CC="$HOME/bin/zig-cc-wrapper" GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=1 \
			go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o "$tmplib" .
	else
		CC="$cc" GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=1 \
			go build -trimpath -buildmode=c-shared -ldflags="-s -w" -o "$tmplib" .
	fi
	rm -f "build/.tmp_"*.h
	python3 -c "import zipfile; z = zipfile.ZipFile('build/' + '$zip', 'w', compression=zipfile.ZIP_DEFLATED); z.write('$tmplib', arcname='$lib'); z.close()"
	rm -f "$tmplib" "build/.tmp_"*.h
	echo "packaged build/$zip (contains $lib)"
}
`
	return header
}

func main() {
	pluginID := "CPA-Auto-Proxy-Test"
	version := "0.1.0"
	targetFlag := flag.String("target", "all", "target to build (goos/goarch), or \"all\"")
	versionFlag := flag.String("version", version, "plugin release version (without leading v, e.g. 0.1.0)")
	keepFlag := flag.Bool("keep", false, "keep existing build/ contents instead of clearing them")
	flag.Parse()

	// The whole build runs inside WSL: Windows lacks a C toolchain, while WSL
	// carries gcc, mingw-w64, the ARM64 cross libc, and zig for Windows ARM64.
	if runtime.GOOS == "windows" {
		if err := runInWSL(pluginID, *versionFlag, *targetFlag, *keepFlag); err != nil {
			fatal(err)
		}
		return
	}
	fatal(fmt.Errorf("run this script from Windows so it can drive WSL; direct %s builds are not wired up", runtime.GOOS))
}

// runInWSL pipes the generated build script into WSL and streams its output.
func runInWSL(pluginID, version, targetFlag string, keep bool) error {
	var script strings.Builder
	script.WriteString(wslHeader(keep))
	targets := allTargets()
	if targetFlag != "all" {
		matched := make([]target, 0, 1)
		for _, t := range targets {
			if t.name() == targetFlag {
				matched = append(matched, t)
			}
		}
		if len(matched) == 0 {
			return fmt.Errorf("unknown target %q (available: all, %s)", targetFlag, targetNames(targets))
		}
		targets = matched
	}
	for _, t := range targets {
		libName := t.libName(pluginID)
		zipName := t.zipName(pluginID, version)
		fmt.Fprintf(&script, "build %s %s %s %s %s %s\n",
			quote(t.goos), quote(t.goarch), quote(libName), quote(zipName), quote(t.cc), quote(t.zigTarget))
	}
	// Generate checksums.txt over all .zip files in build/
	script.WriteString(`(cd build && sha256sum *.zip > checksums.txt)` + "\n")
	script.WriteString("echo \"generated build/checksums.txt:\"\n")
	script.WriteString("cat build/checksums.txt\n")
	script.WriteString("ls -la build/\n")

	// The script goes over stdin: the WSL interop layer rewrites argument
	// quoting when a multi-line script is passed via the command line, while
	// stdin content arrives verbatim. -e bypasses the shell-parsing layer.
	cmd := exec.Command("wsl", "-e", "bash", "-s")
	cmd.Stdin = strings.NewReader(script.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("WSL build failed: %w", err)
	}
	return nil
}

// quote shell-escapes one argument for the generated script.
func quote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// targetNames lists target identifiers for error messages.
func targetNames(targets []target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.name()
	}
	return strings.Join(names, ", ")
}

// fatal prints an error and exits nonzero.
func fatal(err error) {
	fmt.Fprintf(os.Stderr, "build: %v\n", err)
	os.Exit(1)
}
