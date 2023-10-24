#!/bin/sh
full="/etc/transparent-proxy/transparent_full.nft"
partial="/etc/transparent-proxy/transparent.nft"
target="/usr/share/nftables.d/table-post/transparent.nft"
nft flush chain inet fw4 mangle_prerouting
nft flush chain inet fw4 mangle_output
nft -f "$full"
[ -e "$target" ] && rm "$target"
cp "$partial" "$target"

