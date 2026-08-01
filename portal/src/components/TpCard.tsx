import { useCallback, useState } from 'preact/hooks';
import { useApi } from '../lib/api/context';
import { useStatus } from '../lib/useStatus';
import { ProxyToggle } from './ProxyToggle';
import { checkerBadge, proxyStatusText } from './status';

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
    return <article aria-busy="true">加载中...</article>;
  }

  if (error && !status) {
    return (
      <article>
        <header>Transparent Proxy</header>
        <p className="muted">{error}</p>
        <button type="button" className="secondary" onClick={refresh}>重试</button>
      </article>
    );
  }

  const checker = checkerBadge(status?.checker);
  const setCount = status?.rules?.rules?.reduce((sum, s) => sum + (s.elems?.length ?? 0), 0) ?? 0;

  return (
    <article>
      <header>
        <span>Transparent Proxy</span>
        <span className={`badge ${status?.proxy?.enabled ? 'badge-ok' : 'badge-warn'}`}>
          {proxyStatusText(status?.proxy)}
        </span>
      </header>

      <ul className="kv">
        <li className="kv-key">健康检查</li>
        <li className="kv-val"><span className={checker.className}>{checker.text}</span></li>
        <li className="kv-key">规则条目</li>
        <li className="kv-val">{setCount}</li>
      </ul>

      <ProxyToggle proxy={status?.proxy} updating={updating} onToggle={handleToggle} />
    </article>
  );
}
