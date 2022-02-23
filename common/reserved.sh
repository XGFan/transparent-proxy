mkdir -p /etc/transparent-proxy

echo "0.0.0.0/8" > /etc/transparent-proxy/reserved_ip.txt
echo "10.0.0.0/8" >> /etc/transparent-proxy/reserved_ip.txt
echo "100.64.0.0/10" >> /etc/transparent-proxy/reserved_ip.txt
echo "127.0.0.0/8" >> /etc/transparent-proxy/reserved_ip.txt
echo "169.254.0.0/16" >> /etc/transparent-proxy/reserved_ip.txt
echo "172.16.0.0/12" >> /etc/transparent-proxy/reserved_ip.txt
echo "192.0.0.0/24" >> /etc/transparent-proxy/reserved_ip.txt
echo "192.0.2.0/24" >> /etc/transparent-proxy/reserved_ip.txt
echo "192.88.99.0/24" >> /etc/transparent-proxy/reserved_ip.txt
echo "192.168.0.0/16" >> /etc/transparent-proxy/reserved_ip.txt
echo "198.18.0.0/15" >> /etc/transparent-proxy/reserved_ip.txt
echo "198.51.100.0/24" >> /etc/transparent-proxy/reserved_ip.txt
echo "203.0.113.0/24" >> /etc/transparent-proxy/reserved_ip.txt
echo "224.0.0.0/4" >> /etc/transparent-proxy/reserved_ip.txt
echo "240.0.0.0/4" >> /etc/transparent-proxy/reserved_ip.txt
echo "255.255.255.255" >> /etc/transparent-proxy/reserved_ip.txt


ipset -L reserved_ip >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset create reserved_ip hash:net hashsize 64 family inet timeout 604800
fi
ipset flush reserved_ip


while read ip;do
	ipset add reserved_ip $ip
done</etc/transparent-proxy/reserved_ip.txt