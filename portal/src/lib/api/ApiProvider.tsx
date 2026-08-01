import { ComponentChildren } from 'preact';
import { useCallback, useMemo, useState } from 'preact/hooks';
import { createApiClient, setApiKey } from './client';
import { ApiContext } from './context';
import { ApiKeyForm } from '../../components/ApiKeyForm';

interface Props {
  apiBase: string;
  children: ComponentChildren;
}

/**
 * 提供 ApiClient；收到 401/403 时改为渲染 key 输入界面，
 * 提交后写 localStorage 并重挂载子树重试（面板契约 §鉴权）。
 */
export function ApiProvider({ apiBase, children }: Props) {
  const [needKey, setNeedKey] = useState(false);
  // attempt 变化会重建 client 并重挂载子树，等价于「带新 key 重试」
  const [attempt, setAttempt] = useState(0);

  const handleUnauthorized = useCallback(() => setNeedKey(true), []);
  const client = useMemo(
    () => createApiClient(apiBase, handleUnauthorized),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [apiBase, handleUnauthorized, attempt]
  );

  const handleSubmit = useCallback((key: string) => {
    setApiKey(key);
    setNeedKey(false);
    setAttempt(n => n + 1);
  }, []);

  if (needKey) {
    return <ApiKeyForm onSubmit={handleSubmit} />;
  }

  return (
    <ApiContext.Provider value={client} key={attempt}>
      {children}
    </ApiContext.Provider>
  );
}
