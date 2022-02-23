ip rule add fwmark 1 table 100 
ip route add local 0.0.0.0/0 dev lo table 100

# 代理局域网设备
iptables -t mangle -N V2RAY
# 目标地址是本地
iptables -t mangle -A V2RAY -d 127.0.0.1/32 -j RETURN
# 目标地址是多播段
iptables -t mangle -A V2RAY -d 224.0.0.0/4 -j RETURN 
# 目标地址是广播
iptables -t mangle -A V2RAY -d 255.255.255.255/32 -j RETURN 
# 访问局域网
iptables -t mangle -A V2RAY -d 192.168.2.0/24 -j RETURN 
# 国内路由
iptables -t mangle -A V2RAY -j MARK --set-mark 0 -m set --match-set chnroute dst
# SO_MARK 为255也直接链接
iptables -t mangle -A V2RAY -j RETURN -m mark --mark 0xff
# 给 TCP 打标记 1，转发至 1081 端口
iptables -t mangle -A V2RAY -p tcp -j TPROXY --on-ip 127.0.0.1 --on-port 1081 --tproxy-mark 1
# 把v2ray加到 PREROUTING[mangle]
iptables -t mangle -A PREROUTING -j V2RAY

# 代理网关本机
iptables -t mangle -N V2RAY_MASK 
iptables -t mangle -A V2RAY_MASK -d 224.0.0.0/4 -j RETURN 
iptables -t mangle -A V2RAY_MASK -d 255.255.255.255/32 -j RETURN 
iptables -t mangle -A V2RAY_MASK -d 192.168.2.0/24 -j RETURN 
# 国内路由
iptables -t mangle -A V2RAY_MASK -j RETURN -m set --match-set chnroute dst
# 直连 SO_MARK 为 0xff 的流量(0xff 是 16 进制数，数值上等同与上面V2Ray 配置的 255)，此规则目的是避免代理本机(网关)流量出现回环问题
iptables -t mangle -A V2RAY_MASK -j RETURN -m mark --mark 0xff  
# 给 TCP 打标记，重路由
iptables -t mangle -A V2RAY_MASK -p tcp -j MARK --set-mark 1 
# 应用规则
iptables -t mangle -A OUTPUT -j V2RAY_MASK

# 新建 DIVERT 规则，避免已有连接的包二次通过 TPROXY，理论上有一定的性能提升
iptables -t mangle -N DIVERT
iptables -t mangle -A DIVERT -j MARK --set-mark 1
iptables -t mangle -A DIVERT -j ACCEPT
iptables -t mangle -I PREROUTING -p tcp -m socket -j DIVERT