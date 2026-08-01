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
