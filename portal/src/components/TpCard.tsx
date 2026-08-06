import { useCallback, useState } from 'preact/hooks';
import { useApi } from '../lib/api/context';
import { useStatus } from '../lib/useStatus';
import { ProxyToggle } from './ProxyToggle';
import { checkerBadge } from './status';

/**
 * <tp-card> 的内容：只读摘要 + 唯一高频操作（代理开关）。
 */
export function TpCard() {
  const api = useApi();
  const { status, setStatus, loading, error, refresh } = useStatus();
  const [updating, setUpdating] = useState(false);

  const handleToggle = useCallback(async (enabled: boolean) => {
    setUpdating(true);
    try {
      const proxy = await api.updateProxy(enabled);
      setStatus(prev => (prev ? { ...prev, proxy } : prev));
    } catch {
      await refresh();
    } finally {
      setUpdating(false);
    }
  }, [api, setStatus, refresh]);

  if (loading && !status) {
    return <div aria-busy="true">加载中...</div>;
  }

  if (error && !status) {
    return (
      <div>
        <p className="muted">{error}</p>
        <button type="button" className="secondary" onClick={refresh}>重试</button>
      </div>
    );
  }

  const checker = checkerBadge(status?.checker);
  const setCount = status?.rules?.rules?.reduce((sum, s) => sum + (s.elems?.length ?? 0), 0) ?? 0;

  return (
    // 裸容器：壳的卡片框已提供边框/底色/留白，卡片不再自画外框（面板契约「卡片视觉基准 → 不自画外框」）。
    // 用 Pico 的 <article> 会自带底色+圆角+阴影+1rem padding，套在壳的框里就是「卡中卡」。
    <div>
      {/* 不再渲染组件名标题：壳已在卡片上方放了同名链接（面板契约「卡片视觉基准 → 不重复组件名」）。
        * 原标题右侧的运行状态徽章也一并去掉——它与下方开关行的徽章同源同值（都是 proxyStatusText(status.proxy)），
        * 属于重复而非新信息，故按契约删除而不是转成单独一行。 */}
      <ul className="kv">
        <li className="kv-key">健康检查</li>
        <li className="kv-val"><span className={checker.className}>{checker.text}</span></li>
        <li className="kv-key">规则条目</li>
        <li className="kv-val">{setCount}</li>
      </ul>

      <ProxyToggle proxy={status?.proxy} updating={updating} onToggle={handleToggle} />
    </div>
  );
}
