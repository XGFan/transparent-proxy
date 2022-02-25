ipset -L chnroute >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset -N chnroute hash:net maxelem 65536
fi
ipset flush chnroute

while read ip;do
	ipset add chnroute $ip
done</etc/transparent-proxy/chnroute.txt