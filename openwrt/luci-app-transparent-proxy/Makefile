include $(TOPDIR)/rules.mk

TP_PACKAGE_VERSION:=$(if $(TP_PACKAGE_VERSION),$(TP_PACKAGE_VERSION),0.0.0)
TP_PACKAGE_RELEASE:=$(if $(TP_PACKAGE_RELEASE),$(TP_PACKAGE_RELEASE),1)

PKG_NAME:=luci-app-transparent-proxy
PKG_VERSION:=$(TP_PACKAGE_VERSION)
PKG_RELEASE:=$(TP_PACKAGE_RELEASE)

PKG_LICENSE:=MIT
PKG_MAINTAINER:=transparent-proxy project

PKGARCH:=all

include $(INCLUDE_DIR)/package.mk

define Package/luci-app-transparent-proxy
  SECTION:=luci
  CATEGORY:=LuCI
  SUBMENU:=3. Applications
  TITLE:=Transparent Proxy LuCI shell entry
  PKGARCH:=$(PKGARCH)
  DEPENDS:=+luci-base +rpcd
endef

define Package/luci-app-transparent-proxy/description
 Lightweight LuCI shell package for transparent-proxy entry.
endef

define Build/Compile
endef

define Package/luci-app-transparent-proxy/install
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/transparent-proxy
	$(INSTALL_DATA) ./htdocs/luci-static/resources/view/transparent-proxy/index.js $(1)/www/luci-static/resources/view/transparent-proxy/index.js
	$(INSTALL_DIR) $(1)/usr/share/luci/menu.d
	$(INSTALL_DATA) ./root/usr/share/luci/menu.d/transparent-proxy.json $(1)/usr/share/luci/menu.d/transparent-proxy.json
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_DATA) ./root/usr/share/rpcd/acl.d/luci-app-transparent-proxy.json $(1)/usr/share/rpcd/acl.d/luci-app-transparent-proxy.json
endef

define Package/luci-app-transparent-proxy/postinst
#!/bin/sh

[ -n "$${IPKG_INSTROOT:-}" ] && exit 0

rm -f /tmp/luci-indexcache.*
rm -rf /tmp/luci-modulecache/
/etc/init.d/rpcd reload >/dev/null 2>&1 || true

exit 0
endef

define Package/luci-app-transparent-proxy/postrm
#!/bin/sh

[ -n "$${IPKG_INSTROOT:-}" ] && exit 0

rm -f /tmp/luci-indexcache.*
rm -rf /tmp/luci-modulecache/
/etc/init.d/rpcd reload >/dev/null 2>&1 || true

exit 0
endef

$(eval $(call BuildPackage,luci-app-transparent-proxy))
