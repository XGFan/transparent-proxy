import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, StatusData, APIError } from '../../lib/api/client';
import { ProxyToggle } from '../../components/ProxyToggle';
import { RuleSets } from '../../components/RuleSets';
import { SettingsCard } from '../../components/SettingsCard';
import './StatusPage.css';

export function StatusPage() {
  const [status, setStatus] = useState<StatusData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [proxyUpdating, setProxyUpdating] = useState(false);
  const [message, setMessage] = useState<{ text: string; type: 'success' | 'error' } | null>(null);

  const [setDrafts, setSetDrafts] = useState<Record<string, string>>({});
  const [ruleFeedback, setRuleFeedback] = useState<{
    type: 'add' | 'remove' | null;
    loading: boolean;
    error: string | null;
    success: string | null;
  }>({ type: null, loading: false, error: null, success: null });

  const clearRuleFeedback = useCallback(() => {
    setRuleFeedback({ type: null, loading: false, error: null, success: null });
  }, []);

  const fetchStatus = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.getStatus();
      setStatus(data);
    } catch (err) {
      setError(err instanceof APIError ? err.message : '获取状态失败');
    }
    setLoading(false);
  }, []);

  useEffect(() => { fetchStatus(); }, [fetchStatus]);

  const handleProxyToggle = useCallback(async (enabled: boolean) => {
    setProxyUpdating(true);
    setMessage(null);
    try {
      const proxy = await api.updateProxy(enabled);
      setStatus(prev => prev ? { ...prev, proxy } : prev);
      setMessage({ text: proxy.enabled ? '透明代理已开启' : '透明代理已关闭', type: 'success' });
    } catch (err) {
      setMessage({ text: err instanceof APIError ? err.message : '切换失败', type: 'error' });
    } finally {
      setProxyUpdating(false);
    }
  }, []);

  const handleSyncRules = useCallback(async () => {
    setMessage(null);
    try {
      await api.syncRules();
      setMessage({ text: '规则同步完成', type: 'success' });
      fetchStatus();
    } catch (err) {
      setMessage({ text: err instanceof APIError ? err.message : '同步失败', type: 'error' });
    }
  }, [fetchStatus]);

  const handleAddRule = useCallback(async (setName: string) => {
    const ip = setDrafts[setName];
    if (!ip) return;
    setRuleFeedback({ type: 'add', loading: true, error: null, success: null });
    try {
      await api.addRule({ set: setName, ip });
      setRuleFeedback({ type: 'add', loading: false, error: null, success: `已添加 ${ip} 到 ${setName}` });
      setSetDrafts(prev => ({ ...prev, [setName]: '' }));
      await fetchStatus();
      setTimeout(clearRuleFeedback, 3000);
    } catch (err) {
      setRuleFeedback({ type: 'add', loading: false, error: err instanceof APIError ? err.message : '添加失败', success: null });
    }
  }, [setDrafts, fetchStatus, clearRuleFeedback]);

  const handleRemoveRule = useCallback(async (setName: string, ip: string) => {
    setRuleFeedback({ type: 'remove', loading: true, error: null, success: null });
    try {
      await api.removeRule({ set: setName, ip });
      setRuleFeedback({ type: 'remove', loading: false, error: null, success: `已从 ${setName} 移除 ${ip}` });
      await fetchStatus();
      setTimeout(clearRuleFeedback, 3000);
    } catch (err) {
      setRuleFeedback({ type: 'remove', loading: false, error: err instanceof APIError ? err.message : '删除失败', success: null });
    }
  }, [fetchStatus, clearRuleFeedback]);

  const handleDraftChange = useCallback((setName: string, value: string) => {
    setSetDrafts(prev => ({ ...prev, [setName]: value }));
  }, []);

  if (loading) {
    return <div className="status-page"><div className="loading-indicator">加载中...</div></div>;
  }

  if (error && !status) {
    return (
      <div className="status-page">
        <div className="error-message">
          <span className="error-icon">⚠</span>
          <span>{error}</span>
          <button type="button" onClick={fetchStatus} className="retry-btn">重试</button>
        </div>
      </div>
    );
  }

  return (
    <div className="status-page">
      <div className="status-header">
        <ProxyToggle
          proxy={status?.proxy}
          updating={proxyUpdating}
          onToggle={handleProxyToggle}
        />
        <div className="header-actions">
          <button type="button" onClick={fetchStatus} className="refresh-btn">刷新</button>
          <button type="button" className="sync-btn" onClick={handleSyncRules}>同步规则</button>
        </div>
      </div>

      {message && (
        <div className={`save-message ${message.type}`}>{message.text}</div>
      )}

      {/* Settings */}
      <SettingsCard checkerStatus={status?.checker} />

      {/* Rule sets */}
      <RuleSets
        rules={status?.rules?.rules}
        drafts={setDrafts}
        feedback={ruleFeedback}
        onDraftChange={handleDraftChange}
        onAdd={handleAddRule}
        onRemove={handleRemoveRule}
        onClearFeedback={clearRuleFeedback}
      />
    </div>
  );
}

export default StatusPage;
