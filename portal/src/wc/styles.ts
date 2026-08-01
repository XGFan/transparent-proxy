// 注入 shadow root 的样式：Pico conditional（作用域 .pico 容器，无 :root/body 全局选择器）
// + vendored joy tokens + 组件自有样式，分层与顺序都在 shadow.css 里定义，见那份文件的注释。
import shadowCss from '../styles/shadow.css?inline';

export const shadowStyles = shadowCss;
