/**
 * API Client - 与后端通信的类型安全客户端
 */

export interface APIResponse<T = unknown> {
  code: 'ok' | 'invalid_request' | 'internal_error' | 'not_implemented';
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
  host: string;
  timeout: string;
  failureThreshold: number;
  checkInterval: string;
}

export interface CheckerStatus {
  enabled: boolean;
  method?: 'GET' | 'HEAD';
  url?: string;
  host?: string;
  timeout?: string;
  failureThreshold?: number;
  checkInterval?: string;
  running: boolean;
  status: 'up' | 'down' | 'checking';
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

const DEFAULT_API_BASE_PATH = '/api';

function getRuntimeApiBasePath(): string {
  if (typeof window === 'undefined') {
    return DEFAULT_API_BASE_PATH;
  }
  const runtimeBasePath = new URLSearchParams(window.location.search).get('apiBasePath');
  const trimmed = runtimeBasePath?.trim();
  if (!trimmed) return DEFAULT_API_BASE_PATH;
  return trimmed.startsWith('/') ? trimmed : `/${trimmed}`;
}

function resolveApiEndpoint(endpoint: string): string {
  const normalized = endpoint.startsWith('/') ? endpoint : `/${endpoint}`;
  return `${getRuntimeApiBasePath()}${normalized}`;
}

async function apiRequest<T>(endpoint: string, options?: RequestInit): Promise<T> {
  const response = await fetch(resolveApiEndpoint(endpoint), {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
  });

  const envelope: APIResponse<T> = await response.json();

  if (envelope.code !== 'ok') {
    throw new APIError(envelope.code, envelope.message, response.status, envelope.data);
  }

  return envelope.data;
}

export const api = {
  async getStatus(): Promise<StatusData> {
    return apiRequest<StatusData>('/status');
  },

  async getRules(): Promise<RulesData> {
    return apiRequest<RulesData>('/rules');
  },

  async addRule(request: RuleRequest): Promise<RuleOperationData> {
    return apiRequest<RuleOperationData>('/rules/add', {
      method: 'POST',
      body: JSON.stringify(request),
    });
  },

  async removeRule(request: RuleRequest): Promise<RuleOperationData> {
    return apiRequest<RuleOperationData>('/rules/remove', {
      method: 'POST',
      body: JSON.stringify(request),
    });
  },

  async syncRules(): Promise<SyncData> {
    return apiRequest<SyncData>('/rules/sync', {
      method: 'POST',
    });
  },

  async refreshRoute(): Promise<void> {
    await apiRequest<void>('/refresh-route', { method: 'POST' });
  },

  async getChecker(): Promise<CheckerStatus> {
    return apiRequest<CheckerStatus>('/checker');
  },

  async updateChecker(config: CheckerConfig): Promise<CheckerStatus> {
    return apiRequest<CheckerStatus>('/checker', {
      method: 'PUT',
      body: JSON.stringify(config),
    });
  },

  async updateProxy(enabled: boolean): Promise<StatusData['proxy']> {
    return apiRequest<StatusData['proxy']>('/proxy', {
      method: 'PUT',
      body: JSON.stringify({ enabled }),
    });
  },
};
