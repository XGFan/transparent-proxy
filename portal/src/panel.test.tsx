import { beforeEach, describe, expect, it, vi } from 'vitest';
import './panel';
import { defineJoyElement } from './wc/define';

/** 等待 preact 跑完 effect（走 requestAnimationFrame）并完成由 promise 触发的重渲染。 */
async function flush() {
  for (let i = 0; i < 4; i++) {
    await new Promise(resolve => setTimeout(resolve, 25));
  }
}

describe('面板 custom element', () => {
  beforeEach(() => {
    localStorage.clear();
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('注册 tp-card 与 tp-panel', () => {
    expect(customElements.get('tp-card')).toBeTypeOf('function');
    expect(customElements.get('tp-panel')).toBeTypeOf('function');
  });

  it('重复注册同名元素不抛错（bundle 被加载两次的场景）', () => {
    const first = customElements.get('tp-panel');
    expect(() => defineJoyElement('tp-panel', () => null)).not.toThrow();
    expect(customElements.get('tp-panel')).toBe(first);
  });

  it('挂载时读 api-base 并在 open shadow root 内渲染，样式全部内联', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ code: 'ok', message: 'ok', data: { proxy: { enabled: true, status: 'running' }, checker: { enabled: false, running: false, status: 'unknown', consecutiveFailures: 0, lastCheck: '', lastError: '' }, rules: { sets: [], rules: [] } } }),
      { status: 200, headers: { 'Content-Type': 'application/json' } }
    ));
    vi.stubGlobal('fetch', fetchMock);

    const el = document.createElement('tp-card');
    el.setAttribute('api-base', '/tp/api');
    document.body.appendChild(el);
    await flush();

    expect(el.shadowRoot).not.toBeNull();
    expect(el.shadowRoot!.querySelector('style')?.textContent).toContain('.pico');
    expect(el.shadowRoot!.querySelector('.pico')).not.toBeNull();
    expect(fetchMock.mock.calls[0]![0]).toBe('/tp/api/status');
  });

  it('401 时面板内渲染 key 输入界面', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ code: 'unauthorized', message: 'invalid or missing api key', data: {} }),
      { status: 401, headers: { 'Content-Type': 'application/json' } }
    )));

    const el = document.createElement('tp-panel');
    document.body.appendChild(el);
    await flush();

    const input = el.shadowRoot!.querySelector('input[name="apiKey"]');
    expect(input).not.toBeNull();
  });
});
