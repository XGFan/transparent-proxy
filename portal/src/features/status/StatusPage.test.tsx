import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { StatusPage } from './StatusPage';

const { mockApi, MockAPIError } = vi.hoisted(() => {
  class HoistedAPIError extends Error {
    constructor(message: string) {
      super(message);
      this.name = 'APIError';
    }
  }

  return {
    mockApi: {
      getStatus: vi.fn(),
      getChecker: vi.fn(),
      updateChecker: vi.fn(),
      syncRules: vi.fn(),
      addRule: vi.fn(),
      removeRule: vi.fn(),
      updateProxy: vi.fn(),
    },
    MockAPIError: HoistedAPIError,
  };
});

vi.mock('../../lib/api/client', () => ({
  api: mockApi,
  APIError: MockAPIError,
}));

const baseStatus = {
  proxy: {
    enabled: true,
    status: 'running' as const,
  },
  checker: {
    enabled: true,
    running: true,
    status: 'up' as const,
    consecutiveFailures: 0,
    lastCheck: '',
    lastError: '',
  },
  rules: {
    sets: [],
    rules: [],
  },
};

const baseCheckerResponse = {
  enabled: true,
  method: 'GET' as const,
  url: 'https://example.com/health',
  host: '',
  timeout: '10s',
  failureThreshold: 3,
  checkInterval: '30s',
  running: true,
  status: 'up' as const,
  consecutiveFailures: 0,
  lastCheck: '',
  lastError: '',
};

describe('StatusPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getStatus.mockResolvedValue(baseStatus);
    mockApi.getChecker.mockResolvedValue(baseCheckerResponse);
    mockApi.updateChecker.mockResolvedValue(baseStatus.checker);
    mockApi.syncRules.mockResolvedValue({ synced: [], results: [] });
    mockApi.addRule.mockResolvedValue({});
    mockApi.removeRule.mockResolvedValue({});
  });

  it('支持切换透明代理开关并更新状态显示', async () => {
    mockApi.updateProxy.mockResolvedValue({ enabled: false, status: 'stopped' });

    render(<StatusPage />);

    await screen.findByText('已启动');
    const checkbox = screen.getByLabelText('透明代理开关');
    fireEvent.click(checkbox);

    await waitFor(() => {
      expect(mockApi.updateProxy).toHaveBeenCalledWith(false);
    });

    expect(await screen.findByText('已停止')).toBeInTheDocument();
    expect(await screen.findByText('透明代理已关闭')).toBeInTheDocument();
  });

  it('开关切换失败时展示错误提示', async () => {
    mockApi.updateProxy.mockRejectedValue(new MockAPIError('切换失败'));

    render(<StatusPage />);

    await screen.findByText('已启动');
    fireEvent.click(screen.getByLabelText('透明代理开关'));

    expect(await screen.findByText('切换失败')).toBeInTheDocument();
  });

  it('网络检测默认是只读视图，点击编辑后进入可编辑状态并出现保存按钮', async () => {
    render(<StatusPage />);

    await screen.findByText('网络检测');
    expect(screen.getByRole('button', { name: '编辑' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存配置' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '编辑' }));

    expect(await screen.findByRole('button', { name: '保存配置' })).toBeInTheDocument();
  });

  it('网络检测未启用时默认不显示状态与连续失败次数', async () => {
    mockApi.getStatus.mockResolvedValue({
      ...baseStatus,
      checker: {
        ...baseStatus.checker,
        enabled: false,
      },
    });
    mockApi.getChecker.mockResolvedValue({
      ...baseCheckerResponse,
      enabled: false,
    });

    render(<StatusPage />);

    await screen.findByText('网络检测');
    expect(screen.queryByText('连续失败次数')).not.toBeInTheDocument();
    expect(screen.queryByText('正常')).not.toBeInTheDocument();
    expect(screen.queryByText('抖动')).not.toBeInTheDocument();
    expect(screen.queryByText('错误')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存配置' })).not.toBeInTheDocument();
  });
});
