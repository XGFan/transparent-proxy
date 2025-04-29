package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"text/template"

	"github.com/XGFan/go-utils"
	"github.com/XGFan/netguard"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/spyzhov/ajson"
	"gopkg.in/yaml.v3"
)

//go:embed web
var assetData embed.FS

//go:embed set.tmpl
var setTmpl string

const BasePath = "/etc/nftables.d"

func syncSet(setName string) error {
	command := exec.Command("nft", "list", "set", "inet", "fw4", setName)
	output, err := command.CombinedOutput()
	if err != nil {
		log.Printf("exec [%v] fail: %v", command, err)
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
	file, err := os.OpenFile(path.Join(BasePath, fmt.Sprintf("%s.nft", setName)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0664)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

func getSetJson(setName string) (string, []string, error) {
	command := exec.Command("nft", "-j", "list", "set", "inet", "fw4", setName)
	output, err := command.CombinedOutput()
	if err != nil {
		log.Printf("exec [%v] fail: %v", command, err)
	}
	result := make([]string, 0)
	var types string
	typePath := "$.nftables[?(@.set!=null)].set.type"
	jsonPath, err := ajson.JSONPath(output, typePath)
	types = jsonPath[0].MustString()
	if err != nil {
		return types, nil, err
	}
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

func join(sep string, s []string) string {
	return strings.Join(s, sep)
}

type NftSet struct {
	Name     string
	Attrs    []string
	Elements []string
}

type TPConfig struct {
	Checker netguard.CheckerConf `yaml:"checker,omitempty"`
	Nft     NftConfig            `yaml:"nft,omitempty"`
}

type NftConfig struct {
	Sets []string `yaml:"sets,omitempty"`
}

func main() {
	tmpl, err := template.New("set").Funcs(template.FuncMap{"join": join}).Parse(setTmpl)
	utils.PanicIfErr(err)
	configFile := flag.String("c", "config.yaml", "config location")
	flag.Parse()
	open, err := utils.LocateAndRead(*configFile)
	utils.PanicIfErr(err)
	config := new(TPConfig)
	err = yaml.Unmarshal(open, config)
	utils.PanicIfErr(err)
	checker := netguard.AssembleChecker(config.Checker)
	go checker.Check(context.Background())
	r := gin.Default()
	r.Use(static.Serve("/", static.EmbedFolder(assetData, "web")))

	r.GET("/api/status", func(c *gin.Context) {
		ip := currentIP(c.Request)
		sets := make([]map[string]interface{}, 0)
		for _, setName := range config.Nft.Sets {
			typ, elems, err := getSetJson(setName)
			if err != nil {
				c.Error(err)
				return
			}
			sets = append(sets,
				map[string]interface{}{
					"name":  setName,
					"type":  typ,
					"elems": elems,
				})
		}
		c.JSON(200, map[string]interface{}{
			"status": checker.Status(),
			"ip":     ip,
			"sets":   sets,
		})
	})

	r.POST("/api/refresh-route", func(c *gin.Context) {
		ips, err := getCHNRoute()
		utils.PanicIfErr(err)
		file, err := os.OpenFile(path.Join(BasePath, fmt.Sprintf("chnroute.nft")), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0664)
		if err != nil {
			c.Error(err)
			return
		}
		err = tmpl.Execute(file, NftSet{
			Name:     "chnroute",
			Elements: ips,
		})
		_ = file.Close()
		c.JSON(200, "ok")
	})

	r.POST("/api/remove", func(c *gin.Context) {
		ipAndSet := new(IpAndSet)
		err := json.NewDecoder(c.Request.Body).Decode(ipAndSet)
		utils.PanicIfErr(err)
		if ipAndSet.isValid() {
			err = removeFromSet(ipAndSet.Set, ipAndSet.IP)
			utils.PanicIfErr(err)
		}
	})

	r.POST("/api/add", func(c *gin.Context) {
		ipAndSet := new(IpAndSet)
		err := json.NewDecoder(c.Request.Body).Decode(ipAndSet)
		utils.PanicIfErr(err)
		if ipAndSet.isValid() {
			err = addToSet(ipAndSet.Set, ipAndSet.IP)
			utils.PanicIfErr(err)
		}
	})

	r.POST("/api/sync", func(c *gin.Context) {
		for _, setName := range config.Nft.Sets {
			err = syncSet(setName)
			if err != nil {
				c.Error(err)
				return
			}
		}
	})

	r.GET("/api/ip", func(c *gin.Context) {
		c.JSON(200, currentIP(c.Request))
	})

	err = r.Run(":1444")
	utils.PanicIfErr(err)
}
