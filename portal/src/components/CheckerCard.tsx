import type { CheckerConfig, CheckerStatus } from '../lib/api/client';

type FeedbackMessage = { text: string; type: 'success' | 'error' };

function CheckerStatusSummary({ checker, threshold }: { checker?: CheckerStatus; threshold: number }) {
  if (!checker) return null;
  return (
    <>
      <div className={`card-value ${checker.status === 'up' ? 'status-enabled' :
                          checker.status === 'down' ? 'status-disabled' : 'status-unknown'}`}>
        {checker.status === 'up' ? '正常' :
          checker.status === 'down'
            ? ((checker.consecutiveFailures ?? 0) < threshold ? '抖动' : '错误')
            : '等待检测'}
      </div>
      {(checker.consecutiveFailures ?? 0) > 0 && (
        <div className="checker-meta-row">
          <span>连续失败次数</span>
          <strong>{checker.consecutiveFailures}</strong>
        </div>
      )}
      {checker.lastError && (
        <div className="card-error">{checker.lastError}</div>
      )}
    </>
  );
}

interface CheckerCardProps {
  checkerStatus: CheckerStatus | undefined;
  config: CheckerConfig;
  editing: boolean;
  saving: boolean;
  saveMessage: FeedbackMessage | null;
  onConfigChange: (config: CheckerConfig) => void;
  onToggle: (enabled: boolean) => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
  onSave: () => void;
}

export function CheckerCard({
  checkerStatus,
  config,
  editing,
  saving,
  saveMessage,
  onConfigChange,
  onToggle,
  onStartEdit,
  onCancelEdit,
  onSave,
}: CheckerCardProps) {
  return (
    <div className="status-card checker-card">
      <div className="checker-card-head">
        <div className="card-label">网络检测</div>
        <div className="checker-head-actions">
          {editing ? (
            <button type="button" className="checker-cancel-btn" onClick={onCancelEdit}>
              取消
            </button>
          ) : (
            <button type="button" className="checker-edit-btn" onClick={onStartEdit}>
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
                checked={config.enabled}
                disabled={saving}
                onChange={(e) => onToggle((e.target as HTMLInputElement).checked)}
              />
              <span className="proxy-switch-slider" aria-hidden="true" />
            </span>
          </label>
        </div>
      </div>

      {editing ? (
        <div className="config-form checker-config-form">
          {config.enabled ? (
            <>
              <CheckerStatusSummary checker={checkerStatus} threshold={config.failure_threshold} />

              <label className="config-row">
                <span>请求方法</span>
                <select
                  value={config.method}
                  onChange={(e) => onConfigChange({ ...config, method: (e.target as HTMLSelectElement).value as 'GET' | 'HEAD' })}
                >
                  <option value="GET">GET</option>
                  <option value="HEAD">HEAD</option>
                </select>
              </label>

              <label className="config-row">
                <span>检测 URL</span>
                <input
                  type="text"
                  value={config.url}
                  onChange={(e) => onConfigChange({ ...config, url: (e.target as HTMLInputElement).value })}
                  placeholder="http://example.com/path"
                />
              </label>

              <label className="config-row">
                <span>Host 头</span>
                <input
                  type="text"
                  value={config.host}
                  onChange={(e) => onConfigChange({ ...config, host: (e.target as HTMLInputElement).value })}
                  placeholder="可选，留空使用 URL 中的 host"
                />
              </label>

              <label className="config-row">
                <span>超时时间</span>
                <input
                  type="text"
                  value={config.timeout}
                  onChange={(e) => onConfigChange({ ...config, timeout: (e.target as HTMLInputElement).value })}
                  placeholder="10s"
                />
              </label>

              <label className="config-row">
                <span>失败阈值</span>
                <input
                  type="number"
                  min="1"
                  max="10"
                  value={config.failure_threshold}
                  onChange={(e) => onConfigChange({ ...config, failure_threshold: parseInt((e.target as HTMLInputElement).value) || 3 })}
                />
              </label>

              <label className="config-row">
                <span>检测间隔</span>
                <input
                  type="text"
                  value={config.interval}
                  onChange={(e) => onConfigChange({ ...config, interval: (e.target as HTMLInputElement).value })}
                  placeholder="30s"
                />
              </label>
            </>
          ) : (
            <div className="checker-empty-state">当前未启用网络检测，勾选"启用检测"后保存生效。</div>
          )}

          <div className="config-actions">
            <button
              type="button"
              className="save-btn"
              onClick={onSave}
              disabled={saving}
            >
              {saving ? '保存中...' : '保存配置'}
            </button>
          </div>

          {saveMessage && (
            <div className={`save-message ${saveMessage.type}`}>
              {saveMessage.text}
            </div>
          )}
        </div>
      ) : (
        <div className="checker-view-section">
          {config.enabled ? (
            <>
              <CheckerStatusSummary checker={checkerStatus} threshold={config.failure_threshold} />
              <div className="checker-view-grid">
                <div className="checker-view-row"><span>请求方法</span><strong>{config.method}</strong></div>
                <div className="checker-view-row"><span>检测 URL</span><strong>{config.url || '（空）'}</strong></div>
                <div className="checker-view-row"><span>Host 头</span><strong>{config.host || '（空）'}</strong></div>
                <div className="checker-view-row"><span>超时时间</span><strong>{config.timeout}</strong></div>
                <div className="checker-view-row"><span>失败阈值</span><strong>{config.failure_threshold}</strong></div>
                <div className="checker-view-row"><span>检测间隔</span><strong>{config.interval}</strong></div>
              </div>
            </>
          ) : (
            <div className="checker-empty-state">当前未启用网络检测。</div>
          )}

          {saveMessage && (
            <div className={`save-message ${saveMessage.type}`}>
              {saveMessage.text}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
