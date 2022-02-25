create_direct_src() {
  ipset -L direct_src >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create direct_src hash:net hashsize 64 family inet timeout 604800
  fi
  ipset flush direct_src
  while read ip; do
    ipset add direct_src $ip
  done </etc/transparent-proxy/direct_src.txt
}

create_direct_dst() {
  ipset -L direct_dst >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create direct_dst hash:net hashsize 64 family inet timeout 604800
  fi
  ipset flush direct_dst
  while read ip; do
    ipset add direct_dst $ip
  done </etc/transparent-proxy/direct_dst.txt
}

create_chnroute() {
  ipset -L chnroute >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset -N chnroute hash:net maxelem 65536
    #    ipset flush chnroute
    while read ip; do
      ipset add chnroute $ip
    done </etc/transparent-proxy/chnroute.txt
  fi
}
create_reserved_ip() {
  ipset -L reserved_ip >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    ipset create reserved_ip hash:net hashsize 64 family inet timeout 604800
    #    ipset flush reserved_ip
    while read ip; do
      ipset add reserved_ip $ip
    done </etc/transparent-proxy/reserved_ip.txt
  fi
}
create_ipset() {
  create_chnroute
  create_reserved_ip
  create_direct_src
  create_direct_dst
}