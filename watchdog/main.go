//go:build linux && amd64

package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gonetx/ipset"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"time"
)

var ops IpSetOps

//go:embed  web
var assetData embed.FS

const BasePath = "/etc/transparent-proxy"

func init() {
	if err := ipset.Check(); err != nil {
		panic(err)
	}
	ops = IpSetOps{
		directSrc: createIpSetIfNotExist("direct_src"),
		directDst: createIpSetIfNotExist("direct_dst"),
		proxySrc:  createIpSetIfNotExist("proxy_src"),
		proxyDst:  createIpSetIfNotExist("proxy_dst"),
	}
}

type IpSetOps struct {
	directSrc, directDst, proxySrc, proxyDst *HashSet
}

type HashSet struct {
	file string
	ipset.IPSet
}

func (set *HashSet) RemoveAndFlush(key string) error {
	exist, err := set.Test(key)
	if exist {
		if e := set.Del(key); e == nil {
			return set.save()
		} else {
			return e
		}
	}
	return err
}

func (set *HashSet) AddAndFlush(key string) error {
	exist, err := set.Test(key)
	if !exist {
		if e := set.Add(key); e == nil {
			return set.save()
		} else {
			return e
		}
	}
	return err
}

func (set *HashSet) save() error {
	info, err := set.List()
	if err != nil {
		return err
	}
	return os.WriteFile(set.file, []byte(strings.Join(info.Entries, "\n")), 0644)
}

func createIpSetIfNotExist(name string) *HashSet {
	set, err := ipset.New(name,
		ipset.HashNet,
		ipset.HashSize(64),
		ipset.Family(ipset.Inet),
		ipset.MaxElem(65536),
		ipset.Exist(true))
	if err != nil {
		log.Fatalln(err)
	}
	initFile := fmt.Sprintf("%s/%s.txt", BasePath, name)
	if _, err = os.Stat(initFile); err != nil {
		if os.IsNotExist(err) {
			create, _ := os.Create(initFile)
			defer create.Close()
		} else {
			log.Fatalln(err)
		}
	}
	return &HashSet{initFile, set}
}

func (ops IpSetOps) status(res http.ResponseWriter, req *http.Request) {
	directSrcList, _ := ops.directSrc.List()
	directDstList, _ := ops.directDst.List()
	proxySrcList, _ := ops.proxySrc.List()
	proxyDstList, _ := ops.proxyDst.List()
	res.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(res).Encode(map[string]interface{}{
		"ip":   currentIP(req),
		"sets": []*ipset.Info{directSrcList, directDstList, proxySrcList, proxyDstList},
	})
}

func (ops IpSetOps) auto(res http.ResponseWriter, req *http.Request) {
	ip := currentIP(req)
	if e := ops.directSrc.RemoveAndFlush(ip); e != nil {
		handleError(res, e)
		return
	}
	if e := ops.proxySrc.RemoveAndFlush(ip); e != nil {
		handleError(res, e)
		return
	}
	_, _ = res.Write([]byte("ok"))
}

func (ops IpSetOps) test(res http.ResponseWriter, req *http.Request) {
	ip := currentIP(req)
	re, err := ops.directDst.Test(ip)
	res.Header().Add("content-type", "plain/text")
	if err == nil && re {
		res.Write([]byte("direct"))
		return
	}
	re, err = ops.proxyDst.Test(ip)
	if err == nil && re {
		res.Header().Add("content-type", "plain/text")
		res.Write([]byte("proxy"))
		return
	}
	chnroute, err := ipset.New("chnroute",
		ipset.HashNet,
		ipset.HashSize(2048),
		ipset.Family(ipset.Inet),
		ipset.MaxElem(65536),
		ipset.Exist(true))
	re, err = chnroute.Test(ip)
	if err == nil && re {
		res.Write([]byte("direct"))
		return
	}
	_, _ = res.Write([]byte("unknown"))
}

func (ops IpSetOps) proxy(res http.ResponseWriter, req *http.Request) {
	ip := currentIP(req)
	if e := ops.directSrc.RemoveAndFlush(ip); e != nil {
		handleError(res, e)
		return
	}
	if e := ops.proxySrc.AddAndFlush(ip); e != nil {
		handleError(res, e)
		return
	}
	_, _ = res.Write([]byte("ok"))
}

func (ops IpSetOps) direct(res http.ResponseWriter, req *http.Request) {
	ip := currentIP(req)
	if e := ops.directSrc.AddAndFlush(ip); e != nil {
		handleError(res, e)
		return
	}
	if e := ops.proxySrc.RemoveAndFlush(ip); e != nil {
		handleError(res, e)
		return
	}
	_, _ = res.Write([]byte("ok"))
}

func (ops IpSetOps) findIpsetByName(name string) (*HashSet, error) {
	switch name {
	case "direct_src":
		return ops.directSrc, nil
	case "direct_dst":
		return ops.directDst, nil
	case "proxy_src":
		return ops.proxySrc, nil
	case "proxy_dst":
		return ops.proxyDst, nil
	default:
		return nil, errors.New("unknown ipset " + name)
	}
}

func currentIP(req *http.Request) string {
	ip := req.URL.Query().Get("ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(req.RemoteAddr)
	}
	return ip
}

func handleError(res http.ResponseWriter, e error) {
	res.WriteHeader(500)
	res.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(res).Encode(e.Error())
	return
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
	dnsProxy := httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "dns-switchy"
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.DialTimeout("unix", "/var/run/dns-switchy.sock", time.Second*2)
			},
		},
	}
	routeProxy := httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "v2ray"
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.DialTimeout("unix", "/var/run/v2ray.sock", time.Second*2)
			},
		},
	}
	serverFs := http.FileServer(http.FS(FsAdapter{assetData}))
	http.Handle("/", serverFs)
	http.HandleFunc("/api/dns/", func(writer http.ResponseWriter, request *http.Request) {
		dnsProxy.ServeHTTP(writer, request)
	})
	http.HandleFunc("/api/route/", func(writer http.ResponseWriter, request *http.Request) {
		routeProxy.ServeHTTP(writer, request)
	})
	http.HandleFunc("/api/status", ops.status)
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
		if err != nil {
			handleError(writer, err)
			return
		}
		if ipAndSet.isValid() {
			set, err := ops.findIpsetByName(ipAndSet.Set)
			if err == nil {
				err = set.RemoveAndFlush(ipAndSet.IP)
			}
		}
		if err != nil {
			handleError(writer, err)
		}
	})
	http.HandleFunc("/api/add", func(writer http.ResponseWriter, req *http.Request) {
		ipAndSet := new(IpAndSet)
		err := json.NewDecoder(req.Body).Decode(ipAndSet)
		if err != nil {
			handleError(writer, err)
			return
		}
		if ipAndSet.isValid() {
			set, err := ops.findIpsetByName(ipAndSet.Set)
			if err == nil {
				err = set.AddAndFlush(ipAndSet.IP)
			}
		}
		if err != nil {
			handleError(writer, err)
		}
	})
	http.HandleFunc("/api/ip", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(currentIP(request)))
	})
	http.HandleFunc("/api/auto", ops.auto)
	http.HandleFunc("/api/proxy", ops.proxy)
	http.HandleFunc("/api/direct", ops.direct)
	http.HandleFunc("/api/test", ops.test)
	log.Fatalln(http.ListenAndServe(":1444", nil))
}
