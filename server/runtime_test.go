package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"
	"time"
)

type fakeCommand struct {
	stdout string
	stderr string
	runErr error

	stdoutWriter io.Writer
	stderrWriter io.Writer
}

func (c *fakeCommand) Run() error {
	if c.stdoutWriter != nil {
		_, _ = io.WriteString(c.stdoutWriter, c.stdout)
	}
	if c.stderrWriter != nil {
		_, _ = io.WriteString(c.stderrWriter, c.stderr)
	}
	return c.runErr
}

func (c *fakeCommand) SetStdout(w io.Writer) {
	c.stdoutWriter = w
}

func (c *fakeCommand) SetStderr(w io.Writer) {
	c.stderrWriter = w
}

type fakeRunner struct {
	output []byte
	err    error

	name string
	args []string
}

func (r *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	return r.output, r.err
}

type fakeFetcher struct {
	body []byte
	err  error

	url string
}

func (f *fakeFetcher) Fetch(url string) ([]byte, error) {
	f.url = url
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

type fakeFileWriter struct {
	name string
	data []byte
	perm os.FileMode
	err  error
}

func (f *fakeFileWriter) WriteFile(name string, data []byte, perm os.FileMode) error {
	f.name = name
	f.data = append([]byte(nil), data...)
	f.perm = perm
	return f.err
}

type recordingRunner struct {
	commands []string
}

func (r *recordingRunner) Run(name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, commandLine(name, args...))
	return []byte("ok\n"), nil
}

type fakeProxyFiles struct {
	readData map[string][]byte
	removed  []string
	writes   map[string][]byte
	perms    map[string]os.FileMode
}

func newFakeProxyFiles(readData map[string][]byte) *fakeProxyFiles {
	cloned := make(map[string][]byte, len(readData))
	for path, data := range readData {
		cloned[path] = append([]byte(nil), data...)
	}
	return &fakeProxyFiles{
		readData: cloned,
		writes:   map[string][]byte{},
		perms:    map[string]os.FileMode{},
	}
}

func (f *fakeProxyFiles) WriteFile(name string, data []byte, perm os.FileMode) error {
	f.writes[name] = append([]byte(nil), data...)
	f.perms[name] = perm
	return nil
}

func (f *fakeProxyFiles) ReadFile(name string) ([]byte, error) {
	content, ok := f.readData[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), content...), nil
}

func (f *fakeProxyFiles) Remove(name string) error {
	f.removed = append(f.removed, name)
	return nil
}

func TestNftRunnerSuccess(t *testing.T) {
	const timeout = 3 * time.Second
	start := time.Now()
	var gotDeadline time.Time
	var gotName string
	var gotArgs []string
	runner := NftRunner{
		timeout: timeout,
		newCommand: func(ctx context.Context, name string, args ...string) runnableCommand {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("Run() context missing deadline")
			}
			gotDeadline = deadline
			gotName = name
			gotArgs = append([]string(nil), args...)
			return &fakeCommand{stdout: "ok\n"}
		},
	}

	output, err := runner.Run("nft", "list", "set", "inet", "fw4", "direct_dst")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(output) != "ok\n" {
		t.Fatalf("Run() output = %q, want %q", output, "ok\\n")
	}
	if gotName != "nft" {
		t.Fatalf("Run() command name = %q, want %q", gotName, "nft")
	}
	deadlineDelta := gotDeadline.Sub(start)
	if deadlineDelta < timeout-500*time.Millisecond || deadlineDelta > timeout+500*time.Millisecond {
		t.Fatalf("Run() deadline delta = %s, want about %s", deadlineDelta, timeout)
	}
	wantArgs := []string{"list", "set", "inet", "fw4", "direct_dst"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("Run() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestNftRunnerFailure(t *testing.T) {
	runner := NftRunner{
		newCommand: func(ctx context.Context, name string, args ...string) runnableCommand {
			return &fakeCommand{
				stderr: "permission denied\ntry as root\n",
				runErr: errors.New("exit status 1"),
			}
		},
	}

	_, err := runner.Run("nft", "list", "set", "inet", "fw4", "direct_dst")
	if err == nil {
		t.Fatal("Run() error = nil, want wrapped error")
	}
	if !strings.Contains(err.Error(), "nft list set inet fw4 direct_dst") {
		t.Fatalf("Run() error = %q, want command context", err)
	}
	if !strings.Contains(err.Error(), "permission denied try as root") {
		t.Fatalf("Run() error = %q, want stderr summary", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("Run() error = %q, want original error", err)
	}
}

func TestGetSetJsonWithFakeRunner(t *testing.T) {
	runner := &fakeRunner{output: []byte(`{
		"nftables": [
			{"set": {"type": "ipv4_addr", "elem": [
				"1.1.1.1",
				{"prefix": {"addr": "10.0.0.0", "len": 24}},
				{"range": ["2.2.2.2", "2.2.2.3"]}
			]}}
		]
	}`)}
	runtime := Runtime{Runner: runner}

	typ, elems, err := getSetJsonWithRuntime(runtime, "direct_dst")
	if err != nil {
		t.Fatalf("getSetJsonWithRuntime() error = %v", err)
	}
	if typ != "ipv4_addr" {
		t.Fatalf("getSetJsonWithRuntime() type = %q, want %q", typ, "ipv4_addr")
	}
	wantElems := []string{"1.1.1.1", "10.0.0.0/24", "2.2.2.2-2.2.2.3"}
	if !reflect.DeepEqual(elems, wantElems) {
		t.Fatalf("getSetJsonWithRuntime() elems = %#v, want %#v", elems, wantElems)
	}
	if runner.name != "nft" {
		t.Fatalf("runner name = %q, want %q", runner.name, "nft")
	}
	wantArgs := []string{"-j", "list", "set", "inet", "fw4", "direct_dst"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestRemoteFetchFailureWrapped(t *testing.T) {
	url := "http://example.invalid/data"
	const timeout = 7 * time.Second
	fetcher := HTTPRemoteFetcher{
		timeout: timeout,
		get: func(client *http.Client, gotURL string) (*http.Response, error) {
			if gotURL != url {
				t.Fatalf("Fetch() url = %q, want %q", gotURL, url)
			}
			if client.Timeout != timeout {
				t.Fatalf("Fetch() client timeout = %s, want %s", client.Timeout, timeout)
			}
			return nil, errors.New("dial tcp: no route to host")
		},
	}

	_, err := fetcher.Fetch(url)
	if err == nil {
		t.Fatal("Fetch() error = nil, want wrapped error")
	}
	if !strings.Contains(err.Error(), url) {
		t.Fatalf("Fetch() error = %q, want url context", err)
	}
	if !strings.Contains(err.Error(), "no route to host") {
		t.Fatalf("Fetch() error = %q, want original error", err)
	}
}

func TestRuntimeTimeoutDefaults(t *testing.T) {
	runtime := NewRuntime()
	wantTimeouts := DefaultRuntimeTimeouts()
	if runtime.Timeouts != wantTimeouts {
		t.Fatalf("NewRuntime() timeouts = %#v, want %#v", runtime.Timeouts, wantTimeouts)
	}

	runner, ok := runtime.Runner.(NftRunner)
	if !ok {
		t.Fatalf("NewRuntime() runner type = %T, want NftRunner", runtime.Runner)
	}
	if runner.commandTimeout() != wantTimeouts.Command {
		t.Fatalf("NewRuntime() command timeout = %s, want %s", runner.commandTimeout(), wantTimeouts.Command)
	}

	fetcher, ok := runtime.Fetcher.(HTTPRemoteFetcher)
	if !ok {
		t.Fatalf("NewRuntime() fetcher type = %T, want HTTPRemoteFetcher", runtime.Fetcher)
	}
	if fetcher.fetchTimeout() != wantTimeouts.RemoteFetch {
		t.Fatalf("NewRuntime() fetch timeout = %s, want %s", fetcher.fetchTimeout(), wantTimeouts.RemoteFetch)
	}
	if fetcher.httpClient().Timeout != wantTimeouts.RemoteFetch {
		t.Fatalf("NewRuntime() http client timeout = %s, want %s", fetcher.httpClient().Timeout, wantTimeouts.RemoteFetch)
	}
}

func TestHTTPRemoteFetcherUsesConfiguredTimeout(t *testing.T) {
	const timeout = 11 * time.Second
	url := "http://example.invalid/route"
	fetcher := HTTPRemoteFetcher{
		timeout: timeout,
		get: func(client *http.Client, gotURL string) (*http.Response, error) {
			if gotURL != url {
				t.Fatalf("Fetch() url = %q, want %q", gotURL, url)
			}
			if client.Timeout != timeout {
				t.Fatalf("Fetch() client timeout = %s, want %s", client.Timeout, timeout)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		},
	}

	body, err := fetcher.Fetch(url)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("Fetch() body = %q, want %q", body, "ok")
	}
}

func TestSyncSetWithFakeWriter(t *testing.T) {
	runner := &fakeRunner{output: []byte("set direct_dst {\n\t1.1.1.1,\n}\n")}
	writer := &fakeFileWriter{}
	runtime := Runtime{Runner: runner, Files: writer}

	if err := syncSetWithRuntime(runtime, "/tmp/state", "direct_dst"); err != nil {
		t.Fatalf("syncSetWithRuntime() error = %v", err)
	}
	if writer.name != "/tmp/state/direct_dst.nft" {
		t.Fatalf("WriteFile() path = %q, want %q", writer.name, "/tmp/state/direct_dst.nft")
	}
	if string(writer.data) != "1.1.1.1,\n" {
		t.Fatalf("WriteFile() data = %q, want %q", writer.data, "1.1.1.1,\\n")
	}
	if writer.perm != 0664 {
		t.Fatalf("WriteFile() perm = %v, want %v", writer.perm, os.FileMode(0664))
	}
}

func TestGetCHNRouteWithFakeFetcher(t *testing.T) {
	fetcher := &fakeFetcher{body: []byte(strings.Join([]string{
		"# comment",
		"apnic|CN|ipv4|1.0.1.0|256|20110414|allocated",
		"apnic|JP|ipv4|1.0.2.0|256|20110414|allocated",
		"apnic|CN|ipv4|1.0.8.0|1024|20110414|allocated",
	}, "\n"))}
	runtime := Runtime{Fetcher: fetcher}

	ips, err := getCHNRouteWithRuntime(runtime)
	if err != nil {
		t.Fatalf("getCHNRouteWithRuntime() error = %v", err)
	}
	want := []string{"1.0.1.0/24", "1.0.8.0/22"}
	if !reflect.DeepEqual(ips, want) {
		t.Fatalf("getCHNRouteWithRuntime() ips = %#v, want %#v", ips, want)
	}
	if fetcher.url != chnRouteURL {
		t.Fatalf("Fetch() url = %q, want %q", fetcher.url, chnRouteURL)
	}
}

func TestWriteTemplateFileUsesFileWriter(t *testing.T) {
	dir := t.TempDir()
	tmpl := mustTemplate(t, "{{.Name}}={{len .Elements}}\n")
	writer := &fakeFileWriter{}
	runtime := Runtime{Files: writer}
	targetPath := filepath.Join(dir, "chnroute.nft")

	if err := writeTemplateFile(runtime, targetPath, 0640, tmpl, NftSet{Name: "chnroute", Elements: []string{"1.1.1.1/32"}}); err != nil {
		t.Fatalf("writeTemplateFile() error = %v", err)
	}
	if writer.name != targetPath {
		t.Fatalf("WriteFile() path = %q, want %q", writer.name, targetPath)
	}
	if string(writer.data) != "chnroute=1\n" {
		t.Fatalf("WriteFile() data = %q, want %q", writer.data, "chnroute=1\\n")
	}
	if writer.perm != 0640 {
		t.Fatalf("WriteFile() perm = %v, want %v", writer.perm, os.FileMode(0640))
	}
}

func TestEnableProxyMatchesScriptBehavior(t *testing.T) {
	runner := &recordingRunner{}
	files := newFakeProxyFiles(map[string][]byte{
		transparentNftPartialPath: []byte("partial-rules\n"),
	})
	runtime := Runtime{Runner: runner, Files: files}

	if err := runtime.enableProxy(); err != nil {
		t.Fatalf("enableProxy() error = %v", err)
	}

	wantCommands := []string{
		"nft flush chain inet fw4 mangle_prerouting",
		"nft flush chain inet fw4 mangle_output",
		"nft -f /etc/transparent-proxy/transparent_full.nft",
	}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("enableProxy() commands = %#v, want %#v", runner.commands, wantCommands)
	}
	if !reflect.DeepEqual(files.removed, []string{transparentNftTargetPath}) {
		t.Fatalf("enableProxy() removed files = %#v, want %#v", files.removed, []string{transparentNftTargetPath})
	}
	written, ok := files.writes[transparentNftTargetPath]
	if !ok {
		t.Fatalf("enableProxy() target %s not written", transparentNftTargetPath)
	}
	if string(written) != "partial-rules\n" {
		t.Fatalf("enableProxy() target content = %q, want %q", string(written), "partial-rules\\n")
	}
	if files.perms[transparentNftTargetPath] != 0644 {
		t.Fatalf("enableProxy() target perm = %v, want %v", files.perms[transparentNftTargetPath], os.FileMode(0644))
	}
}

func TestDisableProxyMatchesScriptBehavior(t *testing.T) {
	runner := &recordingRunner{}
	files := newFakeProxyFiles(nil)
	runtime := Runtime{Runner: runner, Files: files}

	if err := runtime.disableProxy(); err != nil {
		t.Fatalf("disableProxy() error = %v", err)
	}

	wantCommands := []string{
		"nft flush chain inet fw4 mangle_prerouting",
		"nft flush chain inet fw4 mangle_output",
	}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("disableProxy() commands = %#v, want %#v", runner.commands, wantCommands)
	}
	if !reflect.DeepEqual(files.removed, []string{transparentNftTargetPath}) {
		t.Fatalf("disableProxy() removed files = %#v, want %#v", files.removed, []string{transparentNftTargetPath})
	}
	if len(files.writes) != 0 {
		t.Fatalf("disableProxy() writes = %#v, want no writes", files.writes)
	}
}

func TestDevMockRunnerSupportsProxyToggleState(t *testing.T) {
	mockRunner := NewDevMockRunner()
	runtime := Runtime{Runner: mockRunner}

	enabled, err := proxyEnabledWithRuntime(runtime)
	if err != nil {
		t.Fatalf("proxyEnabledWithRuntime() initial error = %v", err)
	}
	if enabled {
		t.Fatal("proxyEnabledWithRuntime() initial enabled = true, want false")
	}

	if _, err := runtime.nft("-f", transparentNftFullPath); err != nil {
		t.Fatalf("runtime.nft(-f) error = %v", err)
	}
	enabled, err = proxyEnabledWithRuntime(runtime)
	if err != nil {
		t.Fatalf("proxyEnabledWithRuntime() after load error = %v", err)
	}
	if !enabled {
		t.Fatal("proxyEnabledWithRuntime() after load enabled = false, want true")
	}

	if _, err := runtime.nft("flush", "chain", "inet", "fw4", "mangle_forward"); err != nil {
		t.Fatalf("runtime.nft(flush non-proxy chain) error = %v", err)
	}
	enabled, err = proxyEnabledWithRuntime(runtime)
	if err != nil {
		t.Fatalf("proxyEnabledWithRuntime() after non-proxy flush error = %v", err)
	}
	if !enabled {
		t.Fatal("proxyEnabledWithRuntime() after non-proxy flush enabled = false, want true")
	}

	if _, err := runtime.nft("flush", "chain", "inet", "fw4", "mangle_prerouting"); err != nil {
		t.Fatalf("runtime.nft(flush chain) error = %v", err)
	}
	enabled, err = proxyEnabledWithRuntime(runtime)
	if err != nil {
		t.Fatalf("proxyEnabledWithRuntime() after flush error = %v", err)
	}
	if enabled {
		t.Fatal("proxyEnabledWithRuntime() after flush enabled = true, want false")
	}
}

func TestCheckerSetProxyEnabledWorksWithDevMockRunner(t *testing.T) {
	runtime := Runtime{
		Runner: NewDevMockRunner(),
		Files: newFakeProxyFiles(map[string][]byte{
			transparentNftPartialPath: []byte("table inet fw4 {}\n"),
		}),
	}
	nftSvc := NewNftService(runtime)
	service := NewCheckerService(CheckerConfig{}, runtime, nftSvc)

	if err := service.SetProxyEnabled(true); err != nil {
		t.Fatalf("SetProxyEnabled(true) error = %v", err)
	}
	enabled, err := nftSvc.ProxyEnabledFromNft()
	if err != nil {
		t.Fatalf("ProxyEnabledFromNft() after enable error = %v", err)
	}
	if !enabled {
		t.Fatal("ProxyEnabledFromNft() after enable = false, want true")
	}

	if err := service.SetProxyEnabled(true); err != nil {
		t.Fatalf("SetProxyEnabled(true) second call error = %v", err)
	}
	enabled, err = nftSvc.ProxyEnabledFromNft()
	if err != nil {
		t.Fatalf("ProxyEnabledFromNft() after second enable error = %v", err)
	}
	if !enabled {
		t.Fatal("ProxyEnabledFromNft() after second enable = false, want true")
	}

	if err := service.SetProxyEnabled(false); err != nil {
		t.Fatalf("SetProxyEnabled(false) error = %v", err)
	}
	enabled, err = nftSvc.ProxyEnabledFromNft()
	if err != nil {
		t.Fatalf("ProxyEnabledFromNft() after disable error = %v", err)
	}
	if enabled {
		t.Fatal("ProxyEnabledFromNft() after disable = true, want false")
	}

	if err := service.SetProxyEnabled(false); err != nil {
		t.Fatalf("SetProxyEnabled(false) second call error = %v", err)
	}
	enabled, err = nftSvc.ProxyEnabledFromNft()
	if err != nil {
		t.Fatalf("ProxyEnabledFromNft() after second disable error = %v", err)
	}
	if enabled {
		t.Fatal("ProxyEnabledFromNft() after second disable = true, want false")
	}
}

func mustTemplate(t *testing.T, content string) *template.Template {
	t.Helper()
	tmpl, err := template.New("test").Parse(content)
	if err != nil {
		t.Fatalf("template parse error = %v", err)
	}
	return tmpl
}
