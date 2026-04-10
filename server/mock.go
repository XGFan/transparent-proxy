package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// MemoryNft is an in-memory mock of NftExecutor for DEV_MODE and tests.
type MemoryNft struct {
	mu           sync.Mutex
	sets         map[string]*memorySet
	proxyEnabled bool
}

type memorySet struct {
	setType  string
	elements map[string]struct{}
}

func NewMemoryNft() *MemoryNft {
	return &MemoryNft{
		sets: make(map[string]*memorySet),
	}
}

func (m *MemoryNft) Run(args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(args) == 0 {
		return nil, errors.New("no arguments")
	}

	cmd := strings.Join(args, " ")

	// nft -j list sets inet fw4
	if cmd == "-j list sets inet fw4" {
		return m.jsonListSets(), nil
	}

	// nft -j list set inet fw4 <name>
	if len(args) >= 6 && args[0] == "-j" && args[1] == "list" && args[2] == "set" {
		return m.jsonListSet(args[5])
	}

	// nft list set inet fw4 <name>
	if len(args) >= 5 && args[0] == "list" && args[1] == "set" {
		return m.textListSet(args[4])
	}

	// nft add set inet fw4 <name> { type <type> ; ... }
	if len(args) >= 5 && args[0] == "add" && args[1] == "set" {
		return m.addSet(args[4], args[5:])
	}

	// nft add element inet fw4 <name> {<data>}
	if len(args) >= 6 && args[0] == "add" && args[1] == "element" {
		return nil, m.addElement(args[4], args[5])
	}

	// nft delete element inet fw4 <name> {<data>}
	if len(args) >= 6 && args[0] == "delete" && args[1] == "element" {
		return nil, m.deleteElement(args[4], args[5])
	}

	// nft list chain inet fw4 mangle_prerouting / mangle_output
	if len(args) >= 5 && args[0] == "list" && args[1] == "chain" {
		return m.listChain(args[4])
	}

	// nft flush chain inet fw4 <chain>
	if len(args) >= 5 && args[0] == "flush" && args[1] == "chain" {
		m.proxyEnabled = false
		return nil, nil
	}

	// nft -f <file> (loading rules = enabling proxy)
	if len(args) >= 2 && args[0] == "-f" {
		m.proxyEnabled = true
		return nil, nil
	}

	return nil, fmt.Errorf("mock: unrecognized nft command: %s", cmd)
}

func (m *MemoryNft) jsonListSets() []byte {
	var items []map[string]any
	items = append(items, map[string]any{"metainfo": map[string]any{"version": "1.0"}})
	for name, s := range m.sets {
		items = append(items, map[string]any{
			"set": map[string]any{
				"name":   name,
				"type":   s.setType,
				"family": "inet",
				"table":  "fw4",
			},
		})
	}
	data, _ := json.Marshal(map[string]any{"nftables": items})
	return data
}

func (m *MemoryNft) jsonListSet(name string) ([]byte, error) {
	s, ok := m.sets[name]
	if !ok {
		return nil, fmt.Errorf("set %s does not exist", name)
	}

	var elems []any
	for elem := range s.elements {
		elems = append(elems, elem)
	}

	setObj := map[string]any{
		"name":   name,
		"type":   s.setType,
		"family": "inet",
		"table":  "fw4",
	}
	if len(elems) > 0 {
		setObj["elem"] = elems
	}

	items := []any{
		map[string]any{"metainfo": map[string]any{"version": "1.0"}},
		map[string]any{"set": setObj},
	}
	data, _ := json.Marshal(map[string]any{"nftables": items})
	return data, nil
}

func (m *MemoryNft) textListSet(name string) ([]byte, error) {
	s, ok := m.sets[name]
	if !ok {
		return nil, fmt.Errorf("set %s does not exist", name)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("table inet fw4 {\n"))
	sb.WriteString(fmt.Sprintf("\tset %s {\n", name))
	sb.WriteString(fmt.Sprintf("\t\ttype %s\n", s.setType))
	sb.WriteString("\t\tflags interval\n")
	sb.WriteString("\t\tauto-merge\n")
	if len(s.elements) > 0 {
		sb.WriteString("\t\telements = { ")
		first := true
		for elem := range s.elements {
			if !first {
				sb.WriteString(", ")
			}
			sb.WriteString(elem)
			first = false
		}
		sb.WriteString(" }\n")
	}
	sb.WriteString("\t}\n")
	sb.WriteString("}\n")
	return []byte(sb.String()), nil
}

func (m *MemoryNft) addSet(name string, rest []string) ([]byte, error) {
	setType := "ipv4_addr"
	for i, arg := range rest {
		if arg == "type" && i+1 < len(rest) {
			setType = strings.TrimSuffix(rest[i+1], ";")
		}
	}
	m.sets[name] = &memorySet{setType: setType, elements: make(map[string]struct{})}
	return nil, nil
}

func (m *MemoryNft) addElement(setName, data string) error {
	s, ok := m.sets[setName]
	if !ok {
		return fmt.Errorf("set %s does not exist", setName)
	}
	elem := strings.Trim(data, "{}")
	s.elements[strings.TrimSpace(elem)] = struct{}{}
	return nil
}

func (m *MemoryNft) deleteElement(setName, data string) error {
	s, ok := m.sets[setName]
	if !ok {
		return fmt.Errorf("set %s does not exist", setName)
	}
	elem := strings.Trim(data, "{}")
	delete(s.elements, strings.TrimSpace(elem))
	return nil
}

func (m *MemoryNft) listChain(chain string) ([]byte, error) {
	if m.proxyEnabled {
		switch chain {
		case "mangle_prerouting":
			return []byte("table inet fw4 {\n\tchain mangle_prerouting {\n\t\tjump transparent_proxy\n\t}\n}\n"), nil
		case "mangle_output":
			return []byte("table inet fw4 {\n\tchain mangle_output {\n\t\tjump transparent_proxy_mask\n\t}\n}\n"), nil
		}
	}
	return []byte(fmt.Sprintf("table inet fw4 {\n\tchain %s {\n\t}\n}\n", chain)), nil
}

// MemoryFileStore is an in-memory mock of FileStore for DEV_MODE and tests.
type MemoryFileStore struct {
	mu    sync.Mutex
	files map[string][]byte
}

func NewMemoryFileStore() *MemoryFileStore {
	return &MemoryFileStore{files: make(map[string][]byte)}
}

func (m *MemoryFileStore) WriteFile(path string, data []byte, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = append([]byte(nil), data...)
	return nil
}

func (m *MemoryFileStore) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[path]
	if !ok {
		return nil, &os.PathError{Op: "read", Path: path, Err: os.ErrNotExist}
	}
	return append([]byte(nil), data...), nil
}

func (m *MemoryFileStore) RemoveFile(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[path]; !ok {
		return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
	}
	delete(m.files, path)
	return nil
}

// GetFile returns the content of a stored file (for test assertions).
func (m *MemoryFileStore) GetFile(path string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[path]
	return data, ok
}

// MemoryFetcher is a mock RemoteFetcher.
type MemoryFetcher struct {
	Data map[string][]byte
	Err  error
}

func (f *MemoryFetcher) Fetch(url string) ([]byte, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	data, ok := f.Data[url]
	if !ok {
		return nil, fmt.Errorf("no mock data for %s", url)
	}
	return data, nil
}
