package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	gjson "github.com/tidwall/gjson"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// NftExecutor runs nft commands. The sole interface to mock for testing.
type NftExecutor interface {
	Run(args ...string) ([]byte, error)
}

// FileStore abstracts file I/O for testing.
type FileStore interface {
	WriteFile(path string, data []byte, perm os.FileMode) error
	ReadFile(path string) ([]byte, error)
	RemoveFile(path string) error
}

// NftManager owns all nftables operations: set management, proxy toggle, template rendering.
type NftManager struct {
	exec      NftExecutor
	files     FileStore
	mu        sync.Mutex
	templates *template.Template
	config    ProxyConfig
	statePath string
}

func NewNftManager(executor NftExecutor, files FileStore, proxyConfig ProxyConfig, statePath string) *NftManager {
	funcMap := template.FuncMap{
		"join": func(elems []string, sep string) string { return strings.Join(elems, sep) },
	}
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.tmpl"))

	return &NftManager{
		exec:      executor,
		files:     files,
		templates: tmpl,
		config:    proxyConfig,
		statePath: statePath,
	}
}

// --- Set Operations ---

// EnsureSetsExist creates any missing nft sets in inet fw4.
func (m *NftManager) EnsureSetsExist(setNames []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, name := range setNames {
		if m.setExists(name) {
			log.Printf("nft set %s already exists", name)
			continue
		}
		log.Printf("creating nft set %s", name)
		setType := "ipv4_addr"
		if name == "allow_v6_mac" {
			setType = "ether_addr"
		}
		if _, err := m.exec.Run("add", "set", "inet", "fw4", name,
			"{", "type", setType, ";", "flags", "interval", ";", "auto-merge", ";", "}"); err != nil {
			return fmt.Errorf("create set %s: %w", name, err)
		}
	}
	return nil
}

// GetSet returns the type and elements of an nft set.
func (m *NftManager) GetSet(setName string) (string, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getSetJSON(setName)
}

// AddToSet adds an element to an nft set and persists to file.
func (m *NftManager) AddToSet(setName, data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.exec.Run("add", "element", "inet", "fw4", setName, fmt.Sprintf("{%s}", data)); err != nil {
		return fmt.Errorf("add element to %s: %w", setName, err)
	}
	log.Printf("nft add element %s {%s}", setName, data)
	return m.syncSetToFile(setName)
}

// RemoveFromSet removes an element from an nft set and persists to file.
func (m *NftManager) RemoveFromSet(setName, data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.exec.Run("delete", "element", "inet", "fw4", setName, fmt.Sprintf("{%s}", data)); err != nil {
		return fmt.Errorf("remove element from %s: %w", setName, err)
	}
	log.Printf("nft delete element %s {%s}", setName, data)
	return m.syncSetToFile(setName)
}

// SyncAllSets persists all managed sets to files.
func (m *NftManager) SyncAllSets(setNames []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, name := range setNames {
		if err := m.syncSetToFile(name); err != nil {
			return err
		}
	}
	return nil
}

func (m *NftManager) syncSetToFile(setName string) error {
	output, err := m.exec.Run("list", "set", "inet", "fw4", setName)
	if err != nil {
		return fmt.Errorf("list set %s: %w", setName, err)
	}
	// Strip the outer "table inet fw4 {" wrapper, keep inner content
	lines := strings.Split(string(output), "\n")
	if len(lines) >= 2 {
		lines = lines[1 : len(lines)-2]
	}
	for i := range lines {
		lines[i] = strings.Replace(lines[i], "\t", "", 1)
	}
	lines = append(lines, "")
	content := strings.Join(lines, "\n")

	targetPath := filepath.Join(m.statePath, setName+".nft")
	if err := m.files.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("sync set %s to %s: %w", setName, targetPath, err)
	}
	return nil
}

// setExists checks if a named set exists by trying to list it.
func (m *NftManager) setExists(name string) bool {
	_, err := m.exec.Run("-j", "list", "set", "inet", "fw4", name)
	return err == nil
}

func (m *NftManager) getSetJSON(setName string) (string, []string, error) {
	output, err := m.exec.Run("-j", "list", "set", "inet", "fw4", setName)
	if err != nil {
		return "", nil, err
	}
	return parseNftSetJSON(output)
}

// --- Proxy Toggle ---

const (
	transparentNftPath = "/etc/transparent-proxy/transparent.nft"
	tablePostPath      = "/usr/share/nftables.d/table-post/transparent.nft"
)

// ProxyEnabled reads nft chain state to determine if proxy is active.
func (m *NftManager) ProxyEnabled() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proxyEnabled()
}

func (m *NftManager) proxyEnabled() (bool, error) {
	prerouting, err := m.exec.Run("list", "chain", "inet", "fw4", "mangle_prerouting")
	if err != nil {
		return false, fmt.Errorf("list mangle_prerouting: %w", err)
	}
	output, err := m.exec.Run("list", "chain", "inet", "fw4", "mangle_output")
	if err != nil {
		return false, fmt.Errorf("list mangle_output: %w", err)
	}
	return strings.Contains(string(prerouting), "jump transparent_proxy") ||
		strings.Contains(string(output), "jump transparent_proxy_mask"), nil
}

// EnableProxy loads transparent proxy rules and persists them.
func (m *NftManager) EnableProxy() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.flushProxyChains(); err != nil {
		return err
	}

	// Render transparent.nft template and wrap in table declaration
	partial, err := m.renderTransparent()
	if err != nil {
		return err
	}
	full := fmt.Sprintf("table inet fw4 {\n%s}\n", partial)

	// Write to temp file and load via nft -f
	tmpFile, err := os.CreateTemp("", "transparent-*.nft")
	if err != nil {
		return fmt.Errorf("create temp nft file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(full); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp nft file: %w", err)
	}
	tmpFile.Close()

	if _, err := m.exec.Run("-f", tmpPath); err != nil {
		_ = m.flushProxyChains()
		return fmt.Errorf("load transparent rules (proxy left disabled): %w", err)
	}

	// Persist partial rules for fw4 table-post (survives fw4 restart)
	if err := m.files.WriteFile(tablePostPath, []byte(partial), 0644); err != nil {
		return fmt.Errorf("proxy enabled but persistence failed: %w", err)
	}
	return nil
}

// DisableProxy flushes proxy rules and removes persistence.
func (m *NftManager) DisableProxy() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.flushProxyChains(); err != nil {
		return err
	}
	if err := m.files.RemoveFile(tablePostPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove table-post file: %w", err)
	}
	return nil
}

func (m *NftManager) flushProxyChains() error {
	if _, err := m.exec.Run("flush", "chain", "inet", "fw4", "mangle_prerouting"); err != nil {
		return fmt.Errorf("flush mangle_prerouting: %w", err)
	}
	if _, err := m.exec.Run("flush", "chain", "inet", "fw4", "mangle_output"); err != nil {
		return fmt.Errorf("flush mangle_output: %w", err)
	}
	return nil
}

// --- Template Rendering ---

// RenderAndLoadProxyRules renders proxy.nft from template and loads via nft.
func (m *NftManager) RenderAndLoadProxyRules() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	content, err := m.renderProxy()
	if err != nil {
		return err
	}

	targetPath := filepath.Join(m.statePath, "proxy.nft")
	if err := m.files.WriteFile(targetPath, content, 0644); err != nil {
		return fmt.Errorf("write proxy.nft: %w", err)
	}

	// Flush existing proxy chains to prevent rule duplication (ignore errors if chains don't exist yet)
	m.exec.Run("flush", "chain", "inet", "fw4", "transparent_proxy")
	m.exec.Run("flush", "chain", "inet", "fw4", "transparent_proxy_mask")

	// Wrap in table and load
	full := fmt.Sprintf("table inet fw4 {\n%s}\n", content)
	tmpFile, err := os.CreateTemp("", "proxy-*.nft")
	if err != nil {
		return fmt.Errorf("create temp proxy nft: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(full); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	if _, err := m.exec.Run("-f", tmpPath); err != nil {
		return fmt.Errorf("load proxy rules: %w", err)
	}
	return nil
}

func (m *NftManager) renderProxy() ([]byte, error) {
	data := struct {
		SelfMark    int
		ForcedPort  int
		DefaultPort int
	}{
		SelfMark:    m.config.SelfMark,
		ForcedPort:  m.config.ForcedPort,
		DefaultPort: m.config.DefaultPort,
	}

	var buf bytes.Buffer
	if err := m.templates.ExecuteTemplate(&buf, "proxy.nft.tmpl", data); err != nil {
		return nil, fmt.Errorf("render proxy.nft: %w", err)
	}
	return buf.Bytes(), nil
}

func (m *NftManager) renderTransparent() (string, error) {
	data := struct {
		LanInterface string
	}{
		LanInterface: m.config.LanInterface,
	}

	var buf bytes.Buffer
	if err := m.templates.ExecuteTemplate(&buf, "transparent.nft.tmpl", data); err != nil {
		return "", fmt.Errorf("render transparent.nft: %w", err)
	}
	return buf.String(), nil
}

// --- JSON Parsing (via gjson) ---

func parseNftSetNames(data []byte) ([]string, error) {
	var names []string
	gjson.GetBytes(data, "nftables.#.set.name").ForEach(func(_, v gjson.Result) bool {
		if v.Str != "" {
			names = append(names, v.Str)
		}
		return true
	})
	return names, nil
}

func parseNftSetJSON(data []byte) (string, []string, error) {
	sets := gjson.GetBytes(data, "nftables.#.set|0")
	if !sets.Exists() {
		return "", nil, fmt.Errorf("no set found in nft json output")
	}

	setType := sets.Get("type").String()
	elems := sets.Get("elem")
	if !elems.Exists() {
		return setType, nil, nil
	}

	var result []string
	for _, elem := range elems.Array() {
		s, err := parseNftElement(elem)
		if err != nil {
			return setType, result, err
		}
		result = append(result, s)
	}
	return setType, result, nil
}

func parseNftElement(v gjson.Result) (string, error) {
	if v.Type == gjson.String {
		return v.Str, nil
	}
	if prefix := v.Get("prefix"); prefix.Exists() {
		return fmt.Sprintf("%s/%d", prefix.Get("addr").String(), prefix.Get("len").Int()), nil
	}
	if r := v.Get("range"); r.Exists() {
		arr := r.Array()
		if len(arr) == 2 {
			return arr[0].String() + "-" + arr[1].String(), nil
		}
	}
	return "", fmt.Errorf("unrecognized nft element: %s", v.Raw)
}

// --- Production NftExecutor ---

// ExecNftRunner executes real nft commands.
type ExecNftRunner struct {
	timeout time.Duration
}

func NewExecNftRunner(timeout time.Duration) *ExecNftRunner {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ExecNftRunner{timeout: timeout}
}

func (r *ExecNftRunner) Run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nft", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		cmdStr := "nft " + strings.Join(args, " ")
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.Bytes(), fmt.Errorf("command %q timed out after %s", cmdStr, r.timeout)
		}
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return stdout.Bytes(), fmt.Errorf("command %q: %w; stderr: %s", cmdStr, err, stderrStr)
		}
		return stdout.Bytes(), fmt.Errorf("command %q: %w", cmdStr, err)
	}
	return stdout.Bytes(), nil
}

// --- Production FileStore ---

// OSFileStore is the production FileStore using real filesystem operations.
type OSFileStore struct{}

func (OSFileStore) WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return os.WriteFile(path, data, perm)
}

func (OSFileStore) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (OSFileStore) RemoveFile(path string) error {
	return os.Remove(path)
}

// --- HTTP Fetcher (for chnroute) ---

// RemoteFetcher fetches data from a URL.
type RemoteFetcher interface {
	Fetch(url string) ([]byte, error)
}

// HTTPFetcher is the production RemoteFetcher.
type HTTPFetcher struct {
	Timeout time.Duration
}

func (f HTTPFetcher) Fetch(fetchURL string) ([]byte, error) {
	client := &http.Client{Timeout: f.timeout()}

	resp, err := client.Get(fetchURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", fetchURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch %s: status %d", fetchURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", fetchURL, err)
	}
	return body, nil
}

func (f HTTPFetcher) timeout() time.Duration {
	if f.Timeout <= 0 {
		return 30 * time.Second
	}
	return f.Timeout
}
