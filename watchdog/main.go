//go:build linux && amd64

package main

import (
	"fmt"
	"github.com/gonetx/ipset"
	"github.com/labstack/echo/v4"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

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

func getHashSet(name string) *HashSet {
	set, err := ipset.New(name,
		ipset.HashNet,
		ipset.HashSize(64),
		ipset.Family(ipset.Inet),
		ipset.MaxElem(65536),
		ipset.Exist(true))
	if err != nil {
		log.Fatalln(err)
	}
	return &HashSet{fmt.Sprintf("/etc/transparent-proxy/%s.txt", name), set}
}

func init() {
	if err := ipset.Check(); err != nil {
		panic(err)
	}
}

type IpSetOps struct {
	directSrc, directDst, proxySrc *HashSet
}

func (ops IpSetOps) status(c echo.Context) error {
	//reserved_ip_list, _ := reserved_ip.List()
	directSrcList, _ := ops.directSrc.List()
	directDstList, _ := ops.directDst.List()
	proxySrcList, _ := ops.proxySrc.List()
	return c.JSON(http.StatusOK, []*ipset.Info{directSrcList, directDstList, proxySrcList})
}

func (ops IpSetOps) auto(c echo.Context) error {
	ip := currentIP(c)
	if e := ops.directSrc.remove(ip); e != nil {
		return c.JSON(http.StatusInternalServerError, e.Error())
	}
	if e := ops.proxySrc.remove(ip); e != nil {
		return c.JSON(http.StatusInternalServerError, e.Error())
	}
	return c.String(http.StatusOK, "ok")
}

func (ops IpSetOps) proxy(c echo.Context) error {
	ip := currentIP(c)
	if e := ops.directSrc.remove(ip); e != nil {
		return c.JSON(http.StatusInternalServerError, e.Error())
	}
	if e := ops.proxySrc.add(ip); e != nil {
		return c.JSON(http.StatusInternalServerError, e.Error())
	}
	return c.String(http.StatusOK, "ok")
}

func (ops IpSetOps) direct(c echo.Context) error {
	ip := currentIP(c)
	if e := ops.directSrc.add(ip); e != nil {
		return c.JSON(http.StatusInternalServerError, e.Error())
	}
	if e := ops.proxySrc.remove(ip); e != nil {
		return c.JSON(http.StatusInternalServerError, e.Error())
	}
	return c.String(http.StatusOK, "ok")
}

func currentIP(c echo.Context) string {
	ip := c.QueryParam("ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(c.Request().RemoteAddr)
	}
	return ip
}

func main() {
	ops := IpSetOps{
		directSrc: getHashSet("direct_src"),
		directDst: getHashSet("direct_dst"),
		proxySrc:  getHashSet("proxy_src"),
	}
	e := echo.New()
	e.Any("/", ops.status)
	e.Any("/ip", func(context echo.Context) error {
		return context.String(http.StatusOK, currentIP(context))
	})
	e.Any("/auto", ops.auto)
	e.Any("/proxy", ops.proxy)
	e.Any("/direct", ops.direct)

	e.Logger.Fatal(e.Start(":1323"))
}
