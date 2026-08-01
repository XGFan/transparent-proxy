import { useCallback, useEffect, useRef, useState } from 'preact/hooks';
import { APIError, StatusData } from './api/client';
import { useApi } from './api/context';

/**
 * 拉取 /status 并暴露刷新入口；card 与 panel 共用。
 *
 * 用 epoch 计数丢弃过期响应：连点「刷新」或快速切代理开关时，先发的请求可能后到，
 * 不丢弃就会拿旧数据覆盖新状态；401 门卫换掉子树后组件已卸载，此时 setState 也是无效写入。
 */
export function useStatus() {
  const api = useApi();
  const [status, setStatus] = useState<StatusData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const epoch = useRef(0);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  const refresh = useCallback(async () => {
    const mine = ++epoch.current;
    // 仍是最新一次请求、且组件还挂着，才允许写状态
    const current = () => alive.current && mine === epoch.current;

    setLoading(true);
    setError(null);
    try {
      const data = await api.getStatus();
      if (!current()) return;
      setStatus(data);
    } catch (err) {
      if (!current()) return;
      setError(err instanceof APIError ? err.message : '获取状态失败');
    }
    if (!current()) return;
    setLoading(false);
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  return { status, setStatus, loading, error, refresh };
}
