#!/bin/sh
# 在路由器上执行的部署脚本。由 CI scp 到 /tmp/tp-deploy/ 后运行。
#
# 为什么是脚本而不是内联在 .woodpecker.yaml 里的一行 ssh 命令：
# 那条一行命令要同时穿过 CI 容器的 sh 和路由器的 busybox ash 两层引号，
# 实测会以两种方式崩掉 —— 一次是 opkg 段错误，一次是
# `/bin/sh: syntax error: unterminated quoted string`。放进脚本就没有转义问题，
# 也让"服务真的起来了没有"这种判断有地方写清楚。
#
# 只装服务 ipk，**不装 luci-app**（net-console ADR-0005：LuCI 入口退役，统一入口
# 是控制台；luci-app 仍被 CI 构建以防烂，需要菜单入口时手动 opkg install 产物）。
# 失败自动装回上一个验证通过的 ipk（lastgood，与 exit-pin/dns-switchy 同骨架）。
set -eu

IPK_DIR="${1:-/tmp/tp-deploy}"
CFG=/etc/transparent-proxy/config.yaml
LASTGOOD_DIR=/mnt/ext/ipk-lastgood
LASTGOOD="$LASTGOOD_DIR/transparent-proxy.ipk"

fail() {
    echo "部署失败: $1" >&2
    if [ "${HAVE_PREV:-0}" = 1 ]; then
        echo "装回上一个验证通过的版本…" >&2
        opkg install --force-downgrade "$LASTGOOD" >/dev/null 2>&1 || true
        /etc/init.d/transparent-proxy restart || true
        sleep 3
        pidof transparent-proxy >/dev/null 2>&1 \
            && echo "已回滚,上一版在跑" >&2 \
            || echo "!! 回滚后旧版本也没起来,需要人工介入" >&2
    else
        echo "!! 没有可回滚的版本(首次 lastgood 部署)" >&2
    fi
    exit 1
}

svc_ipk=$(ls "$IPK_DIR"/transparent-proxy_*.ipk 2>/dev/null | head -n1)
[ -n "$svc_ipk" ] || { echo "部署失败: 找不到 transparent-proxy ipk" >&2; exit 1; }

# lastgood 在 /mnt/ext 上;它没挂载时装上去的回滚目标会写进只剩 ~8M 的根分区。
grep -q " /mnt/ext " /proc/mounts || { echo "部署失败: /mnt/ext 未挂载,拒绝安装" >&2; exit 1; }

HAVE_PREV=0
[ -f "$LASTGOOD" ] && HAVE_PREV=1

# --force-downgrade 是必须的 —— 版本号是 0.0.0-<sha8>，sha 之间没有大小可言，
# 不加它 opkg 会以"已是最新"为由拒装并**返回 0**，部署静默落空。
# 刻意不用 --force-reinstall：它对本地 ipk 会先卸载、再按包名去软件源找，
# 而这个包不在任何 src 里，结果是卸载成功、安装失败、服务被删掉。
echo "安装 $svc_ipk"
opkg install --force-downgrade "$svc_ipk" || fail "opkg 安装 transparent-proxy 失败"

/etc/init.d/transparent-proxy restart || fail "服务重启失败"

# 启动后要先拉 chnroute 才开始监听，固定 sleep 会稳定误报，这里轮询等。
# 探针用 /panel.js 而不是只探 API：panel.js 是前端产物、不入库、只由 CI
# 现场构建再 go:embed 进二进制，它可达才同时证明"包换了"和"前端打进去了"。
i=0
ready=0
while [ "$i" -lt 30 ]; do
    if pidof transparent-proxy >/dev/null 2>&1 &&
        wget -q -O /dev/null -T 5 "http://127.0.0.1:1444/panel.js"; then
        ready=1
        break
    fi
    i=$((i + 1))
    sleep 3
done
[ "$ready" = 1 ] || fail "90s 内服务未就绪或 /panel.js 不可达"

# 再带 key 探一次 /api/status：panel.js 只证明前端在,这条证明「配置(含 api_key)
# 加载成功」。key 从路由器现网配置取(设备状态,CI 没有也不该有)。
#
# key 取不到必须硬失败而不是降级:ADR-0003 增补把「生产组件必须有凭证」立为不变量,
# 空 key 意味着配置丢了/被 -opkg 默认值顶掉了 —— 而一个无鉴权组件对任何探测都 200,
# 降级放行等于把「凭证失效」标记成「部署成功」并存进 lastgood。
# sed 锚死行首:api_key 是顶层字段,宽松匹配会在配置嵌套后取错值。
api_key=$(sed -n 's/^api_key:[[:space:]]*//p' "$CFG" 2>/dev/null | tr -d '"' | head -n1)
[ -n "$api_key" ] || fail "配置里没有 api_key(ADR-0003 增补:生产组件必须有凭证)"
wget -q -O /dev/null -T 5 --header="X-Api-Key: $api_key" http://127.0.0.1:1444/api/status \
    || fail "API 不应答或拒绝了 api_key(配置可能没加载成功)"

# 只有验证全过的 ipk 才有资格当回滚目标。
mkdir -p "$LASTGOOD_DIR"
cp "$svc_ipk" "$LASTGOOD.new" && mv "$LASTGOOD.new" "$LASTGOOD"

echo "部署校验通过: 服务在跑，/panel.js 可达，API 鉴权正常"
