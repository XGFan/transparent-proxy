import { useCallback, useEffect, useState } from 'react';
import { api, StatusData, CheckerConfig, CheckerStatus, APIError } from '../../lib/api/client';
import './StatusPage.css';

const defaultCheckerConfig: CheckerConfig = {
  enabled: false,
  method: 'GET',
  url: '',
  host: '',
  timeout: '10s',
  failureThreshold: 3,
  checkInterval: '30s',
};

function checkerStatusToConfig(checker: CheckerStatus, fallback: CheckerConfig = defaultCheckerConfig): CheckerConfig {
  return {
    enabled: checker.enabled,
    method: checker.method === 'HEAD' ? 'HEAD' : 'GET',
    url: checker.url ?? fallback.url,
    host: checker.host ?? fallback.host,
    timeout: checker.timeout ?? fallback.timeout,
    failureThreshold: checker.failureThreshold ?? fallback.failureThreshold,
    checkInterval: checker.checkInterval ?? fallback.checkInterval,
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
  const [saveMessage, setSaveMessage] = useState<string | null>(null);

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
      const [statusData, checkerData] = await Promise.all([
        api.getStatus(),
        api.getChecker(),
      ]);
      setStatus(statusData);
      setCheckerConfig((prev) => checkerStatusToConfig(checkerData, prev));
    } catch (err) {
      setError(err instanceof APIError ? err.message : '获取状态失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  const handleSaveConfig = async () => {
    setSaving(true);
    setSaveMessage(null);
    try {
      const result = await api.updateChecker(checkerConfig);
      setCheckerConfig((prev) => checkerStatusToConfig(result, prev));
      setSaveMessage('配置已保存');
      setStatus(prev => prev ? { ...prev, checker: result } : null);
      setCheckerEditing(false);
    } catch (err) {
      setSaveMessage(err instanceof APIError ? err.message : '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const handleProxyToggle = async (enabled: boolean) => {
    setProxyUpdating(true);
    setSaveMessage(null);
    try {
      const proxy = await api.updateProxy(enabled);
      setStatus(prev => prev ? { ...prev, proxy } : prev);
      setSaveMessage(proxy.enabled ? '透明代理已开启' : '透明代理已关闭');
    } catch (err) {
      setSaveMessage(err instanceof APIError ? err.message : '切换透明代理失败');
    } finally {
      setProxyUpdating(false);
    }
  };

  const handleSyncChnroute = async () => {
    setSaveMessage(null);
    try {
      await api.syncRules();
      setSaveMessage('CHNROUTE 同步完成');
      fetchStatus();
    } catch (err) {
      setSaveMessage(err instanceof APIError ? err.message : '同步失败');
    }
  };

  const handleCheckerToggle = async (enabled: boolean) => {
    const newConfig = { ...checkerConfig, enabled };
    setCheckerConfig(newConfig);
    if (!checkerEditing) {
      setSaving(true);
      setSaveMessage(null);
      try {
        const result = await api.updateChecker(newConfig);
        setCheckerConfig((prev) => checkerStatusToConfig(result, prev));
        setSaveMessage(enabled ? '检测已启用' : '检测已禁用');
        setStatus(prev => prev ? { ...prev, checker: result } : null);
      } catch (err) {
        setSaveMessage(err instanceof APIError ? err.message : '保存失败');
        setCheckerConfig(checkerConfig);
      } finally {
        setSaving(false);
      }
    }
  };

  const handleStartCheckerEdit = () => {
    setCheckerEditing(true);
    setSaveMessage(null);
  };

  const handleCancelCheckerEdit = () => {
    setCheckerEditing(false);
    setSaveMessage(null);
    void fetchStatus();
  };

  const handleAddRule = async (setName: string) => {
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
  };

  const handleRemoveRule = async (setName: string, ip: string) => {
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
  };

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
        <div className="header-left">
          <label className="proxy-switch proxy-toggle-combined proxy-toggle-inline" htmlFor="proxy-toggle-input">
            <span className="proxy-inline-name">透明代理</span>
            <input
              id="proxy-toggle-input"
              type="checkbox"
              aria-label="透明代理开关"
              checked={status?.proxy?.enabled ?? false}
              onChange={event => handleProxyToggle(event.target.checked)}
              disabled={proxyUpdating}
            />
            <span className="proxy-switch-slider" aria-hidden="true" />
            <span className={`proxy-switch-label ${status?.proxy?.enabled ? 'status-enabled' : 'status-disabled'}`}>
              {status?.proxy?.status === 'running' ? '已启动' :
                status?.proxy?.status === 'stopped' ? '已停止' : '状态未知'}
            </span>
          </label>
          {proxyUpdating && <span className="proxy-updating">切换中...</span>}
        </div>
        <div className="header-actions">
          <button type="button" onClick={fetchStatus} className="refresh-btn">
            刷新
          </button>
          <button
            type="button"
            className="sync-btn"
            onClick={handleSyncChnroute}
          >
            同步 CHNROUTE
          </button>
        </div>
      </div>

      <div className="status-overview">
        <div className="status-card checker-card">
          <div className="checker-card-head">
            <div className="card-label">网络检测</div>
            <div className="checker-head-actions">
              {checkerEditing ? (
                <button type="button" className="checker-cancel-btn" onClick={handleCancelCheckerEdit}>
                  取消
                </button>
              ) : (
                <button type="button" className="checker-edit-btn" onClick={handleStartCheckerEdit}>
                  编辑
                </button>
              )}
              <label className="checker-enable-switch" htmlFor="checker-enable-input">
                <span className="checker-enable-label">启用检测</span>
                <span className="proxy-switch">
                  <input
                    id="checker-enable-input"
                    type="checkbox"
                    aria-label="启用检测"
                    checked={checkerConfig.enabled}
                    disabled={saving}
                    onChange={(e) => handleCheckerToggle(e.target.checked)}
                  />
                  <span className="proxy-switch-slider" aria-hidden="true" />
                </span>
              </label>
            </div>
          </div>

          {checkerEditing ? (
            <div className="config-form checker-config-form">
              {checkerConfig.enabled ? (
                <>
                  <div className={`card-value ${status?.checker?.status === 'up' ? 'status-enabled' :
                                      status?.checker?.status === 'down' ? 'status-disabled' : 'status-unknown'}`}>
                    {status?.checker?.status === 'up' ? '正常' :
                      status?.checker?.status === 'down'
                        ? ((status?.checker?.consecutiveFailures ?? 0) < checkerConfig.failureThreshold ? '抖动' : '错误')
                        : '等待检测'}
                  </div>
                  {(status?.checker?.consecutiveFailures ?? 0) > 0 && (
                    <div className="checker-meta-row">
                      <span>连续失败次数</span>
                      <strong>{status.checker!.consecutiveFailures}</strong>
                    </div>
                  )}
                  {status?.checker?.lastError && (
                    <div className="card-error">{status.checker.lastError}</div>
                  )}

                  <label className="config-row">
                    <span>请求方法</span>
                    <select
                      value={checkerConfig.method}
                      onChange={(e) => setCheckerConfig({ ...checkerConfig, method: e.target.value as 'GET' | 'HEAD' })}
                    >
                      <option value="GET">GET</option>
                      <option value="HEAD">HEAD</option>
                    </select>
                  </label>

                  <label className="config-row">
                    <span>检测 URL</span>
                    <input
                      type="text"
                      value={checkerConfig.url}
                      onChange={(e) => setCheckerConfig({ ...checkerConfig, url: e.target.value })}
                      placeholder="http://example.com/path"
                    />
                  </label>

                  <label className="config-row">
                    <span>Host 头</span>
                    <input
                      type="text"
                      value={checkerConfig.host}
                      onChange={(e) => setCheckerConfig({ ...checkerConfig, host: e.target.value })}
                      placeholder="可选，留空使用 URL 中的 host"
                    />
                  </label>

                  <label className="config-row">
                    <span>超时时间</span>
                    <input
                      type="text"
                      value={checkerConfig.timeout}
                      onChange={(e) => setCheckerConfig({ ...checkerConfig, timeout: e.target.value })}
                      placeholder="10s"
                    />
                  </label>

                  <label className="config-row">
                    <span>失败阈值</span>
                    <input
                      type="number"
                      min="1"
                      max="10"
                      value={checkerConfig.failureThreshold}
                      onChange={(e) => setCheckerConfig({ ...checkerConfig, failureThreshold: parseInt(e.target.value) || 3 })}
                    />
                  </label>

                  <label className="config-row">
                    <span>检测间隔</span>
                    <input
                      type="text"
                      value={checkerConfig.checkInterval}
                      onChange={(e) => setCheckerConfig({ ...checkerConfig, checkInterval: e.target.value })}
                      placeholder="30s"
                    />
                  </label>
                </>
              ) : (
                <div className="checker-empty-state">当前未启用网络检测，勾选“启用检测”后保存生效。</div>
              )}

              <div className="config-actions">
                <button
                  type="button"
                  className="save-btn"
                  onClick={handleSaveConfig}
                  disabled={saving}
                >
                  {saving ? '保存中...' : '保存配置'}
                </button>
              </div>

              {saveMessage && (
                <div className={`save-message ${saveMessage.includes('失败') ? 'error' : 'success'}`}>
                  {saveMessage}
                </div>
              )}
            </div>
          ) : (
            <div className="checker-view-section">
              {checkerConfig.enabled ? (
                <>
                  <div className={`card-value ${status?.checker?.status === 'up' ? 'status-enabled' :
                                      status?.checker?.status === 'down' ? 'status-disabled' : 'status-unknown'}`}>
                    {status?.checker?.status === 'up' ? '正常' :
                      status?.checker?.status === 'down'
                        ? ((status?.checker?.consecutiveFailures ?? 0) < checkerConfig.failureThreshold ? '抖动' : '错误')
                        : '等待检测'}
                  </div>
                  {(status?.checker?.consecutiveFailures ?? 0) > 0 && (
                    <div className="checker-meta-row">
                      <span>连续失败次数</span>
                      <strong>{status.checker!.consecutiveFailures}</strong>
                    </div>
                  )}
                  {status?.checker?.lastError && (
                    <div className="card-error">{status.checker.lastError}</div>
                  )}
                  <div className="checker-view-grid">
                    <div className="checker-view-row"><span>请求方法</span><strong>{checkerConfig.method}</strong></div>
                    <div className="checker-view-row"><span>检测 URL</span><strong>{checkerConfig.url || '（空）'}</strong></div>
                    <div className="checker-view-row"><span>Host 头</span><strong>{checkerConfig.host || '（空）'}</strong></div>
                    <div className="checker-view-row"><span>超时时间</span><strong>{checkerConfig.timeout}</strong></div>
                    <div className="checker-view-row"><span>失败阈值</span><strong>{checkerConfig.failureThreshold}</strong></div>
                    <div className="checker-view-row"><span>检测间隔</span><strong>{checkerConfig.checkInterval}</strong></div>
                  </div>
                </>
              ) : (
                <div className="checker-empty-state">当前未启用网络检测。</div>
              )}

              {saveMessage && (
                <div className={`save-message ${saveMessage.includes('失败') ? 'error' : 'success'}`}>
                  {saveMessage}
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      <section className="sets-overview">
        <h3>规则集概览</h3>
        
        {/* 规则操作反馈条 */}
        {(ruleFeedback.loading || ruleFeedback.error || ruleFeedback.success) && (
          <div className={`operation-feedback ${ruleFeedback.error ? 'error' : ruleFeedback.success ? 'success' : 'loading'}`}>
            {ruleFeedback.loading && <span className="loading-text">处理中...</span>}
            {ruleFeedback.error && <span className="error-text">{ruleFeedback.error}</span>}
            {ruleFeedback.success && <span className="success-text">{ruleFeedback.success}</span>}
            <button type="button" className="close-btn" onClick={clearRuleFeedback} aria-label="关闭">
              ×
            </button>
          </div>
        )}

        <div className="sets-grid">
          {status?.rules?.rules?.map(set => (
            <div key={set.name} className={`set-card ${set.error ? 'has-error' : ''}`}>
              <div className="set-header">
                <span className="set-name">{set.name}</span>
                <span className="set-type">{set.type || '未知类型'}</span>
              </div>
              <div className="set-count">
                {set.elems?.length ?? 0} 条规则
              </div>
              {set.error ? (
                <div className="set-error">{set.error}</div>
              ) : (
                <div className="set-content">
                  {set.elems && set.elems.length > 0 ? (
                    <ul className="rule-list">
                      {set.elems.map((elem, idx) => (
                        <li key={`${set.name}-${elem}-${idx}`} className="rule-item">
                          <span className="rule-ip">{elem}</span>
                          <button
                            type="button"
                            className="remove-btn"
                            onClick={() => handleRemoveRule(set.name, elem)}
                            disabled={ruleFeedback.loading}
                            aria-label={`删除规则 ${elem}`}
                          >
                            删除
                          </button>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <div className="empty-state">暂无规则</div>
                  )}
                  <div className="add-rule-inline">
                    <input
                      type="text"
                      placeholder="IP 地址 (如: 192.168.1.1)"
                      value={setDrafts[set.name] || ''}
                      onChange={e => setSetDrafts(prev => ({ ...prev, [set.name]: e.target.value }))}
                      disabled={ruleFeedback.loading}
                      className="ip-input-inline"
                    />
                    <button
                      type="button"
                      className="add-btn-inline"
                      onClick={() => handleAddRule(set.name)}
                      disabled={!setDrafts[set.name] || ruleFeedback.loading}
                    >
                      添加
                    </button>
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

export default StatusPage;
