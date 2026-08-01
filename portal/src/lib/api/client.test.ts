import { beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError, createApiClient, getApiKey, setApiKey } from './client';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('createApiClient', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('拼接 apiBase 并在有存 key 时附带 X-Api-Key', async () => {
    setApiKey('s3cret');
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ code: 'ok', message: 'ok', data: { ip: '1.2.3.4' } }));
    vi.stubGlobal('fetch', fetchMock);

    await createApiClient('/tp/api').getStatus();

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/tp/api/status');
    expect((init.headers as Record<string, string>)['X-Api-Key']).toBe('s3cret');
  });

  it('无存 key 时不发送 X-Api-Key 头', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ code: 'ok', message: 'ok', data: {} }));
    vi.stubGlobal('fetch', fetchMock);

    await createApiClient('/api').getStatus();

    const init = fetchMock.mock.calls[0]![1];
    expect((init.headers as Record<string, string>)['X-Api-Key']).toBeUndefined();
  });

  it('401 触发 onUnauthorized 并抛 unauthorized', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      jsonResponse({ code: 'unauthorized', message: 'invalid or missing api key', data: {} }, 401)
    ));
    const onUnauthorized = vi.fn();

    await expect(createApiClient('/api', onUnauthorized).getStatus())
      .rejects.toMatchObject({ code: 'unauthorized', status: 401 });
    expect(onUnauthorized).toHaveBeenCalledOnce();
  });

  it('非 ok 信封抛出 APIError 并保留 code', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      jsonResponse({ code: 'invalid_request', message: 'invalid payload', data: {} }, 400)
    ));

    await expect(createApiClient('/api').addRule({ ip: 'x', set: 'proxy_src' }))
      .rejects.toBeInstanceOf(APIError);
  });

  it('key 读写走 localStorage 的 tp.apiKey', () => {
    setApiKey('abc');
    expect(localStorage.getItem('tp.apiKey')).toBe('abc');
    expect(getApiKey()).toBe('abc');
    setApiKey('');
    expect(getApiKey()).toBe('');
  });
});
