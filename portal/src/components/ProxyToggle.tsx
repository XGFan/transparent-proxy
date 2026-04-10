import type { StatusData } from '../lib/api/client';

interface ProxyToggleProps {
  proxy: StatusData['proxy'] | undefined;
  updating: boolean;
  onToggle: (enabled: boolean) => void;
}

export function ProxyToggle({ proxy, updating, onToggle }: ProxyToggleProps) {
  return (
    <div className="header-left">
      <label className="proxy-switch proxy-toggle-combined proxy-toggle-inline" htmlFor="proxy-toggle-input">
        <span className="proxy-inline-name">透明代理</span>
        <input
          id="proxy-toggle-input"
          type="checkbox"
          aria-label="透明代理开关"
          checked={proxy?.enabled ?? false}
          onChange={event => onToggle((event.target as HTMLInputElement).checked)}
          disabled={updating}
        />
        <span className="proxy-switch-slider" aria-hidden="true" />
        <span className={`proxy-switch-label ${proxy?.enabled ? 'status-enabled' : 'status-disabled'}`}>
          {proxy?.status === 'running' ? '已启动' :
            proxy?.status === 'stopped' ? '已停止' : '状态未知'}
        </span>
      </label>
      {updating && <span className="proxy-updating">切换中...</span>}
    </div>
  );
}
