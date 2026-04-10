include $(TOPDIR)/rules.mk

TP_PACKAGE_VERSION:=$(if $(TP_PACKAGE_VERSION),$(TP_PACKAGE_VERSION),0.0.0)
TP_PACKAGE_RELEASE:=$(if $(TP_PACKAGE_RELEASE),$(TP_PACKAGE_RELEASE),1)
TP_PACKAGE_ARCH:=$(if $(TP_PACKAGE_ARCH),$(TP_PACKAGE_ARCH),aarch64_generic)

PKG_NAME:=transparent-proxy
PKG_VERSION:=$(TP_PACKAGE_VERSION)
PKG_RELEASE:=$(TP_PACKAGE_RELEASE)

PKG_LICENSE:=MIT
PKG_MAINTAINER:=transparent-proxy project

include $(INCLUDE_DIR)/package.mk

define Package/transparent-proxy
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Transparent Proxy service
  PKGARCH:=$(TP_PACKAGE_ARCH)
endef

define Package/transparent-proxy/description
 nftables + tproxy transparent proxy management for OpenWrt.
endef

define Build/Compile
endef

define Package/transparent-proxy/install
	# Binary
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/transparent-proxy $(1)/usr/bin/transparent-proxy

	# Init script
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ../../files/etc/init.d/transparent-proxy $(1)/etc/init.d/transparent-proxy

	# Hotplug script
	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) ../../files/etc/hotplug.d/iface/80-ifup-wan $(1)/etc/hotplug.d/iface/80-ifup-wan

	# Static nft rules
	$(INSTALL_DIR) $(1)/etc/nftables.d
	$(INSTALL_DATA) ../../files/etc/nftables.d/reserved_ip.nft $(1)/etc/nftables.d/reserved_ip.nft
	$(INSTALL_DATA) ../../files/etc/nftables.d/v6block.nft $(1)/etc/nftables.d/v6block.nft

	# Default config (conffile, preserved on upgrade)
	$(INSTALL_DIR) $(1)/etc/transparent-proxy
	$(INSTALL_CONF) ../../files/etc/transparent-proxy/config.yaml $(1)/etc/transparent-proxy/config.yaml
endef

define Package/transparent-proxy/conffiles
/etc/transparent-proxy/config.yaml
endef

define Package/transparent-proxy/postinst
#!/bin/sh
set -eu

[ -n "$${IPKG_INSTROOT:-}" ] && exit 0

guest_initd="/etc/init.d/transparent-proxy"

if [ -x "$${guest_initd}" ]; then
  "$${guest_initd}" enable >/dev/null 2>&1 || true
  "$${guest_initd}" restart >/dev/null 2>&1 || "$${guest_initd}" start >/dev/null 2>&1 || true
fi

exit 0
endef

define Package/transparent-proxy/prerm
#!/bin/sh
set -eu

[ -n "$${IPKG_INSTROOT:-}" ] && exit 0

action="$${1:-}"
guest_initd="/etc/init.d/transparent-proxy"

if [ -x "$${guest_initd}" ]; then
  "$${guest_initd}" stop >/dev/null 2>&1 || true

  case "$${action}" in
    remove|purge|deconfigure)
      "$${guest_initd}" disable >/dev/null 2>&1 || true
      ;;
  esac
fi

exit 0
endef

define Package/transparent-proxy/postrm
#!/bin/sh
set -eu

[ -n "$${IPKG_INSTROOT:-}" ] && exit 0

action="$${1:-}"

case "$${action}" in
  remove|purge|deconfigure)
    rm -f /var/run/transparent-proxy.pid

    # Clean up runtime-generated files (not installed by IPK)
    for path in \
      /etc/nftables.d/proxy.nft \
      /etc/nftables.d/proxy_src.nft \
      /etc/nftables.d/proxy_dst.nft \
      /etc/nftables.d/direct_src.nft \
      /etc/nftables.d/direct_dst.nft \
      /etc/nftables.d/allow_v6_mac.nft \
      /etc/nftables.d/chnroute.nft \
      /usr/share/nftables.d/table-post/transparent.nft
    do
      rm -f "$${path}"
    done

    if [ ! -e /etc/transparent-proxy/config.yaml ]; then
      rmdir /etc/transparent-proxy >/dev/null 2>&1 || true
    fi
    ;;
esac

exit 0
endef

$(eval $(call BuildPackage,transparent-proxy))
