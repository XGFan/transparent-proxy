import { useCallback, useState } from 'preact/hooks';
import { errorText } from '../lib/api/client';
import { useApi } from '../lib/api/context';
import { useStatus } from '../lib/useStatus';
import { ProxyToggle } from './ProxyToggle';
import { RuleSets } from './RuleSets';
import { SettingsCard } from './SettingsCard';

type Notice = { text: string; type: 'success' | 'error' };

/**
 * <tp-panel> 的内容：代理开关 + 规则同步 + 系统设置 + 规则集管理。
 */
export function TpPanel() {
  const api = useApi();
  const { status, setStatus, loading, error, refresh } = useStatus();
  const [proxyUpdating, setProxyUpdating] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [ruleFeedback, setRuleFeedback] = useState<{
    loading: boolean;
    error: string | null;
    success: string | null;
  }>({ loading: false, error: null, success: null });

  const clearRuleFeedback = useCallback(() => {
    setRuleFeedback({ loading: false, error: null, success: null });
  }, []);

  const handleProxyToggle = useCallback(async (enabled: boolean) => {
    setProxyUpdating(true);
    setNotice(null);
    try {
      const proxy = await api.updateProxy(enabled);
      setStatus(prev => (prev ? { ...prev, proxy } : prev));
      setNotice({ text: proxy.enabled ? '透明代理已开启' : '透明代理已关闭', type: 'success' });
    } catch (err) {
      setNotice({ text: errorText(err, '切换失败'), type: 'error' });
    } finally {
      setProxyUpdating(false);
    }
  }, [api, setStatus]);

  const handleSyncRules = useCallback(async () => {
    setNotice(null);
    try {
      await api.syncRules();
      setNotice({ text: '规则同步完成', type: 'success' });
      await refresh();
    } catch (err) {
      setNotice({ text: errorText(err, '同步失败'), type: 'error' });
    }
  }, [api, refresh]);

  const handleAddRule = useCallback(async (setName: string) => {
    const ip = drafts[setName];
    if (!ip) return;
    setRuleFeedback({ loading: true, error: null, success: null });
    try {
      await api.addRule({ set: setName, ip });
      setRuleFeedback({ loading: false, error: null, success: `已添加 ${ip} 到 ${setName}` });
      setDrafts(prev => ({ ...prev, [setName]: '' }));
      await refresh();
    } catch (err) {
      setRuleFeedback({ loading: false, error: errorText(err, '添加失败'), success: null });
    }
  }, [api, drafts, refresh]);

  const handleRemoveRule = useCallback(async (setName: string, ip: string) => {
    setRuleFeedback({ loading: true, error: null, success: null });
    try {
      await api.removeRule({ set: setName, ip });
      setRuleFeedback({ loading: false, error: null, success: `已从 ${setName} 移除 ${ip}` });
      await refresh();
    } catch (err) {
      setRuleFeedback({ loading: false, error: errorText(err, '删除失败'), success: null });
    }
  }, [api, refresh]);

  const handleDraftChange = useCallback((setName: string, value: string) => {
    setDrafts(prev => ({ ...prev, [setName]: value }));
  }, []);

  if (loading && !status) {
    return <div className="panel" aria-busy="true">加载中...</div>;
  }

  if (error && !status) {
    // 面板不渲染组件名（视觉语言 §1，宿主已给身份），失败态就是一条 notice + 重试。
    return (
      <div className="panel">
        <div className="notice notice-err">{error}</div>
        <button type="button" className="secondary" onClick={refresh}>重试</button>
      </div>
    );
  }

  return (
    // .panel 是面板的内容列（§5：72rem 居中 + 两侧 1rem），壳内与 standalone 同一份。
    <div className="panel">
      {/* 首个区块没有卡壳（§6）：开关本体连同它的文字就是区块头的左半边，
          右半边放两个安静动作；提示条作为这一块的内容排在分隔线下方。 */}
      <section className="section">
        <div className="section-head">
          <ProxyToggle proxy={status?.proxy} updating={proxyUpdating} onToggle={handleProxyToggle} />
          <div className="section-actions">
            <button type="button" className="secondary outline" onClick={refresh}>刷新</button>
            <button type="button" className="secondary" onClick={handleSyncRules}>同步规则</button>
          </div>
        </div>

        {notice && (
          <div className={`notice ${notice.type === 'success' ? 'notice-ok' : 'notice-err'}`}>
            {notice.text}
          </div>
        )}
      </section>

      <SettingsCard checkerStatus={status?.checker} />

      <RuleSets
        rules={status?.rules?.rules}
        drafts={drafts}
        feedback={ruleFeedback}
        onDraftChange={handleDraftChange}
        onAdd={handleAddRule}
        onRemove={handleRemoveRule}
        onClearFeedback={clearRuleFeedback}
      />
    </div>
  );
}
