package sidecar

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	// ErrPortInUse is returned when the target port is already bound.
	ErrPortInUse = errors.New("port is already in use")
	// ErrServerBinNotFound is returned when whisper-server.exe cannot be found.
	ErrServerBinNotFound = errors.New("whisper-server.exe binary not found")
	// ErrModelNotFound is returned when the specified model file does not exist.
	ErrModelNotFound = errors.New("whisper model file not found")
)

// AppDirs holds resolved directories for the v2c sidecar.
type AppDirs struct {
	Root   string
	Bin    string
	Models string
}

// GetAppDirs returns standard directories under %LOCALAPPDATA%/v2c/.
func GetAppDirs() (AppDirs, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			localAppData = os.TempDir()
		} else {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
	}

	root := filepath.Join(localAppData, "v2c")
	return AppDirs{
		Root:   root,
		Bin:    filepath.Join(root, "bin"),
		Models: filepath.Join(root, "models"),
	}, nil
}

// EnsureDirectories creates the %LOCALAPPDATA%/v2c/ directories if they don't exist.
func EnsureDirectories() (AppDirs, error) {
	dirs, err := GetAppDirs()
	if err != nil {
		return dirs, err
	}

	for _, dir := range []string{dirs.Root, dirs.Bin, dirs.Models} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return dirs, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return dirs, nil
}

// SyncLocalBinaries scans for whisper-server and runtime DLLs in local folders (e.g. ./bin/)
// and copies them to %LOCALAPPDATA%/v2c/bin/ if not already present.
func SyncLocalBinaries(dirs AppDirs) error {
	candidateSources := []string{
		"bin",
		filepath.Join(".", "bin"),
		".",
	}

	for _, src := range candidateSources {
		fi, err := os.Stat(src)
		if err != nil || !fi.IsDir() {
			continue
		}

		entries, err := os.ReadDir(src)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			lower := strings.ToLower(name)
			// Copy executables, CUDA DLLs, and model files
			if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".dll") {
				dstPath := filepath.Join(dirs.Bin, name)
				srcPath := filepath.Join(src, name)
				_ = copyFileIfMissing(srcPath, dstPath)
			} else if strings.HasSuffix(lower, ".bin") || strings.HasSuffix(lower, ".gguf") {
				dstPath := filepath.Join(dirs.Models, name)
				srcPath := filepath.Join(src, name)
				_ = copyFileIfMissing(srcPath, dstPath)
			}
		}
	}

	return nil
}

func copyFileIfMissing(src, dst string) error {
	dstStat, dstErr := os.Stat(dst)
	srcStat, srcErr := os.Stat(src)
	if srcErr != nil {
		return srcErr
	}

	// If destination exists and matches size, don't re-copy
	if dstErr == nil && dstStat.Size() == srcStat.Size() {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// ResolveServerBinary locates whisper-server.exe across AppData, local folders, and PATH.
func ResolveServerBinary(hint string) (string, error) {
	dirs, _ := GetAppDirs()

	candidates := []string{
		hint,
		filepath.Join(dirs.Bin, "whisper-server.exe"),
		filepath.Join("bin", "whisper-server.exe"),
		"whisper-server.exe",
		"./whisper-server.exe",
	}

	for _, path := range candidates {
		if path == "" {
			continue
		}
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(path)
			if err == nil {
				return abs, nil
			}
			return path, nil
		}
	}

	// Search PATH
	if p, err := exec.LookPath("whisper-server.exe"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("whisper-server"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("%w (searched %%LOCALAPPDATA%%\\v2c\\bin, ./bin, and PATH)", ErrServerBinNotFound)
}

// ResolveModelPath finds the model file given a path or filename.
func ResolveModelPath(modelArg string) (string, error) {
	dirs, _ := GetAppDirs()

	candidates := []string{
		modelArg,
		filepath.Join(dirs.Models, modelArg),
		filepath.Join(dirs.Models, filepath.Base(modelArg)),
		filepath.Join("models", modelArg),
		filepath.Join("models", filepath.Base(modelArg)),
	}

	for _, path := range candidates {
		if path == "" {
			continue
		}
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(path)
			if err == nil {
				return abs, nil
			}
			return path, nil
		}
	}

	return "", fmt.Errorf("%w: %q (place model in models/ or %%LOCALAPPDATA%%\\v2c\\models\\)", ErrModelNotFound, modelArg)
}

// CheckPortAvailable verifies that the specified TCP port is free.
func CheckPortAvailable(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: %s (%v)", ErrPortInUse, addr, err)
	}
	_ = ln.Close()
	return nil
}

// IsWhisperResponding checks whether a live whisper-server is already active at the endpoint.
func IsWhisperResponding(baseURL string) bool {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}
