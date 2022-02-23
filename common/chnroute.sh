mkdir -p /etc/transparent-proxy


wget --no-check-certificate -O- 'http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest'| awk -F\| '/CN\|ipv4/ { printf("%s/%d\n", $4, 32-log($5)/log(2)) }'>/etc/transparent-proxy/chnroute.txt

if [ $? -ne 0 ]; then
  echo "download fail"
  exit 1
fi

ipset -L chnroute >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset -N chnroute hash:net maxelem 65536
fi
ipset flush chnroute

while read ip;do
	ipset add chnroute $ip
done</etc/transparent-proxy/chnroute.txt