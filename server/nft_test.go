package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func newTestNft(t *testing.T) (*NftManager, *MemoryNft, *MemoryFileStore) {
	t.Helper()
	exec := NewMemoryNft()
	files := NewMemoryFileStore()
	config := BuildDefaultConfig()
	nft := NewNftManager(exec, files, config.Proxy, filepath.Join(t.TempDir(), "nft"))
	return nft, exec, files
}

func TestEnsureSetsExist_CreatesMissingSets(t *testing.T) {
	nft, exec, _ := newTestNft(t)
	sets := []string{"direct_src", "proxy_dst"}

	if err := nft.EnsureSetsExist(sets); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}

	for _, name := range sets {
		exec.mu.Lock()
		_, ok := exec.sets[name]
		exec.mu.Unlock()
		if !ok {
			t.Errorf("set %q was not created", name)
		}
	}
}

func TestEnsureSetsExist_SkipsExisting(t *testing.T) {
	nft, exec, _ := newTestNft(t)

	// Pre-create a set
	exec.mu.Lock()
	exec.sets["direct_src"] = &memorySet{setType: "ipv4_addr", elements: map[string]struct{}{}}
	exec.mu.Unlock()

	if err := nft.EnsureSetsExist([]string{"direct_src", "proxy_dst"}); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}

	exec.mu.Lock()
	if len(exec.sets) != 2 {
		t.Errorf("expected 2 sets, got %d", len(exec.sets))
	}
	exec.mu.Unlock()
}

func TestEnsureSetsExist_AllowV6MacUsesEtherAddr(t *testing.T) {
	nft, exec, _ := newTestNft(t)

	if err := nft.EnsureSetsExist([]string{"allow_v6_mac"}); err != nil {
		t.Fatalf("EnsureSetsExist: %v", err)
	}

	exec.mu.Lock()
	s, ok := exec.sets["allow_v6_mac"]
	exec.mu.Unlock()
	if !ok {
		t.Fatal("allow_v6_mac was not created")
	}
	if s.setType != "ether_addr" {
		t.Errorf("allow_v6_mac type = %q, want ether_addr", s.setType)
	}
}

func TestAddToSet_AddsElementAndPersists(t *testing.T) {
	nft, exec, files := newTestNft(t)

	// Create set first
	exec.mu.Lock()
	exec.sets["direct_dst"] = &memorySet{setType: "ipv4_addr", elements: map[string]struct{}{}}
	exec.mu.Unlock()

	if err := nft.AddToSet("direct_dst", "1.2.3.4"); err != nil {
		t.Fatalf("AddToSet: %v", err)
	}

	// Element should exist in memory
	exec.mu.Lock()
	_, elemOK := exec.sets["direct_dst"].elements["1.2.3.4"]
	exec.mu.Unlock()
	if !elemOK {
		t.Error("element 1.2.3.4 not found in set")
	}

	// File should be persisted
	path := filepath.Join(nft.statePath, "direct_dst.nft")
	data, ok := files.GetFile(path)
	if !ok {
		t.Error("set file not persisted")
	}
	if !strings.Contains(string(data), "1.2.3.4") {
		t.Errorf("persisted file does not contain element: %s", data)
	}
}

func TestRemoveFromSet_RemovesElementAndPersists(t *testing.T) {
	nft, exec, files := newTestNft(t)

	// Create set with element
	exec.mu.Lock()
	exec.sets["direct_dst"] = &memorySet{
		setType:  "ipv4_addr",
		elements: map[string]struct{}{"1.2.3.4": {}, "5.6.7.8": {}},
	}
	exec.mu.Unlock()

	if err := nft.RemoveFromSet("direct_dst", "1.2.3.4"); err != nil {
		t.Fatalf("RemoveFromSet: %v", err)
	}

	// Element should be gone
	exec.mu.Lock()
	_, elemOK := exec.sets["direct_dst"].elements["1.2.3.4"]
	_, otherOK := exec.sets["direct_dst"].elements["5.6.7.8"]
	exec.mu.Unlock()
	if elemOK {
		t.Error("element 1.2.3.4 should have been removed")
	}
	if !otherOK {
		t.Error("element 5.6.7.8 should still exist")
	}

	// File should be persisted without the removed element
	path := filepath.Join(nft.statePath, "direct_dst.nft")
	data, ok := files.GetFile(path)
	if !ok {
		t.Error("set file not persisted after remove")
	}
	if strings.Contains(string(data), "1.2.3.4") {
		t.Errorf("persisted file still contains removed element: %s", data)
	}
}

func TestGetSet_ReturnsTypeAndElements(t *testing.T) {
	nft, exec, _ := newTestNft(t)

	exec.mu.Lock()
	exec.sets["proxy_dst"] = &memorySet{
		setType:  "ipv4_addr",
		elements: map[string]struct{}{"10.0.0.1": {}},
	}
	exec.mu.Unlock()

	typ, elems, err := nft.GetSet("proxy_dst")
	if err != nil {
		t.Fatalf("GetSet: %v", err)
	}
	if typ != "ipv4_addr" {
		t.Errorf("type = %q, want ipv4_addr", typ)
	}
	if len(elems) != 1 || elems[0] != "10.0.0.1" {
		t.Errorf("elems = %v, want [10.0.0.1]", elems)
	}
}

func TestGetSet_EmptySet(t *testing.T) {
	nft, exec, _ := newTestNft(t)

	exec.mu.Lock()
	exec.sets["empty"] = &memorySet{setType: "ipv4_addr", elements: map[string]struct{}{}}
	exec.mu.Unlock()

	typ, elems, err := nft.GetSet("empty")
	if err != nil {
		t.Fatalf("GetSet: %v", err)
	}
	if typ != "ipv4_addr" {
		t.Errorf("type = %q, want ipv4_addr", typ)
	}
	if elems != nil {
		t.Errorf("elems = %v, want nil for empty set", elems)
	}
}

func TestProxyEnabled_DefaultFalse(t *testing.T) {
	nft, _, _ := newTestNft(t)

	enabled, err := nft.ProxyEnabled()
	if err != nil {
		t.Fatalf("ProxyEnabled: %v", err)
	}
	if enabled {
		t.Error("proxy should be disabled by default")
	}
}

func TestEnableDisableProxy(t *testing.T) {
	nft, _, files := newTestNft(t)

	// Enable
	if err := nft.EnableProxy(); err != nil {
		t.Fatalf("EnableProxy: %v", err)
	}
	enabled, err := nft.ProxyEnabled()
	if err != nil {
		t.Fatalf("ProxyEnabled after enable: %v", err)
	}
	if !enabled {
		t.Error("proxy should be enabled after EnableProxy")
	}

	// Persistence file should exist
	_, ok := files.GetFile(tablePostPath)
	if !ok {
		t.Error("table-post file not written after EnableProxy")
	}

	// Disable
	if err := nft.DisableProxy(); err != nil {
		t.Fatalf("DisableProxy: %v", err)
	}
	enabled, err = nft.ProxyEnabled()
	if err != nil {
		t.Fatalf("ProxyEnabled after disable: %v", err)
	}
	if enabled {
		t.Error("proxy should be disabled after DisableProxy")
	}

	// Persistence file should be removed
	_, ok = files.GetFile(tablePostPath)
	if ok {
		t.Error("table-post file should be removed after DisableProxy")
	}
}

func TestRenderAndLoadProxyRules(t *testing.T) {
	nft, _, files := newTestNft(t)

	if err := nft.RenderAndLoadProxyRules(); err != nil {
		t.Fatalf("RenderAndLoadProxyRules: %v", err)
	}

	path := filepath.Join(nft.statePath, "proxy.nft")
	data, ok := files.GetFile(path)
	if !ok {
		t.Fatal("proxy.nft not written")
	}

	content := string(data)
	if !strings.Contains(content, "transparent_proxy") {
		t.Error("proxy.nft should contain transparent_proxy chain")
	}
	if !strings.Contains(content, "transparent_proxy_mask") {
		t.Error("proxy.nft should contain transparent_proxy_mask chain")
	}
	// Check default port is rendered
	if !strings.Contains(content, "1081") {
		t.Error("proxy.nft should contain default port 1081")
	}
	if !strings.Contains(content, "1082") {
		t.Error("proxy.nft should contain forced port 1082")
	}
}

func TestParseNftSetNames(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"version":"1.0"}},{"set":{"name":"direct_src","type":"ipv4_addr","family":"inet","table":"fw4"}},{"set":{"name":"proxy_dst","type":"ipv4_addr","family":"inet","table":"fw4"}}]}`)
	names, err := parseNftSetNames(data)
	if err != nil {
		t.Fatalf("parseNftSetNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	want := map[string]bool{"direct_src": true, "proxy_dst": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected set name: %q", n)
		}
	}
}

func TestParseNftSetJSON_StringElements(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"version":"1.0"}},{"set":{"name":"test","type":"ipv4_addr","family":"inet","table":"fw4","elem":["1.2.3.4","5.6.7.8"]}}]}`)
	typ, elems, err := parseNftSetJSON(data)
	if err != nil {
		t.Fatalf("parseNftSetJSON: %v", err)
	}
	if typ != "ipv4_addr" {
		t.Errorf("type = %q, want ipv4_addr", typ)
	}
	if len(elems) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(elems))
	}
}

func TestParseNftSetJSON_Prefix(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"version":"1.0"}},{"set":{"name":"test","type":"ipv4_addr","family":"inet","table":"fw4","elem":[{"prefix":{"addr":"10.0.0.0","len":8}}]}}]}`)
	_, elems, err := parseNftSetJSON(data)
	if err != nil {
		t.Fatalf("parseNftSetJSON: %v", err)
	}
	if len(elems) != 1 || elems[0] != "10.0.0.0/8" {
		t.Errorf("elems = %v, want [10.0.0.0/8]", elems)
	}
}

func TestParseNftSetJSON_Range(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"version":"1.0"}},{"set":{"name":"test","type":"ipv4_addr","family":"inet","table":"fw4","elem":[{"range":["10.0.0.1","10.0.0.255"]}]}}]}`)
	_, elems, err := parseNftSetJSON(data)
	if err != nil {
		t.Fatalf("parseNftSetJSON: %v", err)
	}
	if len(elems) != 1 || elems[0] != "10.0.0.1-10.0.0.255" {
		t.Errorf("elems = %v, want [10.0.0.1-10.0.0.255]", elems)
	}
}

func TestParseNftSetJSON_EmptySet(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"version":"1.0"}},{"set":{"name":"test","type":"ipv4_addr","family":"inet","table":"fw4"}}]}`)
	typ, elems, err := parseNftSetJSON(data)
	if err != nil {
		t.Fatalf("parseNftSetJSON: %v", err)
	}
	if typ != "ipv4_addr" {
		t.Errorf("type = %q, want ipv4_addr", typ)
	}
	if elems != nil {
		t.Errorf("elems = %v, want nil", elems)
	}
}

func TestSyncAllSets(t *testing.T) {
	nft, exec, files := newTestNft(t)

	sets := []string{"a", "b"}
	exec.mu.Lock()
	exec.sets["a"] = &memorySet{setType: "ipv4_addr", elements: map[string]struct{}{"1.1.1.1": {}}}
	exec.sets["b"] = &memorySet{setType: "ipv4_addr", elements: map[string]struct{}{}}
	exec.mu.Unlock()

	if err := nft.SyncAllSets(sets); err != nil {
		t.Fatalf("SyncAllSets: %v", err)
	}

	for _, name := range sets {
		path := filepath.Join(nft.statePath, name+".nft")
		if _, ok := files.GetFile(path); !ok {
			t.Errorf("set file %s not persisted", path)
		}
	}
}

func TestSyncAllSets_MissingSetReturnsError(t *testing.T) {
	nft, _, _ := newTestNft(t)

	// "ghost" was never created in MemoryNft, so textListSet will return an error.
	err := nft.SyncAllSets([]string{"ghost"})
	if err == nil {
		t.Fatal("expected error for missing set, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %q, want mention of set name ghost", err.Error())
	}
}
