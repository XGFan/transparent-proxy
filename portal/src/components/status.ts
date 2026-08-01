import type { CheckerStatus, StatusData } from '../lib/api/client';

/** 代理运行状态的中文标签。 */
export function proxyStatusText(proxy?: StatusData['proxy']): string {
  switch (proxy?.status) {
    case 'running':
      return '已启动';
    case 'stopped':
      return '已停止';
    default:
      return '状态未知';
  }
}

export interface Badge {
  className: string;
  text: string;
}

/** 健康检查状态徽标（未启用/未运行时也给出可读文案）。 */
export function checkerBadge(checker?: CheckerStatus): Badge {
  if (!checker || !checker.enabled) {
    return { className: 'badge', text: '未启用' };
  }
  if (!checker.running) {
    return { className: 'badge badge-warn', text: '未运行' };
  }
  const failures = checker.consecutiveFailures ?? 0;
  const suffix = failures > 0 ? ` (${failures})` : '';
  switch (checker.status) {
    case 'up':
      return { className: 'badge badge-ok', text: `正常${suffix}` };
    case 'down':
      return { className: 'badge badge-err', text: `异常${suffix}` };
    default:
      return { className: 'badge badge-warn', text: `等待${suffix}` };
  }
}
