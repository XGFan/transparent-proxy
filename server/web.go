package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/XGFan/go-utils"
	"github.com/XGFan/netguard"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/spyzhov/ajson"
	"gopkg.in/yaml.v3"
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
)

//go:embed web
var assetData embed.FS

//go:embed set.tmpl
var setTmpl string

const BasePath = "/etc/nftables.d"
const V2ConfPath = "/etc/v2ray/v2ray.v5.json"

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

func restartV2() error {
	command := exec.Command("/etc/init.d/v2ray", "restart")
	output, err := command.CombinedOutput()
	log.Printf("exec [%s] result: %s", command, output)
	return err
}
func join(sep string, s []string) string {
	return strings.Join(s, sep)
}

type NftSet struct {
	Name     string
	Attrs    []string
	Elements []string
}

func main() {
	tmpl, err2 := template.New("set").Funcs(template.FuncMap{"join": join}).Parse(setTmpl)
	if err2 != nil {
		log.Fatalln(err2)
	}
	configFile := flag.String("c", "config.yaml", "config location")
	flag.Parse()
	open, err := utils.LocateAndRead(*configFile)
	var checker netguard.Checker
	if err == nil {
		checkerConf := new(netguard.CheckerConf)
		err = yaml.Unmarshal(open, checkerConf)
		if err == nil {
			checker = netguard.AssembleChecker(*checkerConf)
			go checker.Check(context.Background())
		} else {
			log.Printf("parse guard config error: %s", err)
		}
	} else {
		log.Printf("start without guard: %s", err)
	}

	r := gin.Default()
	r.Use(static.Serve("/", static.EmbedFolder(assetData, "web")))

	r.GET("/api/status", func(c *gin.Context) {
		directSrcList := getSetJson("direct_src")
		directDstList := getSetJson("direct_dst")
		proxySrcList := getSetJson("proxy_src")
		proxyDstList := getSetJson("proxy_dst")
		ip := currentIP(c.Request)
		c.JSON(200, map[string]interface{}{
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
		sets := []string{"direct_src", "direct_dst", "proxy_src", "proxy_dst"}
		for _, setName := range sets {
			file, err := os.OpenFile(path.Join(BasePath, fmt.Sprintf("%s.nft", setName)), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				c.Error(err)
				return
			}
			err = tmpl.Execute(file, NftSet{
				Name:     setName,
				Elements: getSetJson(setName),
			})
			_ = file.Close()
			if err != nil {
				c.Error(err)
				return
			}
		}

	})

	r.GET("/api/ip", func(c *gin.Context) {
		c.JSON(200, currentIP(c.Request))
	})

	r.GET("/api/v2-conf", func(c *gin.Context) {
		file, err := os.ReadFile(V2ConfPath)
		utils.PanicIfErr(err)
		root, err := ajson.Unmarshal(file)
		utils.PanicIfErr(err)
		result, err := root.JSONPath("$.outbounds[0]")
		utils.PanicIfErr(err)
		c.Data(200, "application/json", result[0].Source())
	})

	r.POST("/api/v2-conf", func(c *gin.Context) {
		data, err := c.GetRawData()
		utils.PanicIfErr(err)
		file, err := os.ReadFile(V2ConfPath)
		utils.PanicIfErr(err)
		root, err := ajson.Unmarshal(file)
		utils.PanicIfErr(err)
		result, err := root.JSONPath("$.outbounds[0]")
		utils.PanicIfErr(err)
		unmarshal, err := ajson.Unmarshal(data)
		err = result[0].SetObject(unmarshal.MustObject())
		utils.PanicIfErr(err)
		marshal, err := ajson.Marshal(root)
		utils.PanicIfErr(err)
		err = os.WriteFile(V2ConfPath, marshal, 0666)
		utils.PanicIfErr(err)
		err = restartV2()
		utils.PanicIfErr(err)
	})

	err = r.Run(":1444")
	utils.PanicIfErr(err)
}
