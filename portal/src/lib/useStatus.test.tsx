import { render } from 'preact';
import { beforeEach, describe, expect, it } from 'vitest';
import type { ApiClient, StatusData } from './api/client';
import { ApiContext } from './api/context';
import { useStatus } from './useStatus';

function statusWith(state: StatusData['proxy']['status']): StatusData {
  return {
    proxy: { enabled: state === 'running', status: state },
    checker: { enabled: false, running: false, status: 'unknown', consecutiveFailures: 0, lastCheck: '', lastError: '' },
    rules: { sets: [], rules: [] },
  };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>(r => { resolve = r; });
  return { promise, resolve };
}

/** 等 preact 跑完 effect（走 requestAnimationFrame）并完成 promise 触发的重渲染。 */
async function flush() {
  for (let i = 0; i < 3; i++) {
    await new Promise(r => setTimeout(r, 25));
  }
}

function Probe() {
  const { status, refresh } = useStatus();
  return (
    <div>
      <span id="value">{status?.proxy?.status ?? '-'}</span>
      <button id="go" type="button" onClick={() => { refresh(); }}>go</button>
    </div>
  );
}

describe('useStatus 的过期响应保护', () => {
  let host: HTMLDivElement;

  beforeEach(() => {
    document.body.innerHTML = '';
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  it('后发的请求先回时，先发的旧响应不再覆盖状态', async () => {
    const slow = deferred<StatusData>();
    const fast = deferred<StatusData>();
    const calls = [slow.promise, fast.promise];
    let n = 0;
    const api = { getStatus: () => calls[n++]! } as unknown as ApiClient;

    render(<ApiContext.Provider value={api}><Probe /></ApiContext.Provider>, host);
    await flush();

    // 第二次请求（后发）先回
    host.querySelector<HTMLButtonElement>('#go')!.click();
    await flush();
    fast.resolve(statusWith('stopped'));
    await flush();
    expect(host.querySelector('#value')!.textContent).toBe('stopped');

    // 第一次请求（先发）后回：必须被丢弃
    slow.resolve(statusWith('running'));
    await flush();
    expect(host.querySelector('#value')!.textContent).toBe('stopped');
  });

  it('组件卸载后响应到达不触发状态写入', async () => {
    const pending = deferred<StatusData>();
    const api = { getStatus: () => pending.promise } as unknown as ApiClient;

    render(<ApiContext.Provider value={api}><Probe /></ApiContext.Provider>, host);
    await flush();

    render(null, host);            // 卸载（等价于 401 门卫换掉子树）
    pending.resolve(statusWith('running'));
    await expect(flush()).resolves.toBeUndefined();   // 不应抛错
    expect(host.querySelector('#value')).toBeNull();
  });
});
