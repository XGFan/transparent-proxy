import type { RuleSetView } from '../lib/api/client';

interface RuleFeedback {
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
    // 区块直接排在页面上，不套外层大卡（§6）；每个 set 一张子卡。
    <section className="section">
      <div className="section-head">
        <h2 className="section-title">规则集</h2>
      </div>

      {(feedback.loading || feedback.error || feedback.success) && (
        <div className={`notice ${feedback.error ? 'notice-err' : feedback.success ? 'notice-ok' : ''}`}>
          {feedback.loading && <span>处理中...</span>}
          {feedback.error && <span>{feedback.error}</span>}
          {feedback.success && <span>{feedback.success}</span>}
          {!feedback.loading && (
            <button type="button" className="secondary outline btn-inline" onClick={onClearFeedback} aria-label="关闭">×</button>
          )}
        </div>
      )}

      <div className="grid-cards">
        {rules?.map(set => (
          <article key={set.name}>
            <header>
              <span>{set.name}</span>
              <span className="head-note">
                <span className="badge">{set.type || '未知类型'}</span>
                {set.elems?.length ?? 0} 条
              </span>
            </header>

            {set.error ? (
              <div className="notice notice-err">{set.error}</div>
            ) : (
              <>
                {set.elems && set.elems.length > 0 ? (
                  <ul className="rule-list">
                    {set.elems.map((elem, idx) => (
                      <li key={`${set.name}-${elem}-${idx}`}>
                        {/* IP / CIDR 是数据标识，走 code 样式（§3/§9） */}
                        <span className="code">{elem}</span>
                        <button
                          type="button"
                          className="outline btn-danger"
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
                  <div className="empty">
                    <p>暂无规则</p>
                    <p>在下方填入 IP / CIDR 后添加一条</p>
                  </div>
                )}

                {/* 顶标签（§10）：placeholder 只说格式，不能当标签用 */}
                <label className="rule-add-label">添加规则</label>
                <div className="rule-add">
                  <input
                    type="text"
                    placeholder="IP / CIDR / 区间"
                    value={drafts[set.name] || ''}
                    onInput={e => onDraftChange(set.name, (e.target as HTMLInputElement).value)}
                    disabled={feedback.loading}
                    aria-label={`向 ${set.name} 添加规则`}
                  />
                  <button
                    type="button"
                    onClick={() => onAdd(set.name)}
                    disabled={!drafts[set.name] || feedback.loading}
                  >
                    添加
                  </button>
                </div>
              </>
            )}
          </article>
        ))}
      </div>
    </section>
  );
}
