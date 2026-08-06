import { useCallback, useEffect, useState } from 'preact/hooks';
import { EditableConfig, StatusData, errorText } from '../lib/api/client';
import { useApi } from '../lib/api/context';
import { checkerBadge } from './status';

type FeedbackMessage = { text: string; type: 'success' | 'error' };

interface Props {
  checkerStatus?: StatusData['checker'];
}

export function SettingsCard({ checkerStatus }: Props) {
  const api = useApi();
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
      setMessage({ text: errorText(err, '加载配置失败'), type: 'error' });
    }
  }, [api]);

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
    } catch (err) {
      setMessage({ text: errorText(err, '保存失败'), type: 'error' });
    } finally {
      setSaving(false);
    }
  }, [api, draft]);

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
    } catch (err) {
      setMessage({ text: errorText(err, '刷新失败'), type: 'error' });
    } finally {
      setRefreshing(false);
    }
  }, [api]);

  if (!config || !draft) {
    return null;
  }

  const d = editing ? draft : config;
  const badge = checkerBadge(checkerStatus);

  return (
    // 区块直接排在页面上，不套外层大卡（§6）；里面的三个分组是唯一一级子卡。
    <section className="section">
      <div className="section-head">
        <h2 className="section-title">系统设置</h2>
        <span className="section-actions">
          {editing ? (
            <>
              <button type="button" className="secondary outline" onClick={handleCancel}>取消</button>
              <button type="button" onClick={handleSave} disabled={saving} aria-busy={saving}>
                {saving ? '保存中...' : '保存'}
              </button>
            </>
          ) : (
            <button type="button" className="secondary" onClick={() => { setEditing(true); setMessage(null); }}>
              编辑
            </button>
          )}
        </span>
      </div>

      {message && (
        <div className={`notice ${message.type === 'success' ? 'notice-ok' : 'notice-err'}`}>{message.text}</div>
      )}

      <div className="grid-cards">
        {/* 代理设置 */}
        <article>
          <header>代理设置</header>
          <label className="field">
            <span>LAN 接口</span>
            <input type="text" value={d.proxy.lan_interface} disabled={!editing}
              onInput={e => setDraft({ ...draft, proxy: { ...draft.proxy, lan_interface: (e.target as HTMLInputElement).value } })} />
          </label>
          <label className="field">
            <span>默认代理端口</span>
            <input type="number" value={d.proxy.default_port} disabled={!editing}
              onInput={e => setDraft({ ...draft, proxy: { ...draft.proxy, default_port: parseInt((e.target as HTMLInputElement).value) || 0 } })} />
          </label>
          <label className="field">
            <span>强制代理端口</span>
            <input type="number" value={d.proxy.forced_port} disabled={!editing}
              onInput={e => setDraft({ ...draft, proxy: { ...draft.proxy, forced_port: parseInt((e.target as HTMLInputElement).value) || 0 } })} />
          </label>
          <label className="field">
            <span>fwmark</span>
            <input type="number" min="1" max="255" value={d.proxy.self_mark} disabled={!editing}
              onInput={e => setDraft({ ...draft, proxy: { ...draft.proxy, self_mark: parseInt((e.target as HTMLInputElement).value) || 0 } })} />
          </label>
        </article>

        {/* 健康检查 */}
        <article>
          <header>
            <span>健康检查</span>
            <span className={badge.className}>{badge.text}</span>
          </header>
          <label className="field">
            <span>启用</span>
            <span>
              <input type="checkbox" role="switch" checked={d.checker.enabled} disabled={!editing}
                onChange={e => setDraft({ ...draft, checker: { ...draft.checker, enabled: (e.target as HTMLInputElement).checked } })} />
            </span>
          </label>
          {d.checker.enabled && (
            <>
              <label className="field">
                <span>请求方法</span>
                <select value={d.checker.method} disabled={!editing}
                  onChange={e => setDraft({ ...draft, checker: { ...draft.checker, method: (e.target as HTMLSelectElement).value as 'GET' | 'HEAD' } })}>
                  <option value="GET">GET</option>
                  <option value="HEAD">HEAD</option>
                </select>
              </label>
              <label className="field">
                <span>检测 URL</span>
                <input type="text" value={d.checker.url} disabled={!editing}
                  onInput={e => setDraft({ ...draft, checker: { ...draft.checker, url: (e.target as HTMLInputElement).value } })} />
              </label>
              {(editing || d.checker.host) && (
                <label className="field">
                  <span>Host 头</span>
                  <input type="text" value={d.checker.host ?? ''} disabled={!editing}
                    onInput={e => setDraft({ ...draft, checker: { ...draft.checker, host: (e.target as HTMLInputElement).value || undefined } })} />
                </label>
              )}
              <label className="field">
                <span>超时时间</span>
                <input type="text" value={d.checker.timeout} disabled={!editing}
                  onInput={e => setDraft({ ...draft, checker: { ...draft.checker, timeout: (e.target as HTMLInputElement).value } })} />
              </label>
              <label className="field">
                <span>检测间隔</span>
                <input type="text" value={d.checker.interval} disabled={!editing}
                  onInput={e => setDraft({ ...draft, checker: { ...draft.checker, interval: (e.target as HTMLInputElement).value } })} />
              </label>
              <label className="field">
                <span>失败阈值</span>
                <input type="number" min="1" max="10" value={d.checker.failure_threshold} disabled={!editing}
                  onInput={e => setDraft({ ...draft, checker: { ...draft.checker, failure_threshold: parseInt((e.target as HTMLInputElement).value) || 1 } })} />
              </label>
              <label className="field">
                <span>失败时行为</span>
                <select value={d.checker.on_failure ?? 'disable'} disabled={!editing}
                  onChange={e => setDraft({ ...draft, checker: { ...draft.checker, on_failure: (e.target as HTMLSelectElement).value } })}>
                  <option value="disable">禁用代理</option>
                  <option value="keep">保持代理</option>
                </select>
              </label>
              {(editing || d.checker.proxy) && (
                <label className="field">
                  <span>SOCKS5 代理</span>
                  <input type="text" value={d.checker.proxy ?? ''} disabled={!editing}
                    placeholder="如 127.0.0.1:1080"
                    onInput={e => setDraft({ ...draft, checker: { ...draft.checker, proxy: (e.target as HTMLInputElement).value || undefined } })} />
                </label>
              )}
              {(editing || d.checker.bark_token) && (
                <label className="field">
                  <span>Bark Token</span>
                  <input type="text" value={d.checker.bark_token ?? ''} disabled={!editing}
                    placeholder="留空则不通知"
                    onInput={e => setDraft({ ...draft, checker: { ...draft.checker, bark_token: (e.target as HTMLInputElement).value || undefined } })} />
                </label>
              )}
            </>
          )}
        </article>

        {/* CHNRoute */}
        <article>
          <header>
            <span>CHNRoute</span>
            <button type="button" className="secondary" onClick={handleRefreshRoute} disabled={refreshing} aria-busy={refreshing}>
              {refreshing ? '拉取中...' : '拉取'}
            </button>
          </header>
          <label className="field">
            <span>自动刷新</span>
            <span>
              <input type="checkbox" role="switch" checked={d.chnroute.auto_refresh} disabled={!editing}
                onChange={e => setDraft({ ...draft, chnroute: { ...draft.chnroute, auto_refresh: (e.target as HTMLInputElement).checked } })} />
            </span>
          </label>
          <label className="field">
            <span>刷新间隔</span>
            <input type="text" value={d.chnroute.refresh_interval} disabled={!editing}
              onInput={e => setDraft({ ...draft, chnroute: { ...draft.chnroute, refresh_interval: (e.target as HTMLInputElement).value } })} />
          </label>
        </article>
      </div>
    </section>
  );
}
