#!/bin/sh
target="/usr/share/nftables.d/table-post/transparent.nft"
nft flush chain inet fw4 mangle_prerouting
nft flush chain inet fw4 mangle_output
[ -e "$target" ] && rm "$target"
