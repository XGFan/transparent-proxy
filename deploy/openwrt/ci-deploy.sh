#!/bin/sh
# 在路由器上执行的部署脚本。由 CI scp 到 /tmp/tp-deploy/ 后运行。
#
# 为什么是脚本而不是内联在 .woodpecker.yaml 里的一行 ssh 命令：
# 那条一行命令要同时穿过 CI 容器的 sh 和路由器的 busybox ash 两层引号，
# 实测会以两种方式崩掉 —— 一次是 opkg 段错误，一次是
# `/bin/sh: syntax error: unterminated quoted string`。放进脚本就没有转义问题，
# 也让"服务真的起来了没有"这种判断有地方写清楚。
set -e

IPK_DIR="${1:-/tmp/tp-deploy}"

fail() {
    echo "部署失败: $1" >&2
    exit 1
}

svc_ipk=$(ls "$IPK_DIR"/transparent-proxy_*.ipk 2>/dev/null | head -n1)
luci_ipk=$(ls "$IPK_DIR"/luci-app-transparent-proxy_*.ipk 2>/dev/null | head -n1)

[ -n "$svc_ipk" ] || fail "找不到 transparent-proxy ipk"
[ -n "$luci_ipk" ] || fail "找不到 luci-app-transparent-proxy ipk"

# 逐个装、只取一个文件名：通配符一次喂多个包会让 opkg 段错误(实测)。
# --force-downgrade 是必须的 —— 版本号是 0.0.0-<sha8>，sha 之间没有大小可言，
# 不加它 opkg 会以"已是最新"为由拒装并**返回 0**，部署静默落空。
# 刻意不用 --force-reinstall：它对本地 ipk 会先卸载、再按包名去软件源找，
# 而这个包不在任何 src 里，结果是卸载成功、安装失败、服务被删掉。
echo "安装 $svc_ipk"
opkg install --force-downgrade "$svc_ipk" || fail "opkg 安装 transparent-proxy 失败"
echo "安装 $luci_ipk"
opkg install --force-downgrade "$luci_ipk" || fail "opkg 安装 luci-app 失败"

/etc/init.d/transparent-proxy restart || fail "服务重启失败"

# 启动后要先拉 chnroute 才开始监听，固定 sleep 会稳定误报，这里轮询等。
# 探针用 /panel.js 而不是 /api/status：panel.js 是前端产物、不入库、只由 CI
# 现场构建再 go:embed 进二进制，它可达才同时证明"包换了"和"前端打进去了"。
i=0
while [ "$i" -lt 30 ]; do
    if pidof transparent-proxy >/dev/null 2>&1 &&
        wget -q -O /dev/null -T 5 "http://127.0.0.1:1444/panel.js"; then
        echo "部署校验通过: 服务在跑，/panel.js 可达"
        exit 0
    fi
    i=$((i + 1))
    sleep 3
done

fail "90s 内服务未就绪或 /panel.js 不可达"
