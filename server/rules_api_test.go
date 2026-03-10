package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type rulesStateRunner struct {
	mu sync.Mutex

	setTypes map[string]string
	setElems map[string][]string

	callDelay     time.Duration
	currentCalls  int
	maxConcurrent int
}

func newRulesStateRunner(initial map[string][]string) *rulesStateRunner {
	setElems := make(map[string][]string, len(initial))
	setTypes := make(map[string]string, len(initial))
	for setName, elems := range initial {
		setElems[setName] = append([]string(nil), elems...)
		setTypes[setName] = "ipv4_addr"
	}
	return &rulesStateRunner{setTypes: setTypes, setElems: setElems}
}

func (r *rulesStateRunner) Run(name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.currentCalls++
	if r.currentCalls > r.maxConcurrent {
		r.maxConcurrent = r.currentCalls
	}
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.currentCalls--
		r.mu.Unlock()
	}()

	if r.callDelay > 0 {
		time.Sleep(r.callDelay)
	}

	if name != "nft" {
		return nil, fmt.Errorf("unexpected command: %s", name)
	}

	if len(args) >= 6 && args[0] == "-j" && args[1] == "list" && args[2] == "set" && args[3] == "inet" && args[4] == "fw4" {
		return r.listSetJSON(args[5])
	}
	if len(args) >= 5 && args[0] == "list" && args[1] == "set" && args[2] == "inet" && args[3] == "fw4" {
		return r.listSetText(args[4]), nil
	}
	if len(args) >= 6 && args[0] == "add" && args[1] == "element" && args[2] == "inet" && args[3] == "fw4" {
		r.addElement(args[4], parseElementArg(args[5]))
		return []byte(""), nil
	}
	if len(args) >= 6 && args[0] == "delete" && args[1] == "element" && args[2] == "inet" && args[3] == "fw4" {
		r.removeElement(args[4], parseElementArg(args[5]))
		return []byte(""), nil
	}

	return nil, fmt.Errorf("unexpected nft args: %v", args)
}

func (r *rulesStateRunner) MaxConcurrent() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxConcurrent
}

func (r *rulesStateRunner) listSetJSON(setName string) ([]byte, error) {
	r.mu.Lock()
	typ := r.setTypes[setName]
	if typ == "" {
		typ = "ipv4_addr"
	}
	elems := append([]string(nil), r.setElems[setName]...)
	r.mu.Unlock()

	payload := map[string]any{
		"nftables": []any{map[string]any{"set": map[string]any{
			"type": typ,
			"elem": elems,
		}}},
	}
	return json.Marshal(payload)
}

func (r *rulesStateRunner) listSetText(setName string) []byte {
	r.mu.Lock()
	elems := append([]string(nil), r.setElems[setName]...)
	r.mu.Unlock()

	var builder strings.Builder
	builder.WriteString("set ")
	builder.WriteString(setName)
	builder.WriteString(" {\n")
	for _, elem := range elems {
		builder.WriteString("\t")
		builder.WriteString(elem)
		builder.WriteString(",\n")
	}
	builder.WriteString("}\n")
	return []byte(builder.String())
}

func (r *rulesStateRunner) addElement(setName, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.setElems[setName]; !ok {
		r.setElems[setName] = []string{}
	}
	for _, existing := range r.setElems[setName] {
		if existing == value {
			return
		}
	}
	r.setElems[setName] = append(r.setElems[setName], value)
}

func (r *rulesStateRunner) removeElement(setName, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	elems := r.setElems[setName]
	kept := make([]string, 0, len(elems))
	for _, elem := range elems {
		if elem != value {
			kept = append(kept, elem)
		}
	}
	r.setElems[setName] = kept
}

func parseElementArg(arg string) string {
	trimmed := strings.TrimSpace(arg)
	trimmed = strings.TrimPrefix(trimmed, "{")
	trimmed = strings.TrimSuffix(trimmed, "}")
	return strings.TrimSpace(trimmed)
}

type collectingFileWriter struct {
	mu    sync.Mutex
	data  map[string]string
	perms map[string]os.FileMode
}

func newCollectingFileWriter() *collectingFileWriter {
	return &collectingFileWriter{data: map[string]string{}, perms: map[string]os.FileMode{}}
}

func (w *collectingFileWriter) WriteFile(name string, data []byte, perm os.FileMode) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data[name] = string(data)
	w.perms[name] = perm
	return nil
}

func (w *collectingFileWriter) fileContent(name string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	content, ok := w.data[name]
	return content, ok
}

func newRulesRouter(config *AppConfig, runtime Runtime) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	app := NewApp(config, runtime)
	registerAPIRoutes(router.Group("/api"), apiServer{app: app})
	return router
}

func performJSONRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	requestBody := bytes.NewBufferString(body)
	req := httptest.NewRequest(method, path, requestBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestRulesList(t *testing.T) {
	runner := newRulesStateRunner(map[string][]string{
		"proxy_src": {"1.1.1.1", "10.0.0.0/24"},
	})
	config := &AppConfig{Nft: NftConfig{StatePath: "/tmp/state", Sets: []string{"proxy_src"}}}
	router := newRulesRouter(config, Runtime{Runner: runner})

	recorder := performJSONRequest(router, http.MethodGet, "/api/rules", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	env := decodeContractEnvelope(t, recorder)
	if env.Code != APICodeOK {
		t.Fatalf("response code = %q, want %q", env.Code, APICodeOK)
	}

	var data struct {
		Sets  []string      `json:"sets"`
		Rules []RuleSetView `json:"rules"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data error = %v", err)
	}
	if !reflect.DeepEqual(data.Sets, []string{"proxy_src"}) {
		t.Fatalf("sets = %#v, want %#v", data.Sets, []string{"proxy_src"})
	}
	if len(data.Rules) != 1 {
		t.Fatalf("rules len = %d, want 1", len(data.Rules))
	}
	if data.Rules[0].Name != "proxy_src" {
		t.Fatalf("rule set name = %q, want %q", data.Rules[0].Name, "proxy_src")
	}
	if !reflect.DeepEqual(data.Rules[0].Elems, []string{"1.1.1.1", "10.0.0.0/24"}) {
		t.Fatalf("rule elems = %#v, want %#v", data.Rules[0].Elems, []string{"1.1.1.1", "10.0.0.0/24"})
	}
}

func TestRulesAddRemove(t *testing.T) {
	runner := newRulesStateRunner(map[string][]string{
		"proxy_src": {"1.1.1.1"},
	})
	config := &AppConfig{Nft: NftConfig{StatePath: "/tmp/state", Sets: []string{"proxy_src"}}}
	router := newRulesRouter(config, Runtime{Runner: runner})

	addRecorder := performJSONRequest(router, http.MethodPost, "/api/rules/add", `{"set":"proxy_src","ip":"10.0.0.0/24"}`)
	if addRecorder.Code != http.StatusOK {
		t.Fatalf("add status code = %d, want %d", addRecorder.Code, http.StatusOK)
	}
	addEnv := decodeContractEnvelope(t, addRecorder)
	var addData struct {
		Set       string      `json:"set"`
		IP        string      `json:"ip"`
		Rule      RuleSetView `json:"rule"`
		Operation struct {
			Action string `json:"action"`
			Result string `json:"result"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(addEnv.Data, &addData); err != nil {
		t.Fatalf("unmarshal add data error = %v", err)
	}
	if addData.Operation.Action != "add" || addData.Operation.Result != "applied" {
		t.Fatalf("add operation = %#v, want action=add result=applied", addData.Operation)
	}
	if !containsElem(addData.Rule.Elems, "10.0.0.0/24") {
		t.Fatalf("add rule elems = %#v, want contains %q", addData.Rule.Elems, "10.0.0.0/24")
	}

	removeRecorder := performJSONRequest(router, http.MethodPost, "/api/rules/remove", `{"set":"proxy_src","ip":"10.0.0.0/24"}`)
	if removeRecorder.Code != http.StatusOK {
		t.Fatalf("remove status code = %d, want %d", removeRecorder.Code, http.StatusOK)
	}
	removeEnv := decodeContractEnvelope(t, removeRecorder)
	var removeData struct {
		Rule      RuleSetView `json:"rule"`
		Operation struct {
			Action string `json:"action"`
			Result string `json:"result"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(removeEnv.Data, &removeData); err != nil {
		t.Fatalf("unmarshal remove data error = %v", err)
	}
	if removeData.Operation.Action != "remove" || removeData.Operation.Result != "applied" {
		t.Fatalf("remove operation = %#v, want action=remove result=applied", removeData.Operation)
	}
	if containsElem(removeData.Rule.Elems, "10.0.0.0/24") {
		t.Fatalf("remove rule elems = %#v, want not contains %q", removeData.Rule.Elems, "10.0.0.0/24")
	}
}

func TestRulesSync(t *testing.T) {
	runner := newRulesStateRunner(map[string][]string{
		"proxy_src":  {"1.1.1.1"},
		"direct_src": {"2.2.2.2"},
	})
	writer := newCollectingFileWriter()
	statePath := "/tmp/state"
	config := &AppConfig{Nft: NftConfig{StatePath: statePath, Sets: []string{"proxy_src", "direct_src"}}}
	router := newRulesRouter(config, Runtime{Runner: runner, Files: writer})

	recorder := performJSONRequest(router, http.MethodPost, "/api/rules/sync", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	env := decodeContractEnvelope(t, recorder)
	var data struct {
		Synced  []string `json:"synced"`
		Results []struct {
			Rule      RuleSetView `json:"rule"`
			Operation struct {
				Action string `json:"action"`
				Result string `json:"result"`
				Output string `json:"output"`
			} `json:"operation"`
		} `json:"results"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal data error = %v", err)
	}
	if !reflect.DeepEqual(data.Synced, []string{"proxy_src", "direct_src"}) {
		t.Fatalf("synced = %#v, want %#v", data.Synced, []string{"proxy_src", "direct_src"})
	}
	if len(data.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(data.Results))
	}
	for _, result := range data.Results {
		if result.Operation.Action != "sync" || result.Operation.Result != "applied" {
			t.Fatalf("operation = %#v, want action=sync result=applied", result.Operation)
		}
		expectedOutput := filepath.Join(statePath, result.Rule.Name+".nft")
		if result.Operation.Output != expectedOutput {
			t.Fatalf("operation output = %q, want %q", result.Operation.Output, expectedOutput)
		}
		content, ok := writer.fileContent(expectedOutput)
		if !ok {
			t.Fatalf("sync output %q was not written", expectedOutput)
		}
		if !strings.Contains(content, ",") {
			t.Fatalf("sync file content = %q, want nft elements", content)
		}
	}
}

func TestRulesRejectInvalidInput(t *testing.T) {
	runner := newRulesStateRunner(map[string][]string{
		"proxy_src": {},
	})
	config := &AppConfig{Nft: NftConfig{StatePath: "/tmp/state", Sets: []string{"proxy_src"}}}
	router := newRulesRouter(config, Runtime{Runner: runner})

	testCases := []struct {
		name           string
		path           string
		body           string
		errorSubstring string
	}{
		{name: "missing ip", path: "/api/rules/add", body: `{"set":"proxy_src"}`, errorSubstring: "required"},
		{name: "invalid ip", path: "/api/rules/add", body: `{"set":"proxy_src","ip":"invalid"}`, errorSubstring: "invalid ip"},
		{name: "invalid cidr", path: "/api/rules/add", body: `{"set":"proxy_src","ip":"10.0.0.0/99"}`, errorSubstring: "invalid cidr"},
		{name: "invalid range", path: "/api/rules/add", body: `{"set":"proxy_src","ip":"10.0.0.2-10.0.0.1"}`, errorSubstring: "start must be <= end"},
		{name: "unmanaged set", path: "/api/rules/remove", body: `{"set":"unknown_set","ip":"1.1.1.1"}`, errorSubstring: "not managed"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := performJSONRequest(router, http.MethodPost, tc.path, tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			env := decodeContractEnvelope(t, recorder)
			if env.Code != APICodeInvalidRequest {
				t.Fatalf("response code = %q, want %q", env.Code, APICodeInvalidRequest)
			}

			var data map[string]string
			if err := json.Unmarshal(env.Data, &data); err != nil {
				t.Fatalf("unmarshal data error = %v", err)
			}
			if !strings.Contains(data["error"], tc.errorSubstring) {
				t.Fatalf("error = %q, want contains %q", data["error"], tc.errorSubstring)
			}
		})
	}
}

func TestRulesOpsAreSerialized(t *testing.T) {
	runner := newRulesStateRunner(map[string][]string{
		"proxy_src": {},
	})
	runner.callDelay = 20 * time.Millisecond
	config := &AppConfig{Nft: NftConfig{StatePath: "/tmp/state", Sets: []string{"proxy_src"}}}
	router := newRulesRouter(config, Runtime{Runner: runner})

	const workers = 8
	var wg sync.WaitGroup
	errCh := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			ip := fmt.Sprintf("10.0.0.%d", index+1)
			payload := fmt.Sprintf(`{"set":"proxy_src","ip":"%s"}`, ip)
			recorder := performJSONRequest(router, http.MethodPost, "/api/rules/add", payload)
			if recorder.Code != http.StatusOK {
				errCh <- fmt.Sprintf("request %d status = %d, body=%s", index, recorder.Code, recorder.Body.String())
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	if runner.MaxConcurrent() != 1 {
		t.Fatalf("max concurrent nft operations = %d, want 1", runner.MaxConcurrent())
	}
}

func containsElem(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
