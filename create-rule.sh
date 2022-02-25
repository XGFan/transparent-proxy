ip rule add fwmark 1 table 100 #带有mark1的包都发到table 100
ip route add local 0.0.0.0/0 dev lo table 100 #所有包都走lo

# 代理局域网设备
iptables -t mangle -N V2RAY
iptables -t mangle -A V2RAY -m mark --mark 0xff -j RETURN 
iptables -t mangle -A V2RAY -m set --match-set direct_src src -j RETURN 
iptables -t mangle -A V2RAY -m set --match-set direct_dst dst -j RETURN 
iptables -t mangle -A V2RAY -m set --match-set reserved_ip dst -j RETURN 
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

