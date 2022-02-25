ipset -L direct_src >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset create direct_src hash:net hashsize 64 family inet timeout 604800
fi
ipset flush direct_src
while read ip; do
  ipset add direct_src $ip
done </etc/transparent-proxy/direct_src.txt

ipset -L direct_dst >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset create direct_dst hash:net hashsize 64 family inet timeout 604800
fi
ipset flush direct_dst
while read ip; do
  ipset add direct_dst $ip
done </etc/transparent-proxy/direct_dst.txt