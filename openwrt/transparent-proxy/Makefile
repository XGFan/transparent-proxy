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
 Additive OpenWrt service package for transparent-proxy.
endef

define Build/Compile
endef

define Package/transparent-proxy/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/transparent-proxy $(1)/usr/bin/transparent-proxy
endef

define Package/transparent-proxy/conffiles
endef

define Package/transparent-proxy/postinst
#!/bin/sh
set -eu

[ -n "$${IPKG_INSTROOT:-}" ] && exit 0

source_bin="/usr/bin/transparent-proxy"
guest_root="/etc/transparent-proxy"
guest_server="$${guest_root}/server"
guest_config="$${guest_root}/config.yaml"
guest_initd="/etc/init.d/transparent-proxy"
expected_service_command="/etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml"

install_file() {
	src="$$1"
	dst="$$2"
	mode="$$3"
	dir="$${dst%/*}"
	base="$${dst##*/}"
	tmp="$${dir}/.$${base}.opkg.$$"

	test -x "$${src}"
	mkdir -p "$${dir}"
	rm -f "$${tmp}"
	cp "$${src}" "$${tmp}"
	chmod "$${mode}" "$${tmp}"
	mv "$${tmp}" "$${dst}"
}

run_bootstrap_once() {
	bootstrap_log="/tmp/transparent-proxy-bootstrap.log"
	rm -f "$${bootstrap_log}"

	"$${guest_server}" -c "$${guest_config}" >"$${bootstrap_log}" 2>&1 &
	bootstrap_pid="$$!"

	ready=0
	tries=0
	while [ "$${tries}" -lt 30 ]; do
		tries=$$((tries + 1))

		if [ -f "$${guest_config}" ] && [ -f "$${guest_initd}" ]; then
			ready=1
			break
		fi

		if ! kill -0 "$${bootstrap_pid}" >/dev/null 2>&1; then
			wait "$${bootstrap_pid}" || true
			if [ -f "$${guest_config}" ] && [ -f "$${guest_initd}" ]; then
				ready=1
			fi
			break
		fi

		sleep 1
	done

	if kill -0 "$${bootstrap_pid}" >/dev/null 2>&1; then
		kill "$${bootstrap_pid}" >/dev/null 2>&1 || true
		wait "$${bootstrap_pid}" >/dev/null 2>&1 || true
	fi

	if [ "$${ready}" -ne 1 ]; then
		printf 'bootstrap 失败：未在时限内生成 canonical config/init 脚本\n' >&2
		if [ -f "$${bootstrap_log}" ]; then
			cat "$${bootstrap_log}" >&2
		fi
		return 1
	fi
}

assert_bootstrap_outputs() {
	test -x "$${guest_server}"
	test -f "$${guest_config}"
	test -f "$${guest_initd}"
	grep -F -- "$${expected_service_command}" "$${guest_initd}" >/dev/null

	for path in \
		/etc/hotplug.d/iface/80-ifup-wan \
		/etc/nftables.d/direct_dst.nft \
		/etc/nftables.d/direct_src.nft \
		/etc/nftables.d/proxy.nft \
		/etc/nftables.d/proxy_dst.nft \
		/etc/nftables.d/proxy_src.nft \
		/etc/nftables.d/reserved_ip.nft \
		/etc/nftables.d/v6block.nft \
		/etc/transparent-proxy/disable.sh \
		/etc/transparent-proxy/enable.sh \
		/etc/transparent-proxy/transparent.nft \
		/etc/transparent-proxy/transparent_full.nft
	do
		test -f "$${path}"
	done
}

ensure_service_ready() {
	"$${guest_initd}" enable >/dev/null 2>&1 || {
		printf 'postinst 失败：无法启用服务 %s\n' "$${guest_initd}" >&2
		return 1
	}

	if ! "$${guest_initd}" restart >/dev/null 2>&1; then
		"$${guest_initd}" start >/dev/null 2>&1 || {
			printf 'postinst 失败：无法启动服务 %s\n' "$${guest_initd}" >&2
			return 1
		}
	fi

	"$${guest_initd}" enabled >/dev/null 2>&1 || {
		printf 'postinst 失败：服务未处于 enabled 状态 %s\n' "$${guest_initd}" >&2
		return 1
	}

	"$${guest_initd}" running >/dev/null 2>&1 || {
		printf 'postinst 失败：服务未处于 running 状态 %s\n' "$${guest_initd}" >&2
		return 1
	}
}

install_file "$${source_bin}" "$${guest_server}" 755
run_bootstrap_once
assert_bootstrap_outputs
ensure_service_ready

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

		for path in \
			/etc/init.d/transparent-proxy \
			/etc/hotplug.d/iface/80-ifup-wan \
			/etc/nftables.d/direct_dst.nft \
			/etc/nftables.d/direct_src.nft \
			/etc/nftables.d/proxy.nft \
			/etc/nftables.d/proxy_dst.nft \
			/etc/nftables.d/proxy_src.nft \
			/etc/nftables.d/reserved_ip.nft \
			/etc/nftables.d/v6block.nft \
			/etc/transparent-proxy/server \
			/etc/transparent-proxy/disable.sh \
			/etc/transparent-proxy/enable.sh \
			/etc/transparent-proxy/transparent.nft \
			/etc/transparent-proxy/transparent_full.nft \
			/etc/transparent-proxy/state/managed-records.json
		do
			rm -f "$${path}"
		done

		rmdir /etc/transparent-proxy/state >/dev/null 2>&1 || true
		if [ ! -e /etc/transparent-proxy/config.yaml ]; then
			rmdir /etc/transparent-proxy >/dev/null 2>&1 || true
		fi
		;;
esac

exit 0
endef

$(eval $(call BuildPackage,transparent-proxy))
