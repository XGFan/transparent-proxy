ip rule delete fwmark 1 table 100
ip route flush table 100

iptables -t mangle -D PREROUTING -i br-lan -j V2RAY
iptables -t mangle -D PREROUTING -m mark --mark 1 -j V2RAY
iptables -t mangle -D OUTPUT -j V2RAY_MASK 

iptables -t mangle -F V2RAY
iptables -t mangle -X V2RAY
iptables -t mangle -F V2RAY_MASK 
iptables -t mangle -X V2RAY_MASK 