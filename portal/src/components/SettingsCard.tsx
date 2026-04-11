import { useCallback, useEffect, useState } from 'preact/hooks';
import { api, EditableConfig, StatusData, APIError } from '../lib/api/client';

type FeedbackMessage = { text: string; type: 'success' | 'error' };

interface Props {
  checkerStatus?: StatusData['checker'];
}

export function SettingsCard({ checkerStatus }: Props) {
  const [config, setConfig] = useState<EditableConfig | null>(null);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<EditableConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [message, setMessage] = useState<FeedbackMessage | null>(null);

  const fetchConfig = useCallback(async () => {
    try {
      const data = await api.getConfig();
      setConfig(data);
      setDraft(data);
    } catch (err) {
      setMessage({ text: err instanceof APIError ? err.message : '加载配置失败', type: 'error' });
    }
  }, []);

  useEffect(() => { fetchConfig(); }, [fetchConfig]);

  const handleSave = useCallback(async () => {
    if (!draft) return;
    setSaving(true);
    setMessage(null);
    try {
      const saved = await api.updateConfig(draft);
      setConfig(saved);
      setDraft(saved);
      setEditing(false);
      setMessage({ text: '配置已保存（部分更改需重启生效）', type: 'success' });
      setTimeout(() => setMessage(null), 3000);
    } catch (err) {
      setMessage({ text: err instanceof APIError ? err.message : '保存失败', type: 'error' });
      setTimeout(() => setMessage(null), 5000);
    } finally {
      setSaving(false);
    }
  }, [draft]);

  const handleCancel = useCallback(() => {
    setDraft(config);
    setEditing(false);
    setMessage(null);
  }, [config]);

  const handleRefreshRoute = useCallback(async () => {
    setRefreshing(true);
    setMessage(null);
    try {
      await api.refreshRoute();
      setMessage({ text: 'CHNRoute 已刷新', type: 'success' });
      setTimeout(() => setMessage(null), 3000);
    } catch (err) {
      setMessage({ text: err instanceof APIError ? err.message : '刷新失败', type: 'error' });
      setTimeout(() => setMessage(null), 5000);
    } finally {
      setRefreshing(false);
    }
  }, []);

  if (!config || !draft) {
    return null;
  }

  const d = editing ? draft : config;
  const cs = checkerStatus;

  return (
    <section className="sets-overview">
      <div className="settings-header">
        <h3>系统设置</h3>
        <div className="checker-head-actions">
          {editing ? (
            <>
              <button type="button" className="checker-cancel-btn" onClick={handleCancel}>取消</button>
              <button type="button" className="save-btn" onClick={handleSave} disabled={saving}>
                {saving ? '保存中...' : '保存'}
              </button>
            </>
          ) : (
            <button type="button" className="checker-edit-btn" onClick={() => { setEditing(true); setMessage(null); }}>
              编辑
            </button>
          )}
        </div>
      </div>

      {message && (
        <div className={`save-message ${message.type}`}>{message.text}</div>
      )}

      <div className="settings-grid">
        {/* 代理设置 */}
        <div className="set-card">
          <div className="set-header">
            <span className="set-name">代理设置</span>
          </div>
          <div className="settings-fields">
            <label className="config-row">
              <span>LAN 接口</span>
              <input type="text" value={d.proxy.lan_interface} disabled={!editing}
                onChange={e => setDraft({ ...draft, proxy: { ...draft.proxy, lan_interface: (e.target as HTMLInputElement).value } })} />
            </label>
            <label className="config-row">
              <span>默认代理端口</span>
              <input type="number" value={d.proxy.default_port} disabled={!editing}
                onChange={e => setDraft({ ...draft, proxy: { ...draft.proxy, default_port: parseInt((e.target as HTMLInputElement).value) || 0 } })} />
            </label>
            <label className="config-row">
              <span>强制代理端口</span>
              <input type="number" value={d.proxy.forced_port} disabled={!editing}
                onChange={e => setDraft({ ...draft, proxy: { ...draft.proxy, forced_port: parseInt((e.target as HTMLInputElement).value) || 0 } })} />
            </label>
            <label className="config-row">
              <span>fwmark</span>
              <input type="number" min="1" max="255" value={d.proxy.self_mark} disabled={!editing}
                onChange={e => setDraft({ ...draft, proxy: { ...draft.proxy, self_mark: parseInt((e.target as HTMLInputElement).value) || 0 } })} />
            </label>
          </div>
        </div>

        {/* 健康检查 */}
        <div className="set-card">
          <div className="set-header">
            <span className="set-name">健康检查</span>
            {cs && cs.running && (
              <span className={`checker-badge ${cs.status === 'up' ? 'badge-up' : cs.status === 'down' ? 'badge-down' : 'badge-unknown'}`}>
                {cs.status === 'up' ? '正常' : cs.status === 'down' ? '异常' : '等待'}
                {(cs.consecutiveFailures ?? 0) > 0 && ` (${cs.consecutiveFailures})`}
              </span>
            )}
          </div>
          <div className="settings-fields">
            <label className="config-row">
              <span>启用</span>
              <label className="proxy-switch config-switch">
                <input type="checkbox" checked={d.checker.enabled} disabled={!editing}
                  onChange={e => setDraft({ ...draft, checker: { ...draft.checker, enabled: (e.target as HTMLInputElement).checked } })} />
                <span className="proxy-switch-slider" />
              </label>
            </label>
            {d.checker.enabled && (
              <>
                <label className="config-row">
                  <span>请求方法</span>
                  <select value={d.checker.method} disabled={!editing}
                    onChange={e => setDraft({ ...draft, checker: { ...draft.checker, method: (e.target as HTMLSelectElement).value as 'GET' | 'HEAD' } })}>
                    <option value="GET">GET</option>
                    <option value="HEAD">HEAD</option>
                  </select>
                </label>
                <label className="config-row">
                  <span>检测 URL</span>
                  <input type="text" value={d.checker.url} disabled={!editing}
                    onChange={e => setDraft({ ...draft, checker: { ...draft.checker, url: (e.target as HTMLInputElement).value } })} />
                </label>
                {(editing || d.checker.host) && (
                  <label className="config-row">
                    <span>Host 头</span>
                    <input type="text" value={d.checker.host ?? ''} disabled={!editing}
                      onChange={e => setDraft({ ...draft, checker: { ...draft.checker, host: (e.target as HTMLInputElement).value || undefined } })} />
                  </label>
                )}
                <label className="config-row">
                  <span>超时时间</span>
                  <input type="text" value={d.checker.timeout} disabled={!editing}
                    onChange={e => setDraft({ ...draft, checker: { ...draft.checker, timeout: (e.target as HTMLInputElement).value } })} />
                </label>
                <label className="config-row">
                  <span>检测间隔</span>
                  <input type="text" value={d.checker.interval} disabled={!editing}
                    onChange={e => setDraft({ ...draft, checker: { ...draft.checker, interval: (e.target as HTMLInputElement).value } })} />
                </label>
                <label className="config-row">
                  <span>失败阈值</span>
                  <input type="number" min="1" max="10" value={d.checker.failure_threshold} disabled={!editing}
                    onChange={e => setDraft({ ...draft, checker: { ...draft.checker, failure_threshold: parseInt((e.target as HTMLInputElement).value) || 1 } })} />
                </label>
                <label className="config-row">
                  <span>失败时行为</span>
                  <select value={d.checker.on_failure ?? 'disable'} disabled={!editing}
                    onChange={e => setDraft({ ...draft, checker: { ...draft.checker, on_failure: (e.target as HTMLSelectElement).value } })}>
                    <option value="disable">禁用代理</option>
                    <option value="keep">保持代理</option>
                  </select>
                </label>
                {(editing || d.checker.proxy) && (
                  <label className="config-row">
                    <span>SOCKS5 代理</span>
                    <input type="text" value={d.checker.proxy ?? ''} disabled={!editing}
                      placeholder="如 127.0.0.1:1080"
                      onChange={e => setDraft({ ...draft, checker: { ...draft.checker, proxy: (e.target as HTMLInputElement).value || undefined } })} />
                  </label>
                )}
                {(editing || d.checker.bark_token) && (
                  <label className="config-row">
                    <span>Bark Token</span>
                    <input type="text" value={d.checker.bark_token ?? ''} disabled={!editing}
                      placeholder="留空则不通知"
                      onChange={e => setDraft({ ...draft, checker: { ...draft.checker, bark_token: (e.target as HTMLInputElement).value || undefined } })} />
                  </label>
                )}
              </>
            )}
          </div>
        </div>

        {/* CHNRoute */}
        <div className="set-card">
          <div className="set-header">
            <span className="set-name">CHNRoute</span>
            <button type="button" className="checker-edit-btn" onClick={handleRefreshRoute} disabled={refreshing}>
              {refreshing ? '拉取中...' : '拉取'}
            </button>
          </div>
          <div className="settings-fields">
            <label className="config-row">
              <span>自动刷新</span>
              <label className="proxy-switch config-switch">
                <input type="checkbox" checked={d.chnroute.auto_refresh} disabled={!editing}
                  onChange={e => setDraft({ ...draft, chnroute: { ...draft.chnroute, auto_refresh: (e.target as HTMLInputElement).checked } })} />
                <span className="proxy-switch-slider" />
              </label>
            </label>
            <label className="config-row">
              <span>刷新间隔</span>
              <input type="text" value={d.chnroute.refresh_interval} disabled={!editing}
                onChange={e => setDraft({ ...draft, chnroute: { ...draft.chnroute, refresh_interval: (e.target as HTMLInputElement).value } })} />
            </label>
          </div>
        </div>
      </div>
    </section>
  );
}
