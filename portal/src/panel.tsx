import { ApiProvider } from './lib/api/ApiProvider';
import { TpCard } from './components/TpCard';
import { TpPanel } from './components/TpPanel';
import { defineJoyElement } from './wc/define';

// 面板契约 v1：单文件自包含 ES module，注册 <tp-card> 与 <tp-panel>。
defineJoyElement('tp-card', apiBase => (
  <ApiProvider apiBase={apiBase}><TpCard /></ApiProvider>
));

defineJoyElement('tp-panel', apiBase => (
  <ApiProvider apiBase={apiBase}><TpPanel /></ApiProvider>
));
