#!/bin/sh

RULE_PATH=/etc/transparent-proxy

create_iptables_rules() {
  echo "Creating iptables rules"
  ip rule add fwmark 1 table 100                #带有mark1的包都发到table 100
  ip route add local 0.0.0.0/0 dev lo table 100 #所有包都走lo

  # src > dst, direct > proxy
  # 代理局域网设备
  iptables -t mangle -N V2RAY
  iptables -t mangle -A V2RAY -m mark --mark 0xff -j RETURN
  iptables -t mangle -A V2RAY -m set --match-set reserved_ip dst -j RETURN
  iptables -t mangle -A V2RAY -m set --match-set direct_src src -j RETURN
  iptables -t mangle -A V2RAY -m set --match-set direct_dst dst -j RETURN
  iptables -t mangle -A V2RAY -m set --match-set proxy_src src -p tcp -j TPROXY --on-port 1082 --tproxy-mark 0x1/0x1
  iptables -t mangle -A V2RAY -m set --match-set proxy_dst dst -p tcp -j TPROXY --on-port 1082 --tproxy-mark 0x1/0x1
  iptables -t mangle -A V2RAY -m set --match-set chnroute dst -j RETURN
  iptables -t mangle -A V2RAY -p tcp -j TPROXY --on-port 1081 --tproxy-mark 0x1/0x1

  iptables -t mangle -A PREROUTING -i br-lan -j V2RAY
  iptables -t mangle -A PREROUTING -m mark --mark 0x1 -j V2RAY

  # 代理网关本机
  iptables -t mangle -N V2RAY_MASK
  iptables -t mangle -A V2RAY_MASK -m mark --mark 0xff -j RETURN
  # tproxy 转发本机流量到v2ray 1081端口，v2ray的响应的DST地址会是本机的地址，所以干脆直接放行所有lo
  iptables -t mangle -A V2RAY_MASK -o lo -j RETURN
  iptables -t mangle -A V2RAY_MASK -m set --match-set reserved_ip dst -j RETURN
  iptables -t mangle -A V2RAY_MASK -m set --match-set chnroute dst -j RETURN
  iptables -t mangle -A V2RAY_MASK -p tcp -j MARK --set-mark 0x1

  iptables -t mangle -A OUTPUT -j V2RAY_MASK
  echo "iptables rules created"
}

clear_iptables_rules() {
  echo "Clearing iptables rules"
  ip rule delete fwmark 1 table 100
  ip route flush table 100

  iptables -t mangle -D PREROUTING -i br-lan -j V2RAY
  iptables -t mangle -D PREROUTING -m mark --mark 1 -j V2RAY
  iptables -t mangle -D OUTPUT -j V2RAY_MASK

  iptables -t mangle -F V2RAY
  iptables -t mangle -X V2RAY
  iptables -t mangle -F V2RAY_MASK
  iptables -t mangle -X V2RAY_MASK
  echo "iptables rules cleared"
}

create_direct_src() {
  ipset -L direct_src >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create direct_src hash:net hashsize 64 family
  fi
  ipset flush direct_src
  while IFS= read -r ip || [ -n "$ip" ]; do
    ipset add direct_src "$ip"
  done <"$RULE_PATH/direct_src.txt"
}

create_direct_dst() {
  ipset -L direct_dst >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create direct_dst hash:net hashsize 64 family inet
  fi
  ipset flush direct_dst
  while IFS= read -r ip || [ -n "$ip" ]; do
    ipset add direct_dst "$ip"
  done <"$RULE_PATH/direct_dst.txt"
}

create_proxy_src() {
  ipset -L proxy_src >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create proxy_src hash:net hashsize 64 family inet
  fi
  ipset flush proxy_src
  while IFS= read -r ip || [ -n "$ip" ]; do
    ipset add proxy_src "$ip"
  done <"$RULE_PATH/proxy_src.txt"
}

create_proxy_dst() {
  ipset -L proxy_dst >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create proxy_dst hash:net hashsize 64 family inet
  fi
  ipset flush proxy_dst
  while IFS= read -r ip || [ -n "$ip" ]; do
    ipset add proxy_dst "$ip"
  done <"$RULE_PATH/proxy_dst.txt"
}

create_chnroute() {
  ipset -L chnroute >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset -N chnroute hash:net maxelem 65536
    #    ipset flush chnroute
    while IFS= read -r ip || [ -n "$ip" ]; do
      ipset add chnroute "$ip"
    done <"$RULE_PATH/chnroute.txt"
  fi
}

create_reserved_ip() {
  ipset -L reserved_ip >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create reserved_ip hash:net hashsize 64 family inet
    #    ipset flush reserved_ip
    while IFS= read -r ip || [ -n "$ip" ]; do
      ipset add reserved_ip "$ip"
    done <"$RULE_PATH/reserved_ip.txt"
  fi
}

update_chnroute() {
  echo "Updating chnroute..."
  wget --no-check-certificate -O- 'http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest' | awk -F\| '/CN\|ipv4/ { printf("%s/%d\n", $4, 32-log($5)/log(2)) }' >"$RULE_PATH/chnroute.txt"
  if [ $? -ne 0 ]; then
    echo "download fail"
    exit 1
  fi
  ipset -L chnroute >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset -N chnroute hash:net maxelem 65536
  fi
  ipset flush chnroute
  while IFS= read -r ip || [ -n "$ip" ]; do
    ipset add chnroute "$ip"
  done <"$RULE_PATH/chnroute.txt"
  echo "Update done"
}

create_ipset() {
  echo "create ipset"
  create_chnroute
  create_reserved_ip
  create_direct_src
  create_direct_dst
  create_proxy_src
  create_proxy_dst
  echo "ipset created"
}

if [ "$1" = "install" ]; then
  create_ipset
elif [ -n "$1" ]; then
  $1
fi
