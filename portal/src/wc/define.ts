import { ComponentChild, render } from 'preact';
import { shadowStyles } from './styles';

const DEFAULT_API_BASE = '/api';

export type PanelRenderer = (apiBase: string) => ComponentChild;

/**
 * 定义一个面板 custom element：open shadow DOM，样式全部内联进 shadow，
 * api-base 在挂载时读取一次（契约 v1 不要求响应属性变更）。
 * 重复注册直接跳过，避免同页面多次加载 bundle 时抛错。
 */
export function defineJoyElement(tag: string, renderer: PanelRenderer): void {
  if (customElements.get(tag)) {
    return;
  }

  class JoyPanelElement extends HTMLElement {
    private mount: HTMLDivElement | null = null;
    private mounted = false;

    connectedCallback() {
      if (this.mounted) {
        return;
      }
      // 主题固定亮色（面板契约「样式 → 主题固定亮色」）。打在宿主元素上是因为 shadow 内的 Pico
      // 用 :host(...) 判主题：暗色块的选择器是 :host(:not([data-theme]))，只要这个属性存在就不生效；
      // 亮色块 :host(:not([data-theme=dark])) 则照常命中。暗色规则因此原样留着，将来做主题开关可用。
      this.setAttribute('data-theme', 'light');

      if (!this.mount) {
        const shadow = this.attachShadow({ mode: 'open' });

        const style = document.createElement('style');
        style.textContent = shadowStyles;
        shadow.appendChild(style);

        // Pico conditional 构建的作用域容器
        this.mount = document.createElement('div');
        this.mount.className = 'pico';
        shadow.appendChild(this.mount);
      }

      const apiBase = (this.getAttribute('api-base') || DEFAULT_API_BASE).replace(/\/+$/, '');
      render(renderer(apiBase), this.mount);
      this.mounted = true;
    }

    disconnectedCallback() {
      if (this.mounted && this.mount) {
        render(null, this.mount);
        this.mounted = false;
      }
    }
  }

  customElements.define(tag, JoyPanelElement);
}
