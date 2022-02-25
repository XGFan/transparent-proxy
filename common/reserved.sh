ipset -L reserved_ip >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset create reserved_ip hash:net hashsize 64 family inet timeout 604800
fi
ipset flush reserved_ip


while read ip;do
	ipset add reserved_ip $ip
done</etc/transparent-proxy/reserved_ip.txt