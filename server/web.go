package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/XGFan/go-utils"
	"github.com/XGFan/netguard"
	"github.com/gonetx/ipset"
	"github.com/spyzhov/ajson"
	"gopkg.in/yaml.v3"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

//go:embed web
var assetData embed.FS

const BasePath = "/etc/transparent-proxy"

func getSetJson(setName string) []string {
	command := exec.Command("nft", "-j", "list", "set", "inet", "fw4", setName)
	output, err := command.CombinedOutput()
	if err != nil {
		log.Printf("exec [%v] fail: %v", command, err)
	}
	result := make([]string, 0)
	jpath := "$.nftables[?(@.set!=null)].set.elem"
	elem, err := ajson.JSONPath(output, jpath)
	if err != nil {
		log.Printf("read json path [%s] fail, json: %s", jpath, output)
		return result
	}
	if len(elem) >= 1 {
		value, err := elem[0].GetArray()
		if err != nil {
			log.Printf("read json path [%s] fail, json: %s", jpath, output)
			return result
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
					log.Printf("can not reconize %s", n)
				}
			} else {
				log.Printf("can not reconize %s", n)
			}
		}
	}
	return result
}

func addToSet(setName, data string) error {
	command := exec.Command("nft", "add", "element", "inet", "fw4", setName, fmt.Sprintf("{%s}", data))
	output, err := command.CombinedOutput()
	log.Printf("exec [%s] result: %s", command, output)
	return err
}

func removeFromSet(setName, data string) error {
	command := exec.Command("nft", "delete", "element", "inet", "fw4", setName, fmt.Sprintf("{%s}", data))
	output, err := command.CombinedOutput()
	log.Printf("exec [%s] result: %s", command, output)
	return err
}

func currentIP(req *http.Request) string {
	ip := req.URL.Query().Get("ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(req.RemoteAddr)
	}
	return ip
}

func handleError(res http.ResponseWriter, e error) {
	if e != nil {
		res.WriteHeader(500)
		res.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(res).Encode(e.Error())
	}
}

type IpAndSet struct {
	IP  string `json:"ip"`
	Set string `json:"set"`
}

func (ipAndSet *IpAndSet) isValid() bool {
	return ipAndSet != nil && ipAndSet.Set != "" && ipAndSet.IP != ""
}

func getCHNRoute() ([]string, error) {
	//  wget --no-check-certificate -O- 'http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest'
	// | awk -F\| '/CN\|ipv4/ { printf("%s/%d\n", $4, 32-log($5)/log(2)) }'
	resp, err := http.Get("http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest")
	if err != nil {
		return nil, fmt.Errorf("fetch data fail: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("response status code is: %w", err)
	}
	all, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read response data fail: %w", err)
	}
	lines := strings.Split(string(all), "\n")
	ipRanges := make([]string, 0)
	for _, line := range lines {
		if line != "" && !strings.HasPrefix(line, "#") {
			parts := strings.Split(line, "|")
			if len(parts) >= 6 && parts[1] == "CN" && parts[2] == "ipv4" {
				float, err := strconv.ParseFloat(parts[4], 64)
				if err != nil {
					return nil, fmt.Errorf("parse line %s fail: %w", line, err)
				}
				mask := 32 - int8(math.Log2(float))
				ipRanges = append(ipRanges, fmt.Sprintf("%s/%d", parts[3], mask))
			}
		}
	}
	return ipRanges, nil
}

func refreshCHNRoute() error {
	_, err := ipset.New("chnroute",
		ipset.HashNet,
		ipset.HashSize(2048),
		ipset.Family(ipset.Inet),
		ipset.MaxElem(65536),
		ipset.Exist(true))
	if err != nil {
		return fmt.Errorf("create or read ipset [chnroute] fail: %w", err)
	}
	chnrouteForUpdate, err := ipset.New("chnroute-for-update",
		ipset.HashNet,
		ipset.HashSize(2048),
		ipset.Family(ipset.Inet),
		ipset.MaxElem(65536),
		ipset.Exist(true))
	if err != nil {
		return fmt.Errorf("create or read ipset [chnroute-for-update] fail: %w", err)
	}
	routes, err := getCHNRoute()
	if err != nil {
		return err
	}
	err = chnrouteForUpdate.Flush()
	if err != nil {
		return fmt.Errorf("flush ipset [chnroute-for-update] fail: %w", err)
	}
	for _, route := range routes {
		err := chnrouteForUpdate.Add(route, ipset.Exist(true))
		if err != nil {
			return fmt.Errorf("add %s to ipset [chnroute-for-update] fail: %w", route, err)
		}
	}
	err = ipset.Swap("chnroute-for-update", "chnroute")
	if err != nil {
		return fmt.Errorf("swap ipsets fail: %w", err)
	}
	err = os.WriteFile(fmt.Sprintf("%s/%s", BasePath, "chnroute.txt"), []byte(strings.Join(routes, "\n")), 0644)
	if err != nil {
		return fmt.Errorf("save route to chnroute.txt: %w", err)
	}
	return nil
}

type FsAdapter struct {
	forward embed.FS
}

func (f FsAdapter) Open(name string) (fs.File, error) {
	if name == "." {
		return f.forward.Open("web")
	}
	return f.forward.Open("web/" + name)
}

func main() {
	configFile := flag.String("c", "config.yaml", "config location")
	flag.Parse()
	open, err := utils.LocateAndRead(*configFile)
	if err != nil {
		log.Printf("[exit]read config error: %s", err)
		return
	}
	checkerConf := new(netguard.CheckerConf)
	err = yaml.Unmarshal(open, checkerConf)
	if err != nil {
		log.Printf("[exit]parse config error: %s", err)
		return
	}
	checker := netguard.AssembleChecker(*checkerConf)
	go checker.Check(context.Background())
	serverFs := http.FileServer(http.FS(FsAdapter{assetData}))
	http.Handle("/", serverFs)
	http.HandleFunc("/api/status", func(res http.ResponseWriter, req *http.Request) {
		directSrcList := getSetJson("direct_src")
		directDstList := getSetJson("direct_dst")
		proxySrcList := getSetJson("proxy_src")
		proxyDstList := getSetJson("proxy_dst")
		res.Header().Set("Content-Type", "application/json")
		ip := currentIP(req)
		_ = json.NewEncoder(res).Encode(map[string]interface{}{
			"status": checker.Status(),
			"ip":     ip,
			"sets": []map[string]interface{}{
				{
					"name": "direct_src",
					"ip":   directSrcList,
				},
				{
					"name": "direct_dst",
					"ip":   directDstList,
				},
				{
					"name": "proxy_src",
					"ip":   proxySrcList,
				},
				{
					"name": "proxy_dst",
					"ip":   proxyDstList,
				},
			},
		})
	})
	http.HandleFunc("/api/refresh-route", func(writer http.ResponseWriter, request *http.Request) {
		err := refreshCHNRoute()
		if err != nil {
			handleError(writer, err)
		} else {
			_, _ = writer.Write([]byte("ok"))
		}
	})
	http.HandleFunc("/api/remove", func(writer http.ResponseWriter, req *http.Request) {
		ipAndSet := new(IpAndSet)
		err := json.NewDecoder(req.Body).Decode(ipAndSet)
		if err == nil {
			if ipAndSet.isValid() {
				err = removeFromSet(ipAndSet.Set, ipAndSet.IP)
			}
		}
		handleError(writer, err)
	})

	http.HandleFunc("/api/add", func(writer http.ResponseWriter, req *http.Request) {
		ipAndSet := new(IpAndSet)
		err := json.NewDecoder(req.Body).Decode(ipAndSet)
		if err == nil {
			if ipAndSet.isValid() {
				err = addToSet(ipAndSet.Set, ipAndSet.IP)
			}
		}
		handleError(writer, err)
	})

	http.HandleFunc("/api/ip", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(currentIP(request)))
	})

	log.Fatalln(http.ListenAndServe(":1444", nil))
}
