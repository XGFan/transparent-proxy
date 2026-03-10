package main

import (
	"fmt"
	"net/netip"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

type IpAndSet struct {
	IP  string `json:"ip"`
	Set string `json:"set"`
}

type RuleSetView struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Elems []string `json:"elems"`
}

func (ipAndSet *IpAndSet) validate(config *AppConfig) error {
	if ipAndSet == nil {
		return fmt.Errorf("ip and set are required")
	}
	ipAndSet.Set = strings.TrimSpace(ipAndSet.Set)
	ipAndSet.IP = strings.TrimSpace(ipAndSet.IP)
	if ipAndSet.Set == "" || ipAndSet.IP == "" {
		return fmt.Errorf("ip and set are required")
	}
	if !isConfiguredSet(config, ipAndSet.Set) {
		return fmt.Errorf("set %q is not managed", ipAndSet.Set)
	}
	normalized, err := normalizeRuleElement(ipAndSet.IP)
	if err != nil {
		return err
	}
	ipAndSet.IP = normalized
	return nil
}

func isConfiguredSet(config *AppConfig, setName string) bool {
	if config == nil {
		return false
	}
	for _, managedSet := range config.Nft.Sets {
		if managedSet == setName {
			return true
		}
	}
	return false
}

func normalizeRuleElement(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("ip and set are required")
	}

	if strings.Contains(value, "-") {
		parts := strings.SplitN(value, "-", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid ip/range format: %q", raw)
		}
		start, err := netip.ParseAddr(strings.TrimSpace(parts[0]))
		if err != nil {
			return "", fmt.Errorf("invalid ip range start: %q", raw)
		}
		end, err := netip.ParseAddr(strings.TrimSpace(parts[1]))
		if err != nil {
			return "", fmt.Errorf("invalid ip range end: %q", raw)
		}
		if start.BitLen() != end.BitLen() {
			return "", fmt.Errorf("ip range families must match: %q", raw)
		}
		if start.Compare(end) > 0 {
			return "", fmt.Errorf("ip range start must be <= end: %q", raw)
		}
		return start.String() + "-" + end.String(), nil
	}

	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", fmt.Errorf("invalid cidr: %q", raw)
		}
		return prefix.String(), nil
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", fmt.Errorf("invalid ip: %q", raw)
	}
	return addr.String(), nil
}

func (server apiServer) managedRuleSet(setName string) (RuleSetView, error) {
	typ, elems, err := server.nftService().GetSetJSON(setName)
	if err != nil {
		return RuleSetView{}, err
	}
	return RuleSetView{Name: setName, Type: typ, Elems: elems}, nil
}

func (server apiServer) handleRules(c *gin.Context) {
	config := server.effectiveConfig()
	rules := make([]gin.H, 0, len(config.Nft.Sets))
	for _, setName := range config.Nft.Sets {
		ruleSet, err := server.managedRuleSet(setName)
		if err != nil {
			rules = append(rules, gin.H{
				"name":  setName,
				"type":  "",
				"elems": []string{},
				"error": err.Error(),
			})
			continue
		}
		rules = append(rules, gin.H{
			"name":  ruleSet.Name,
			"type":  ruleSet.Type,
			"elems": ruleSet.Elems,
		})
	}
	apiOK(c, gin.H{
		"sets":  config.Nft.Sets,
		"rules": rules,
	})
}

func (server apiServer) handleRuleAdd(c *gin.Context) {
	request := new(IpAndSet)
	if err := decodeJSONBodyStrict(c.Request, request); err != nil {
		apiInvalidRequest(c, "invalid rules payload", gin.H{"error": err.Error()})
		return
	}
	if err := request.validate(server.effectiveConfig()); err != nil {
		apiInvalidRequest(c, "invalid rules payload", gin.H{"error": err.Error()})
		return
	}
	if err := server.nftService().AddToSet(request.Set, request.IP); err != nil {
		apiInternalError(c, "add rule failed", err)
		return
	}
	ruleSet, err := server.managedRuleSet(request.Set)
	if err != nil {
		apiInternalError(c, "read nft set failed", err)
		return
	}
	apiOK(c, gin.H{
		"set":  request.Set,
		"ip":   request.IP,
		"rule": ruleSet,
		"operation": gin.H{
			"action": "add",
			"result": "applied",
		},
	})
}

func (server apiServer) handleRuleRemove(c *gin.Context) {
	request := new(IpAndSet)
	if err := decodeJSONBodyStrict(c.Request, request); err != nil {
		apiInvalidRequest(c, "invalid rules payload", gin.H{"error": err.Error()})
		return
	}
	if err := request.validate(server.effectiveConfig()); err != nil {
		apiInvalidRequest(c, "invalid rules payload", gin.H{"error": err.Error()})
		return
	}
	if err := server.nftService().RemoveFromSet(request.Set, request.IP); err != nil {
		apiInternalError(c, "remove rule failed", err)
		return
	}
	ruleSet, err := server.managedRuleSet(request.Set)
	if err != nil {
		apiInternalError(c, "read nft set failed", err)
		return
	}
	apiOK(c, gin.H{
		"set":  request.Set,
		"ip":   request.IP,
		"rule": ruleSet,
		"operation": gin.H{
			"action": "remove",
			"result": "applied",
		},
	})
}

func (server apiServer) handleRuleSync(c *gin.Context) {
	config := server.effectiveConfig()
	results := make([]gin.H, 0, len(config.Nft.Sets))
	for _, setName := range config.Nft.Sets {
		if err := server.nftService().SyncSet(config.Nft.StatePath, setName); err != nil {
			apiInternalError(c, "sync rules failed", err)
			return
		}
		ruleSet, err := server.managedRuleSet(setName)
		if err != nil {
			apiInternalError(c, "read nft set failed", err)
			return
		}
		results = append(results, gin.H{
			"rule": ruleSet,
			"operation": gin.H{
				"action": "sync",
				"result": "applied",
				"output": path.Join(config.Nft.StatePath, setName+".nft"),
			},
		})
	}
	apiOK(c, gin.H{"synced": config.Nft.Sets, "results": results})
}
