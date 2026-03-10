package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"
)

const chnRouteURL = "http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest"

const (
	transparentNftFullPath    = "/etc/transparent-proxy/transparent_full.nft"
	transparentNftPartialPath = "/etc/transparent-proxy/transparent.nft"
	transparentNftTargetPath  = "/usr/share/nftables.d/table-post/transparent.nft"
)

const (
	DefaultCommandTimeout     = 10 * time.Second
	DefaultRemoteFetchTimeout = 30 * time.Second
)

var appRuntime = NewRuntime()

type RuntimeTimeouts struct {
	Command     time.Duration
	RemoteFetch time.Duration
}

type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type RemoteFetcher interface {
	Fetch(url string) ([]byte, error)
}

type FileWriter interface {
	WriteFile(name string, data []byte, perm os.FileMode) error
}

type FileReader interface {
	ReadFile(name string) ([]byte, error)
}

type FileRemover interface {
	Remove(name string) error
}

type Runtime struct {
	Runner   CommandRunner
	Fetcher  RemoteFetcher
	Files    FileWriter
	Timeouts RuntimeTimeouts
}

func NewRuntime() Runtime {
	timeouts := DefaultRuntimeTimeouts()
	return Runtime{
		Runner:   NewNftRunner(timeouts.Command),
		Fetcher:  NewHTTPRemoteFetcher(timeouts.RemoteFetch),
		Files:    OSFileWriter{},
		Timeouts: timeouts,
	}
}

func DefaultRuntimeTimeouts() RuntimeTimeouts {
	return RuntimeTimeouts{
		Command:     DefaultCommandTimeout,
		RemoteFetch: DefaultRemoteFetchTimeout,
	}
}

func (r Runtime) runtimeTimeouts() RuntimeTimeouts {
	timeouts := r.Timeouts
	if timeouts.Command <= 0 {
		timeouts.Command = DefaultCommandTimeout
	}
	if timeouts.RemoteFetch <= 0 {
		timeouts.RemoteFetch = DefaultRemoteFetchTimeout
	}
	return timeouts
}

func (r Runtime) nft(args ...string) ([]byte, error) {
	runner := r.Runner
	if runner == nil {
		runner = NewNftRunner(r.runtimeTimeouts().Command)
	}
	return runner.Run("nft", args...)
}

func (r Runtime) command(name string, args ...string) ([]byte, error) {
	runner := r.Runner
	if runner == nil {
		runner = NewNftRunner(r.runtimeTimeouts().Command)
	}
	return runner.Run(name, args...)
}

func (r Runtime) fetch(url string) ([]byte, error) {
	fetcher := r.Fetcher
	if fetcher == nil {
		fetcher = NewHTTPRemoteFetcher(r.runtimeTimeouts().RemoteFetch)
	}
	return fetcher.Fetch(url)
}

func (r Runtime) writeFile(name string, data []byte, perm os.FileMode) error {
	files := r.Files
	if files == nil {
		files = OSFileWriter{}
	}
	return files.WriteFile(name, data, perm)
}

func (r Runtime) readFile(name string) ([]byte, error) {
	if reader, ok := r.Files.(FileReader); ok {
		content, err := reader.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read file %s fail: %w", name, err)
		}
		return content, nil
	}

	content, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read file %s fail: %w", name, err)
	}
	return content, nil
}

func (r Runtime) removeFileIfExists(name string) error {
	if remover, ok := r.Files.(FileRemover); ok {
		err := remover.Remove(name)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove file %s fail: %w", name, err)
	}

	err := os.Remove(name)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove file %s fail: %w", name, err)
}

func (r Runtime) disableProxy() error {
	if err := r.flushProxyChains(); err != nil {
		return err
	}
	if err := r.removeFileIfExists(transparentNftTargetPath); err != nil {
		return err
	}
	return nil
}

func (r Runtime) enableProxy() error {
	if err := r.flushProxyChains(); err != nil {
		return err
	}
	if _, err := r.nft("-f", transparentNftFullPath); err != nil {
		return fmt.Errorf("load nft rules from %s fail: %w", transparentNftFullPath, err)
	}
	if err := r.removeFileIfExists(transparentNftTargetPath); err != nil {
		return err
	}
	content, err := r.readFile(transparentNftPartialPath)
	if err != nil {
		return err
	}
	if err := r.writeFile(transparentNftTargetPath, content, 0644); err != nil {
		return err
	}
	return nil
}

func (r Runtime) flushProxyChains() error {
	if _, err := r.nft("flush", "chain", "inet", "fw4", "mangle_prerouting"); err != nil {
		return fmt.Errorf("flush chain inet fw4 mangle_prerouting fail: %w", err)
	}
	if _, err := r.nft("flush", "chain", "inet", "fw4", "mangle_output"); err != nil {
		return fmt.Errorf("flush chain inet fw4 mangle_output fail: %w", err)
	}
	return nil
}

type runnableCommand interface {
	Run() error
	SetStdout(io.Writer)
	SetStderr(io.Writer)
}

type execCommand struct {
	*exec.Cmd
}

func (c *execCommand) SetStdout(w io.Writer) {
	c.Stdout = w
}

func (c *execCommand) SetStderr(w io.Writer) {
	c.Stderr = w
}

type CommandFactory func(ctx context.Context, name string, args ...string) runnableCommand

type NftRunner struct {
	timeout    time.Duration
	newCommand CommandFactory
}

func NewNftRunner(timeout time.Duration) NftRunner {
	timeout = normalizeTimeout(timeout, DefaultCommandTimeout)
	return NftRunner{
		timeout: timeout,
		newCommand: func(ctx context.Context, name string, args ...string) runnableCommand {
			return &execCommand{Cmd: exec.CommandContext(ctx, name, args...)}
		},
	}
}

func (r NftRunner) commandTimeout() time.Duration {
	return normalizeTimeout(r.timeout, DefaultCommandTimeout)
}

func (r NftRunner) Run(name string, args ...string) ([]byte, error) {
	timeout := r.commandTimeout()
	newCommand := r.newCommand
	if newCommand == nil {
		newCommand = NewNftRunner(timeout).newCommand
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command := newCommand(ctx, name, args...)
	command.SetStdout(&stdout)
	command.SetStderr(&stderr)

	if err := command.Run(); err != nil {
		failureMessage := fmt.Sprintf("run command %q fail", commandLine(name, args...))
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failureMessage = fmt.Sprintf("run command %q timeout after %s", commandLine(name, args...), timeout)
		}
		stderrSummary := summarizeCommandOutput(stderr.String())
		if stderrSummary == "" {
			stderrSummary = summarizeCommandOutput(stdout.String())
		}
		if stderrSummary != "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w; stderr: %s", failureMessage, err, stderrSummary)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w", failureMessage, err)
	}

	return stdout.Bytes(), nil
}

type HTTPRemoteFetcher struct {
	timeout time.Duration
	client  *http.Client
	get     func(client *http.Client, url string) (*http.Response, error)
}

func NewHTTPRemoteFetcher(timeout time.Duration) HTTPRemoteFetcher {
	timeout = normalizeTimeout(timeout, DefaultRemoteFetchTimeout)
	return HTTPRemoteFetcher{
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
		get:     defaultHTTPGet,
	}
}

func (f HTTPRemoteFetcher) fetchTimeout() time.Duration {
	return normalizeTimeout(f.timeout, DefaultRemoteFetchTimeout)
}

func (f HTTPRemoteFetcher) httpClient() *http.Client {
	if f.client != nil {
		return f.client
	}
	return &http.Client{Timeout: f.fetchTimeout()}
}

func (f HTTPRemoteFetcher) Fetch(url string) ([]byte, error) {
	client := f.httpClient()
	get := f.get
	if get == nil {
		get = defaultHTTPGet
	}

	resp, err := get(client, url)
	if err != nil {
		if isTimeoutError(err) {
			return nil, fmt.Errorf("fetch %s timeout after %s: %w", url, client.Timeout, err)
		}
		return nil, fmt.Errorf("fetch %s fail: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s fail: unexpected status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response from %s fail: %w", url, err)
	}

	return body, nil
}

func defaultHTTPGet(client *http.Client, url string) (*http.Response, error) {
	return client.Get(url)
}

type OSFileWriter struct{}

func (OSFileWriter) WriteFile(name string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(name, data, perm); err != nil {
		return fmt.Errorf("write file %s fail: %w", name, err)
	}
	return nil
}

func (OSFileWriter) ReadFile(name string) ([]byte, error) {
	content, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (OSFileWriter) Remove(name string) error {
	return os.Remove(name)
}

func writeTemplateFile(runtime Runtime, targetPath string, perm os.FileMode, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render template for %s fail: %w", targetPath, err)
	}
	if err := runtime.writeFile(targetPath, buf.Bytes(), perm); err != nil {
		return err
	}
	return nil
}

func commandLine(name string, args ...string) string {
	parts := append([]string{name}, args...)
	return strings.Join(parts, " ")
}

func summarizeCommandOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	summary := strings.Join(strings.Fields(trimmed), " ")
	const limit = 160
	if len(summary) > limit {
		return summary[:limit-3] + "..."
	}
	return summary
}

func normalizeTimeout(timeout, fallback time.Duration) time.Duration {
	if timeout <= 0 {
		return fallback
	}
	return timeout
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	type timeout interface {
		Timeout() bool
	}
	var timeoutErr timeout
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}
