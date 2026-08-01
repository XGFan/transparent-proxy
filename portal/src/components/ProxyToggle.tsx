import type { StatusData } from '../lib/api/client';
import { proxyStatusText } from './status';

interface ProxyToggleProps {
  proxy: StatusData['proxy'] | undefined;
  updating: boolean;
  onToggle: (enabled: boolean) => void;
}

export function ProxyToggle({ proxy, updating, onToggle }: ProxyToggleProps) {
  return (
    <div className="switch-row">
      <label>
        <input
          type="checkbox"
          role="switch"
          aria-label="透明代理开关"
          checked={proxy?.enabled ?? false}
          disabled={updating}
          onChange={event => onToggle((event.target as HTMLInputElement).checked)}
        />
        <span>透明代理</span>
      </label>
      <span className={`badge ${proxy?.enabled ? 'badge-ok' : 'badge-warn'}`}>
        {proxyStatusText(proxy)}
      </span>
      {updating && <span className="muted">切换中...</span>}
    </div>
  );
}
