#!/bin/sh
partial="/usr/share/nftables.d/table-post/transparent.nft"
full="/usr/share/nftables.d/table-post/transparent.nft"
nft flush chain inet fw4 mangle_prerouting
nft flush chain inet fw4 mangle_output
[ -e "$partial" ] && rm "$partial"
