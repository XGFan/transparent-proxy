import { useState } from 'preact/hooks';
import { getApiKey } from '../lib/api/client';

interface Props {
  onSubmit: (key: string) => void;
}

/** 收到 401/403 时的面板内 key 输入界面。 */
export function ApiKeyForm({ onSubmit }: Props) {
  const [value, setValue] = useState(getApiKey());

  return (
    <article className="tp-authgate">
      <header>需要 API Key</header>
      <form
        onSubmit={e => {
          e.preventDefault();
          const key = value.trim();
          if (key) onSubmit(key);
        }}
      >
        <label>
          X-Api-Key
          <input
            type="password"
            name="apiKey"
            autocomplete="off"
            placeholder="填写后保存到本地并重试"
            value={value}
            onInput={e => setValue((e.target as HTMLInputElement).value)}
          />
        </label>
        <button type="submit" disabled={!value.trim()}>保存并重试</button>
      </form>
    </article>
  );
}
