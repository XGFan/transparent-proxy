import './AppShell.css';
import { StatusPage } from '../features/status/StatusPage';

function AppShell() {
  return (
    <div className="app-shell">
      <header className="app-header">
        <div className="header-brand">
          <span className="brand-icon">◈</span>
          <h1 className="brand-title">Transparent Proxy</h1>
        </div>
      </header>

      <main className="app-main">
        <StatusPage />
      </main>

      <footer className="app-footer">
        <span className="footer-status">Ready</span>
        <span className="footer-version">v0.3.0</span>
      </footer>
    </div>
  );
}

export default AppShell;
