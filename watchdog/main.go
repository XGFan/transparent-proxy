//go:build linux && amd64

package main

import (
	"encoding/json"
	"fmt"
	"github.com/gonetx/ipset"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

var ops IpSetOps

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

func (set *HashSet) remove(key string) error {
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

func (set *HashSet) add(key string) error {
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
	initFile := fmt.Sprintf("/etc/transparent-proxy/%s.txt", name)
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
	if e := ops.directSrc.remove(ip); e != nil {
		handleError(res, e)
		return
	}
	if e := ops.proxySrc.remove(ip); e != nil {
		handleError(res, e)
		return
	}
	_, _ = res.Write([]byte("ok"))
}

func (ops IpSetOps) proxy(res http.ResponseWriter, req *http.Request) {
	ip := currentIP(req)
	if e := ops.directSrc.remove(ip); e != nil {
		handleError(res, e)
		return
	}
	if e := ops.proxySrc.add(ip); e != nil {
		handleError(res, e)
		return
	}
	_, _ = res.Write([]byte("ok"))
}

func (ops IpSetOps) direct(res http.ResponseWriter, req *http.Request) {
	ip := currentIP(req)
	if e := ops.directSrc.add(ip); e != nil {
		handleError(res, e)
		return
	}
	if e := ops.proxySrc.remove(ip); e != nil {
		handleError(res, e)
		return
	}
	_, _ = res.Write([]byte("ok"))
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

func main() {
	http.HandleFunc("/", ops.status)
	http.HandleFunc("/ip", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(currentIP(request)))
	})
	http.HandleFunc("/auto", ops.auto)
	http.HandleFunc("/proxy", ops.proxy)
	http.HandleFunc("/direct", ops.direct)
	log.Fatalln(http.ListenAndServe(":1333", nil))
}
