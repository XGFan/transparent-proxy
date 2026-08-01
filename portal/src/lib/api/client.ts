/**
 * API Client - 与后端通信的类型安全客户端
 *
 * 按面板契约：apiBase 由 custom element 的 api-base 属性传入，
 * 鉴权头统一 X-Api-Key，key 存在 localStorage['tp.apiKey']。
 */

export interface APIResponse<T = unknown> {
  code: 'ok' | 'invalid_request' | 'internal_error' | 'not_implemented' | 'unauthorized';
  message: string;
  data: T;
}

export interface RuleSetView {
  name: string;
  type: string;
  elems: string[];
  error?: string;
}

export interface RulesData {
  sets: string[];
  rules: RuleSetView[];
}

export interface RuleRequest {
  ip: string;
  set: string;
}

export interface RuleOperationData {
  set: string;
  ip: string;
  rule: RuleSetView;
  operation: {
    action: 'add' | 'remove';
    result: 'applied';
  };
}

export interface SyncData {
  synced: string[];
  results: Array<{
    rule: RuleSetView;
    operation: {
      action: 'sync';
      result: 'applied';
      output: string;
    };
  }>;
}

export interface CheckerConfig {
  enabled: boolean;
  method: 'GET' | 'HEAD';
  url: string;
  host?: string;
  timeout: string;
  failure_threshold: number;
  interval: string;
  on_failure?: string;
  proxy?: string;
  bark_token?: string;
}

export interface CheckerStatus {
  enabled: boolean;
  method?: 'GET' | 'HEAD';
  url?: string;
  host?: string;
  timeout?: string;
  failure_threshold?: number;
  interval?: string;
  running: boolean;
  status: 'up' | 'down' | 'unknown';
  consecutiveFailures: number;
  lastCheck: string;
  lastError: string;
}

export interface StatusData {
  proxy: {
    enabled: boolean;
    status: 'running' | 'stopped' | 'unknown';
  };
  checker: CheckerStatus;
  rules: RulesData;
}

export interface ProxyConfig {
  lan_interface: string;
  default_port: number;
  forced_port: number;
  self_mark: number;
}

export interface ChnRouteConfig {
  auto_refresh: boolean;
  refresh_interval: string;
}

export interface EditableConfig {
  listen: string;
  proxy: ProxyConfig;
  checker: CheckerConfig;
  chnroute: ChnRouteConfig;
}

export class APIError extends Error {
  constructor(
    public code: string,
    message: string,
    public status: number,
    public details?: unknown
  ) {
    super(message);
    this.name = 'APIError';
  }
}

/** 把任意异常转成可展示文案。 */
export function errorText(err: unknown, fallback: string): string {
  return err instanceof APIError ? err.message : fallback;
}

const API_KEY_STORAGE = 'tp.apiKey';

export function getApiKey(): string {
  try {
    return localStorage.getItem(API_KEY_STORAGE) ?? '';
  } catch {
    return '';
  }
}

export function setApiKey(key: string): void {
  try {
    if (key) {
      localStorage.setItem(API_KEY_STORAGE, key);
    } else {
      localStorage.removeItem(API_KEY_STORAGE);
    }
  } catch {
    // localStorage 不可用（隐私模式等）时忽略，退化为单次会话
  }
}

export interface ApiClient {
  getStatus(): Promise<StatusData>;
  getRules(): Promise<RulesData>;
  addRule(request: RuleRequest): Promise<RuleOperationData>;
  removeRule(request: RuleRequest): Promise<RuleOperationData>;
  syncRules(): Promise<SyncData>;
  refreshRoute(): Promise<void>;
  getChecker(): Promise<CheckerStatus>;
  updateChecker(config: CheckerConfig): Promise<CheckerStatus>;
  updateProxy(enabled: boolean): Promise<StatusData['proxy']>;
  getConfig(): Promise<EditableConfig>;
  updateConfig(config: EditableConfig): Promise<EditableConfig>;
}

/**
 * 创建绑定到指定 apiBase 的客户端。
 * onUnauthorized：收到 401/403 时回调（面板据此渲染 key 输入界面）。
 */
export function createApiClient(apiBase: string, onUnauthorized?: () => void): ApiClient {
  const base = apiBase.replace(/\/+$/, '');

  async function apiRequest<T>(endpoint: string, options?: RequestInit): Promise<T> {
    const key = getApiKey();
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(options?.headers as Record<string, string> | undefined),
    };
    if (key) {
      headers['X-Api-Key'] = key;
    }

    let response: Response;
    try {
      response = await fetch(`${base}${endpoint}`, { ...options, headers });
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        throw err;
      }
      throw new APIError('network_error', '网络连接失败，请检查网络后重试', 0);
    }

    // 401/403 先于 JSON 解析处理：反代可能返回非 envelope 响应
    if (response.status === 401 || response.status === 403) {
      onUnauthorized?.();
      throw new APIError('unauthorized', 'API Key 无效或缺失', response.status);
    }

    let envelope: APIResponse<T>;
    try {
      envelope = await response.json();
    } catch {
      throw new APIError('parse_error', `服务器返回了无效的响应 (HTTP ${response.status})`, response.status);
    }

    if (envelope.code !== 'ok') {
      throw new APIError(envelope.code, envelope.message, response.status, envelope.data);
    }

    return envelope.data;
  }

  return {
    getStatus: () => apiRequest<StatusData>('/status'),
    getRules: () => apiRequest<RulesData>('/rules'),
    addRule: request => apiRequest<RuleOperationData>('/rules/add', {
      method: 'POST',
      body: JSON.stringify(request),
    }),
    removeRule: request => apiRequest<RuleOperationData>('/rules/remove', {
      method: 'POST',
      body: JSON.stringify(request),
    }),
    syncRules: () => apiRequest<SyncData>('/rules/sync', { method: 'POST' }),
    refreshRoute: async () => {
      await apiRequest<unknown>('/refresh-route', { method: 'POST' });
    },
    getChecker: () => apiRequest<CheckerStatus>('/checker'),
    updateChecker: config => apiRequest<CheckerStatus>('/checker', {
      method: 'PUT',
      body: JSON.stringify(config),
    }),
    updateProxy: enabled => apiRequest<StatusData['proxy']>('/proxy', {
      method: 'PUT',
      body: JSON.stringify({ enabled }),
    }),
    getConfig: () => apiRequest<EditableConfig>('/config'),
    updateConfig: config => apiRequest<EditableConfig>('/config', {
      method: 'PUT',
      body: JSON.stringify(config),
    }),
  };
}
