ip rule delete fwmark 1 table 100
ip route del local 0.0.0.0/0 dev lo table 100

iptables -t mangle -D PREROUTING -j V2RAY
iptables -t mangle -D OUTPUT -j V2RAY_MASK
iptables -t mangle -D PREROUTING -p tcp -m socket -j DIVERT

iptables -t mangle -F V2RAY
iptables -t mangle -X V2RAY
iptables -t mangle -F V2RAY_MASK 
iptables -t mangle -X V2RAY_MASK 
iptables -t mangle -F DIVERT
iptables -t mangle -X DIVERT