import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, StatusData, CheckerConfig, CheckerStatus, APIError } from '../../lib/api/client';
import { ProxyToggle } from '../../components/ProxyToggle';
import { CheckerCard } from '../../components/CheckerCard';
import { RuleSets } from '../../components/RuleSets';
import './StatusPage.css';

type FeedbackMessage = { text: string; type: 'success' | 'error' };

const defaultCheckerConfig: CheckerConfig = {
  enabled: false,
  method: 'GET',
  url: '',
  host: '',
  timeout: '10s',
  failure_threshold: 3,
  interval: '30s',
};

function checkerStatusToConfig(checker: CheckerStatus, fallback: CheckerConfig = defaultCheckerConfig): CheckerConfig {
  return {
    enabled: checker.enabled,
    method: checker.method === 'HEAD' ? 'HEAD' : 'GET',
    url: checker.url ?? fallback.url,
    host: checker.host ?? fallback.host,
    timeout: checker.timeout ?? fallback.timeout,
    failure_threshold: checker.failure_threshold ?? fallback.failure_threshold,
    interval: checker.interval ?? fallback.interval,
  };
}

export function StatusPage() {
  const [status, setStatus] = useState<StatusData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [checkerConfig, setCheckerConfig] = useState<CheckerConfig>(defaultCheckerConfig);
  const [checkerEditing, setCheckerEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [proxyUpdating, setProxyUpdating] = useState(false);
  const [saveMessage, setSaveMessage] = useState<FeedbackMessage | null>(null);

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

  const showSuccess = useCallback((text: string) => setSaveMessage({ text, type: 'success' }), []);
  const showError = useCallback((text: string) => setSaveMessage({ text, type: 'error' }), []);

  const fetchStatus = useCallback(async () => {
    setLoading(true);
    setError(null);
    const [statusResult, checkerResult] = await Promise.allSettled([
      api.getStatus(),
      api.getChecker(),
    ]);

    if (statusResult.status === 'fulfilled') {
      setStatus(statusResult.value);
    } else {
      const err = statusResult.reason;
      setError(err instanceof APIError ? err.message : '获取状态失败');
    }

    if (checkerResult.status === 'fulfilled') {
      setCheckerConfig((prev) => checkerStatusToConfig(checkerResult.value, prev));
    }

    setLoading(false);
  }, []);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  const handleSaveConfig = useCallback(async () => {
    setSaving(true);
    setSaveMessage(null);
    try {
      const result = await api.updateChecker(checkerConfig);
      setCheckerConfig((prev) => checkerStatusToConfig(result, prev));
      showSuccess('配置已保存');
      setStatus(prev => prev ? { ...prev, checker: result } : null);
      setCheckerEditing(false);
    } catch (err) {
      showError(err instanceof APIError ? err.message : '保存失败');
    } finally {
      setSaving(false);
    }
  }, [checkerConfig, showSuccess, showError]);

  const handleProxyToggle = useCallback(async (enabled: boolean) => {
    setProxyUpdating(true);
    setSaveMessage(null);
    try {
      const proxy = await api.updateProxy(enabled);
      setStatus(prev => prev ? { ...prev, proxy } : prev);
      showSuccess(proxy.enabled ? '透明代理已开启' : '透明代理已关闭');
    } catch (err) {
      showError(err instanceof APIError ? err.message : '切换透明代理失败');
    } finally {
      setProxyUpdating(false);
    }
  }, [showSuccess, showError]);

  const handleSyncRules = useCallback(async () => {
    setSaveMessage(null);
    try {
      await api.syncRules();
      showSuccess('规则同步完成');
      fetchStatus();
    } catch (err) {
      showError(err instanceof APIError ? err.message : '同步失败');
    }
  }, [fetchStatus, showSuccess, showError]);

  const handleCheckerToggle = useCallback(async (enabled: boolean) => {
    const newConfig = { ...checkerConfig, enabled };
    setCheckerConfig(newConfig);
    if (!checkerEditing) {
      setSaving(true);
      setSaveMessage(null);
      try {
        const result = await api.updateChecker(newConfig);
        setCheckerConfig((prev) => checkerStatusToConfig(result, prev));
        showSuccess(enabled ? '检测已启用' : '检测已禁用');
        setStatus(prev => prev ? { ...prev, checker: result } : null);
      } catch (err) {
        showError(err instanceof APIError ? err.message : '保存失败');
        setCheckerConfig(checkerConfig);
      } finally {
        setSaving(false);
      }
    }
  }, [checkerConfig, checkerEditing, showSuccess, showError]);

  const handleStartCheckerEdit = useCallback(() => {
    setCheckerEditing(true);
    setSaveMessage(null);
  }, []);

  const handleCancelCheckerEdit = useCallback(() => {
    setCheckerEditing(false);
    setSaveMessage(null);
    void fetchStatus();
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
      const message = err instanceof APIError ? err.message : '添加规则失败';
      setRuleFeedback({ type: 'add', loading: false, error: message, success: null });
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
      const message = err instanceof APIError ? err.message : '删除规则失败';
      setRuleFeedback({ type: 'remove', loading: false, error: message, success: null });
    }
  }, [fetchStatus, clearRuleFeedback]);

  const handleDraftChange = useCallback((setName: string, value: string) => {
    setSetDrafts(prev => ({ ...prev, [setName]: value }));
  }, []);

  if (loading) {
    return (
      <div className="status-page">
        <div className="loading-indicator">加载中...</div>
      </div>
    );
  }

  if (error && !status) {
    return (
      <div className="status-page">
        <div className="error-message">
          <span className="error-icon">⚠</span>
          <span>{error}</span>
          <button type="button" onClick={fetchStatus} className="retry-btn">
            重试
          </button>
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
          <button type="button" onClick={fetchStatus} className="refresh-btn">
            刷新
          </button>
          <button
            type="button"
            className="sync-btn"
            onClick={handleSyncRules}
          >
            同步规则
          </button>
        </div>
      </div>

      <div className="status-overview">
        <CheckerCard
          checkerStatus={status?.checker}
          config={checkerConfig}
          editing={checkerEditing}
          saving={saving}
          saveMessage={saveMessage}
          onConfigChange={setCheckerConfig}
          onToggle={handleCheckerToggle}
          onStartEdit={handleStartCheckerEdit}
          onCancelEdit={handleCancelCheckerEdit}
          onSave={handleSaveConfig}
        />
      </div>

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
