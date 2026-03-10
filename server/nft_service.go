package main

import (
	"fmt"
	"log"
	"path"
	"strings"
	"sync"

	"github.com/spyzhov/ajson"
)

type NftService struct {
	runtime     Runtime
	operationMu *sync.Mutex
}

var nftResourceOperationMu sync.Mutex

func defaultNftResourceLock() *sync.Mutex {
	return &nftResourceOperationMu
}

func NewNftService(runtime Runtime, operationMu ...*sync.Mutex) *NftService {
	lock := defaultNftResourceLock()
	if len(operationMu) > 0 && operationMu[0] != nil {
		lock = operationMu[0]
	}
	return &NftService{runtime: runtime, operationMu: lock}
}

func (service *NftService) withSerializedOp(fn func() error) error {
	lock := defaultNftResourceLock()
	if service != nil && service.operationMu != nil {
		lock = service.operationMu
	}
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (service *NftService) GetSetJSON(setName string) (string, []string, error) {
	var (
		typ   string
		elems []string
	)
	err := service.withSerializedOp(func() error {
		var opErr error
		typ, elems, opErr = getSetJsonWithRuntime(service.runtime, setName)
		return opErr
	})
	return typ, elems, err
}

func (service *NftService) AddToSet(setName, data string) error {
	return service.withSerializedOp(func() error {
		return addToSetWithRuntime(service.runtime, setName, data)
	})
}

func (service *NftService) RemoveFromSet(setName, data string) error {
	return service.withSerializedOp(func() error {
		return removeFromSetWithRuntime(service.runtime, setName, data)
	})
}

func (service *NftService) SyncSet(basePath, setName string) error {
	return service.withSerializedOp(func() error {
		return syncSetWithRuntime(service.runtime, basePath, setName)
	})
}

func (service *NftService) ProxyEnabledFromNft() (bool, error) {
	enabled := false
	err := service.withSerializedOp(func() error {
		var opErr error
		enabled, opErr = proxyEnabledWithRuntime(service.runtime)
		return opErr
	})
	return enabled, err
}

func proxyEnabledWithRuntime(runtime Runtime) (bool, error) {
	preroutingOutput, err := runtime.nft("list", "chain", "inet", "fw4", "mangle_prerouting")
	if err != nil {
		return false, fmt.Errorf("read nft chain mangle_prerouting fail: %w", err)
	}
	outputOutput, err := runtime.nft("list", "chain", "inet", "fw4", "mangle_output")
	if err != nil {
		return false, fmt.Errorf("read nft chain mangle_output fail: %w", err)
	}

	preroutingEnabled := strings.Contains(string(preroutingOutput), "jump transparent_proxy")
	outputEnabled := strings.Contains(string(outputOutput), "jump transparent_proxy_mask")

	return preroutingEnabled || outputEnabled, nil
}

func syncSet(basePath, setName string) error {
	return syncSetWithRuntime(appRuntime, basePath, setName)
}

func syncSetWithRuntime(runtime Runtime, basePath, setName string) error {
	output, err := runtime.nft("list", "set", "inet", "fw4", setName)
	if err != nil {
		return err
	}
	lines := strings.Split(string(output), "\n")
	if len(lines) >= 2 {
		lines = lines[1 : len(lines)-2]
	}
	lines = append(lines, "")
	for i := range lines {
		lines[i] = strings.Replace(lines[i], "\t", "", 1)
	}
	content := strings.Join(lines, "\n")
	targetPath := path.Join(basePath, fmt.Sprintf("%s.nft", setName))
	if err := runtime.writeFile(targetPath, []byte(content), 0664); err != nil {
		return fmt.Errorf("sync nft set %s to %s fail: %w", setName, targetPath, err)
	}
	return nil
}

func getSetJson(setName string) (string, []string, error) {
	return getSetJsonWithRuntime(appRuntime, setName)
}

func getSetJsonWithRuntime(runtime Runtime, setName string) (string, []string, error) {
	output, err := runtime.nft("-j", "list", "set", "inet", "fw4", setName)
	if err != nil {
		return "", nil, err
	}
	result := make([]string, 0)
	var types string
	typePath := "$.nftables[?(@.set!=null)].set.type"
	jsonPath, err := ajson.JSONPath(output, typePath)
	if err != nil {
		return types, nil, err
	}
	if len(jsonPath) == 0 {
		return types, nil, fmt.Errorf("read json path [%s] fail, json: %s", typePath, output)
	}
	types = jsonPath[0].MustString()
	jpath := "$.nftables[?(@.set!=null)].set.elem"
	elem, err := ajson.JSONPath(output, jpath)
	if err != nil {
		return types, result, fmt.Errorf("read json path [%s] fail, json: %s", jpath, output)
	}
	if len(elem) >= 1 {
		value, err := elem[0].GetArray()
		if err != nil {
			return types, result, fmt.Errorf("read json path [%s] fail, json: %s", jpath, output)
		}
		for _, n := range value {
			if n.IsString() {
				result = append(result, n.MustString())
			} else if n.IsObject() {
				if n.HasKey("prefix") {
					pn := n.MustKey("prefix")
					ip := fmt.Sprintf("%s/%d",
						pn.MustKey("addr").MustString(),
						int(pn.MustKey("len").MustNumeric()))
					result = append(result, ip)
				} else if n.HasKey("range") {
					ips := n.MustKey("range").MustArray()
					ip := fmt.Sprintf("%s-%s",
						ips[0].MustString(),
						ips[1].MustString())
					result = append(result, ip)
				} else {
					return types, result, fmt.Errorf("can not reconize %s in %s", n, output)
				}
			} else {
				return types, result, fmt.Errorf("can not reconize %s in %s", n, output)
			}
		}
	}
	return types, result, nil
}

func addToSet(setName, data string) error {
	return addToSetWithRuntime(appRuntime, setName, data)
}

func addToSetWithRuntime(runtime Runtime, setName, data string) error {
	output, err := runtime.nft("add", "element", "inet", "fw4", setName, fmt.Sprintf("{%s}", data))
	log.Printf("exec [nft add element inet fw4 %s {%s}] result: %s", setName, data, output)
	return err
}

func removeFromSet(setName, data string) error {
	return removeFromSetWithRuntime(appRuntime, setName, data)
}

func removeFromSetWithRuntime(runtime Runtime, setName, data string) error {
	output, err := runtime.nft("delete", "element", "inet", "fw4", setName, fmt.Sprintf("{%s}", data))
	log.Printf("exec [nft delete element inet fw4 %s {%s}] result: %s", setName, data, output)
	return err
}

// EnsureSetsExist ensures all configured nft sets exist in the fw4 table.
// If a set does not exist, it creates an empty set with the appropriate type.
func (service *NftService) EnsureSetsExist(setNames []string) error {
	return service.withSerializedOp(func() error {
		return ensureSetsExistWithRuntime(service.runtime, setNames)
	})
}

func ensureSetsExistWithRuntime(runtime Runtime, setNames []string) error {
	existingSets, err := listSetsWithRuntime(runtime)
	if err != nil {
		return fmt.Errorf("list existing sets fail: %w", err)
	}

	existingMap := make(map[string]bool, len(existingSets))
	for _, name := range existingSets {
		existingMap[name] = true
	}

	for _, setName := range setNames {
		if existingMap[setName] {
			log.Printf("nft set inet fw4 %s already exists, skip creation", setName)
			continue
		}

		log.Printf("nft set inet fw4 %s does not exist, creating...", setName)
		if err := createSetWithRuntime(runtime, setName); err != nil {
			return fmt.Errorf("create set %s fail: %w", setName, err)
		}
		log.Printf("nft set inet fw4 %s created successfully", setName)
	}
	return nil
}

func listSetsWithRuntime(runtime Runtime) ([]string, error) {
	output, err := runtime.nft("-j", "list", "sets", "inet", "fw4")
	if err != nil {
		return nil, fmt.Errorf("list sets fail: %w", err)
	}

	sets := make([]string, 0)
	namePath := "$.nftables[*].set.name"
	names, err := ajson.JSONPath(output, namePath)
	if err != nil {
		return sets, nil
	}

	for _, name := range names {
		if name.IsString() {
			sets = append(sets, name.MustString())
		}
	}
	return sets, nil
}

func createSetWithRuntime(runtime Runtime, setName string) error {
	// Create an empty set with ipv4_addr type and interval flag for CIDR support
	// This matches the set definitions that were previously in the .nft files
	_, err := runtime.nft("add", "set", "inet", "fw4", setName,
		"{", "type", "ipv4_addr", ";", "flags", "interval", ";", "auto-merge", ";", "}")
	return err
}
