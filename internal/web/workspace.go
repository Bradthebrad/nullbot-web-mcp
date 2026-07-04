package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxFetchBytes int64 = 512 * 1024

type Config struct {
	Workspace      string
	SearchProvider string
	CDPEndpoint    string
	BrowserPath    string
	AllowEval      bool
	BraveAPIKey    string
	TavilyAPIKey   string
	SerpAPIKey     string
	BingAPIKey     string
	MaxFetchBytes  int64
	HTTPClient     *http.Client
}

type WebTools struct {
	root           string
	searchProvider string
	cdpEndpoint    string
	browserPath    string
	allowEval      bool
	braveAPIKey    string
	tavilyAPIKey   string
	serpAPIKey     string
	bingAPIKey     string
	maxFetchBytes  int64
	httpClient     *http.Client
	browser        *BrowserManager
}

func New(config Config) (*WebTools, error) {
	workspace := strings.TrimSpace(config.Workspace)
	if workspace == "" {
		workspace = "."
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve absolute path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: stat %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace: not a directory: %s", root)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	searchProvider := strings.TrimSpace(config.SearchProvider)
	if searchProvider == "" {
		searchProvider = "auto"
	}
	cdpEndpoint := strings.TrimSpace(config.CDPEndpoint)
	if cdpEndpoint == "" {
		cdpEndpoint = "http://127.0.0.1:9222"
	}
	maxFetchBytes := config.MaxFetchBytes
	if maxFetchBytes <= 0 {
		maxFetchBytes = defaultMaxFetchBytes
	}

	return &WebTools{
		root:           root,
		searchProvider: searchProvider,
		cdpEndpoint:    cdpEndpoint,
		browserPath:    strings.TrimSpace(config.BrowserPath),
		allowEval:      config.AllowEval,
		braveAPIKey:    strings.TrimSpace(config.BraveAPIKey),
		tavilyAPIKey:   strings.TrimSpace(config.TavilyAPIKey),
		serpAPIKey:     strings.TrimSpace(config.SerpAPIKey),
		bingAPIKey:     strings.TrimSpace(config.BingAPIKey),
		maxFetchBytes:  maxFetchBytes,
		httpClient:     config.HTTPClient,
		browser:        NewBrowserManager(cdpEndpoint, config.HTTPClient),
	}, nil
}

// resolve converts a workspace-relative path to an absolute path under the
// configured workspace. It rejects absolute paths and parent traversal escapes.
func (w *WebTools) resolve(relPath string) (string, error) {
	clean, err := cleanWorkspacePath(relPath, "path", true)
	if err != nil {
		return "", err
	}
	full := filepath.Join(w.root, clean)
	if err := w.ensureInside(full); err != nil {
		return "", err
	}
	if err := w.ensureResolvedParentInside(full); err != nil {
		return "", err
	}
	return full, nil
}

// resolveExisting is for reads/inputs. If the final path exists and is itself a
// symlink, the symlink target must also remain inside the workspace.
func (w *WebTools) resolveExisting(relPath string) (string, error) {
	full, err := w.resolve(relPath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("path: resolve symlinks for %q: %w", filepath.ToSlash(relPath), err)
	}
	if err := w.ensureInside(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// resolveOutput is for workspace writes. It validates the output path, ensures
// the resolved parent directory stays inside the workspace, and creates missing
// parent directories when needed.
func (w *WebTools) resolveOutput(relPath string) (string, error) {
	clean, err := cleanWorkspacePath(relPath, "output_path", false)
	if err != nil {
		return "", err
	}
	full := filepath.Join(w.root, clean)
	if err := w.ensureInside(full); err != nil {
		return "", err
	}
	parent := filepath.Dir(full)
	if err := w.ensureInside(parent); err != nil {
		return "", err
	}
	if err := w.ensureResolvedParentInside(full); err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("output_path: create parent directory: %w", err)
	}
	return full, nil
}

func cleanWorkspacePath(value, field string, allowDot bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if allowDot {
			return ".", nil
		}
		return "", fmt.Errorf("%s: required workspace-relative path", field)
	}
	if filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%s: absolute paths are not allowed: %s", field, trimmed)
	}
	clean := filepath.Clean(filepath.FromSlash(trimmed))
	if clean == "." && !allowDot {
		return "", fmt.Errorf("%s: must name a file or child path, not workspace root", field)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s: path escapes workspace: %s", field, trimmed)
	}
	return clean, nil
}

func (w *WebTools) ensureResolvedParentInside(path string) error {
	parent := filepath.Dir(path)
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		if err := w.ensureInside(resolved); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebTools) ensureInside(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("path: resolve absolute path: %w", err)
	}
	root := filepath.Clean(w.root)
	abs = filepath.Clean(abs)
	if samePath(abs, root) {
		return nil
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return fmt.Errorf("path: compare with workspace: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path: escapes workspace: %s", path)
	}
	return nil
}

func samePath(a, b string) bool {
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (w *WebTools) rel(path string) string {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
