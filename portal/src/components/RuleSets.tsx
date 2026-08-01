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
    <article>
      <header>规则集</header>

      {(feedback.loading || feedback.error || feedback.success) && (
        <div className={`notice ${feedback.error ? 'notice-err' : feedback.success ? 'notice-ok' : ''}`}>
          {feedback.loading && <span>处理中...</span>}
          {feedback.error && <span>{feedback.error}</span>}
          {feedback.success && <span>{feedback.success}</span>}
          {!feedback.loading && (
            <button type="button" className="secondary outline" onClick={onClearFeedback} aria-label="关闭">×</button>
          )}
        </div>
      )}

      <div className="grid-cards">
        {rules?.map(set => (
          <article key={set.name}>
            <header>
              <span>{set.name}</span>
              <span className="muted">{set.type || '未知类型'} · {set.elems?.length ?? 0}</span>
            </header>

            {set.error ? (
              <p className="muted">{set.error}</p>
            ) : (
              <>
                {set.elems && set.elems.length > 0 ? (
                  <ul className="rule-list">
                    {set.elems.map((elem, idx) => (
                      <li key={`${set.name}-${elem}-${idx}`}>
                        <span>{elem}</span>
                        <button
                          type="button"
                          className="secondary outline"
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
                  <p className="muted">暂无规则</p>
                )}

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
    </article>
  );
}
