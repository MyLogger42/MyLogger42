import { Link, NavLink, Route, Routes } from 'react-router-dom';
import { LogsPage } from './pages/LogsPage';
import { LiveTailPage } from './pages/LiveTailPage';

// Top-level layout. Two routes: /logs (history search) and /live
// (WebSocket live tail). The default route redirects to /logs.
export function App() {
  return (
    <div className="app">
      <header className="app-header">
        <Link to="/logs" className="brand">
          MyLogger<span className="brand-accent">42</span>
        </Link>
        <nav className="app-nav">
          <NavLink to="/logs" className={navClass}>Logs</NavLink>
          <NavLink to="/live" className={navClass}>Live tail</NavLink>
        </nav>
      </header>
      <main className="app-main">
        <Routes>
          <Route path="/" element={<LogsPage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/live" element={<LiveTailPage />} />
        </Routes>
      </main>
    </div>
  );
}

function navClass({ isActive }: { isActive: boolean }) {
  return isActive ? 'nav-link nav-link-active' : 'nav-link';
}
