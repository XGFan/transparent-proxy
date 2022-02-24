ipset -L direct_src >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset create direct_src hash:net hashsize 64 family inet timeout 604800
fi

ipset -L direct_dst >/dev/null 2>&1
if [ $? -ne 0 ]; then
  ipset create direct_dst hash:net hashsize 64 family inet timeout 604800
fi