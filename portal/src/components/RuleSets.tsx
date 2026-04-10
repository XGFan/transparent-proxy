import type { RuleSetView } from '../lib/api/client';

interface RuleFeedback {
  type: 'add' | 'remove' | null;
  loading: boolean;
  error: string | null;
  success: string | null;
}

interface RuleSetsProps {
  rules: RuleSetView[] | undefined;
  drafts: Record<string, string>;
  feedback: RuleFeedback;
  onDraftChange: (setName: string, value: string) => void;
  onAdd: (setName: string) => void;
  onRemove: (setName: string, ip: string) => void;
  onClearFeedback: () => void;
}

export function RuleSets({ rules, drafts, feedback, onDraftChange, onAdd, onRemove, onClearFeedback }: RuleSetsProps) {
  return (
    <section className="sets-overview">
      <h3>规则集概览</h3>

      {(feedback.loading || feedback.error || feedback.success) && (
        <div className={`operation-feedback ${feedback.error ? 'error' : feedback.success ? 'success' : 'loading'}`}>
          {feedback.loading && <span className="loading-text">处理中...</span>}
          {feedback.error && <span className="error-text">{feedback.error}</span>}
          {feedback.success && <span className="success-text">{feedback.success}</span>}
          <button type="button" className="close-btn" onClick={onClearFeedback} aria-label="关闭">
            ×
          </button>
        </div>
      )}

      <div className="sets-grid">
        {rules?.map(set => (
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
                          onClick={() => onRemove(set.name, elem)}
                          disabled={feedback.loading}
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
                    value={drafts[set.name] || ''}
                    onChange={e => onDraftChange(set.name, (e.target as HTMLInputElement).value)}
                    disabled={feedback.loading}
                    className="ip-input-inline"
                  />
                  <button
                    type="button"
                    className="add-btn-inline"
                    onClick={() => onAdd(set.name)}
                    disabled={!drafts[set.name] || feedback.loading}
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
  );
}
