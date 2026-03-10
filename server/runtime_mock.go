package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type DevMockRunner struct {
	mu           sync.RWMutex
	sets         map[string]map[string]struct{}
	proxyEnabled bool
}

func NewDevMockRunner() *DevMockRunner {
	return &DevMockRunner{
		sets: make(map[string]map[string]struct{}),
	}
}

func (r *DevMockRunner) Run(name string, args ...string) ([]byte, error) {
	if name == "nft" {
		return r.runNft(args)
	}

	if strings.Contains(name, "init.d") && len(args) > 0 && args[0] == "restart" {
		return []byte("mock restart ok\n"), nil
	}

	return []byte("mock ok\n"), nil
}

func (r *DevMockRunner) runNft(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("nft: missing arguments")
	}

	switch args[0] {
	case "-j":
		return r.runNftJSON(args[1:])
	case "list":
		return r.runNftList(args[1:])
	case "flush":
		return r.runNftFlush(args[1:])
	case "add":
		return r.runNftAdd(args[1:])
	case "delete":
		return r.runNftDelete(args[1:])
	case "-f":
		r.mu.Lock()
		r.proxyEnabled = true
		r.mu.Unlock()
		return []byte("mock load ok\n"), nil
	default:
		return []byte("mock ok\n"), nil
	}
}

func (r *DevMockRunner) runNftJSON(args []string) ([]byte, error) {
	if len(args) < 3 || args[0] != "list" {
		return nil, fmt.Errorf("nft -j: unsupported command")
	}

	switch args[1] {
	case "sets":
		return r.runNftListSetsJSON()
	case "set":
		return r.runNftListSetJSON(args[2:])
	default:
		return nil, fmt.Errorf("nft -j: unsupported list command: %s", args[1])
	}
}

func (r *DevMockRunner) runNftListSetsJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nftables := make([]map[string]any, 0, len(r.sets))
	for setName := range r.sets {
		nftables = append(nftables, map[string]any{
			"set": map[string]any{
				"name": setName,
			},
		})
	}

	result := map[string]any{
		"nftables": nftables,
	}
	return json.Marshal(result)
}

func (r *DevMockRunner) runNftListSetJSON(args []string) ([]byte, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("nft -j list set: missing arguments")
	}
	setName := args[2]

	r.mu.RLock()
	elems := r.sets[setName]
	r.mu.RUnlock()

	if elems == nil {
		return nil, fmt.Errorf("nft: set %s not found", setName)
	}

	var elemList []string
	for elem := range elems {
		elemList = append(elemList, elem)
	}
	sort.Strings(elemList)

	if elemList == nil {
		elemList = []string{}
	}

	result := map[string]any{
		"nftables": []map[string]any{
			{
				"set": map[string]any{
					"name":  setName,
					"type":  "ipv4_addr",
					"elem":  elemList,
					"flags": []string{},
				},
			},
		},
	}

	return json.Marshal(result)
}

func (r *DevMockRunner) runNftList(args []string) ([]byte, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("nft list: missing arguments")
	}

	switch args[0] {
	case "set":
		return r.runNftListSet(args[1:])
	case "chain":
		return r.runNftListChain(args[1:])
	default:
		return nil, fmt.Errorf("nft list: unsupported command")
	}
}

func (r *DevMockRunner) runNftListSet(args []string) ([]byte, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("nft list set: missing arguments")
	}

	setName := args[2]
	r.mu.RLock()
	elems := r.sets[setName]
	r.mu.RUnlock()

	if elems == nil {
		return nil, fmt.Errorf("nft: set %s not found", setName)
	}

	if len(elems) == 0 {
		return []byte(fmt.Sprintf("table inet fw4 {\n\tset %s {\n\t\ttype ipv4_addr\n\t\telements = { }\n\t}\n}\n", setName)), nil
	}

	var elemList []string
	for elem := range elems {
		elemList = append(elemList, elem)
	}
	sort.Strings(elemList)

	elements := strings.Join(elemList, ", ")
	return []byte(fmt.Sprintf("table inet fw4 {\n\tset %s {\n\t\ttype ipv4_addr\n\t\telements = { %s }\n\t}\n}\n", setName, elements)), nil
}

func (r *DevMockRunner) runNftListChain(args []string) ([]byte, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("nft list chain: missing arguments")
	}

	chainName := args[2]
	r.mu.RLock()
	enabled := r.proxyEnabled
	r.mu.RUnlock()

	if enabled {
		switch chainName {
		case "mangle_prerouting":
			return []byte("chain mangle_prerouting {\n\tiifname \"br-lan\" jump transparent_proxy\n}\n"), nil
		case "mangle_output":
			return []byte("chain mangle_output {\n\tjump transparent_proxy_mask\n}\n"), nil
		}
	}

	return []byte(fmt.Sprintf("chain %s {\n}\n", chainName)), nil
}

func (r *DevMockRunner) runNftFlush(args []string) ([]byte, error) {
	if len(args) < 4 || args[0] != "chain" {
		return nil, fmt.Errorf("nft flush: unsupported command")
	}

	chainName := args[3]

	if chainName == "mangle_prerouting" || chainName == "mangle_output" {
		r.mu.Lock()
		r.proxyEnabled = false
		r.mu.Unlock()
	}

	return []byte("ok\n"), nil
}

func (r *DevMockRunner) runNftAdd(args []string) ([]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("nft add: missing arguments")
	}

	switch args[0] {
	case "set":
		return r.runNftAddSet(args[1:])
	case "element":
		return r.runNftAddElement(args[1:])
	default:
		return nil, fmt.Errorf("nft add: unsupported command: %s", args[0])
	}
}

func (r *DevMockRunner) runNftAddSet(args []string) ([]byte, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("nft add set: missing arguments")
	}

	var setName string
	if len(args) >= 3 && args[0] == "inet" && args[1] == "fw4" {
		setName = args[2]
	} else {
		setName = args[0]
	}

	if setName == "" {
		return nil, fmt.Errorf("nft add set: missing set name")
	}

	r.mu.Lock()
	if r.sets[setName] == nil {
		r.sets[setName] = make(map[string]struct{})
	}
	r.mu.Unlock()

	return []byte("ok\n"), nil
}

func (r *DevMockRunner) runNftAddElement(args []string) ([]byte, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("nft add element: missing arguments")
	}

	setName := args[2]
	elem := strings.Trim(args[3], "{} ")

	r.mu.Lock()
	if r.sets[setName] == nil {
		r.sets[setName] = make(map[string]struct{})
	}
	r.sets[setName][elem] = struct{}{}
	r.mu.Unlock()

	return []byte("ok\n"), nil
}

func (r *DevMockRunner) runNftDelete(args []string) ([]byte, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("nft delete: missing arguments")
	}

	switch args[0] {
	case "element":
		return r.runNftDeleteElement(args[1:])
	default:
		return nil, fmt.Errorf("nft delete: unsupported command: %s", args[0])
	}
}

func (r *DevMockRunner) runNftDeleteElement(args []string) ([]byte, error) {
	if len(args) < 4 {
		return nil, fmt.Errorf("nft delete element: missing arguments")
	}

	setName := args[2]
	elem := strings.Trim(args[3], "{} ")

	r.mu.Lock()
	if r.sets[setName] != nil {
		delete(r.sets[setName], elem)
	}
	r.mu.Unlock()

	return []byte("ok\n"), nil
}
