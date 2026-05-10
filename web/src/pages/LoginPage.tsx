import { useState } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import { HttpError } from '../api/client';
import { useAuth } from '../hooks/useAuth';

// LoginPage is the only public-facing route. On successful login we
// navigate to /projects, which then resolves the user's last-active
// project (or shows the list if there isn't one).
//
// 503 from /auth/me — the server's "no admin configured" signal — is
// surfaced as a setup hint pointing at the CLI bootstrap path.
export function LoginPage() {
  const { status, login } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (status === 'authed') {
    return <Navigate to="/projects" replace />;
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await login(username, password);
      navigate('/projects', { replace: true });
    } catch (err) {
      if (err instanceof HttpError && err.status === 503) {
        setError(
          'No admin user configured yet. Run `mylogger42 admin create-user` ' +
            'on the server, then try again.',
        );
      } else if (err instanceof Error) {
        setError(err.message);
      } else {
        setError('login failed');
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="login-shell">
      <form className="login-card" onSubmit={onSubmit}>
        <h1 className="login-title">
          MyLogger<span className="brand-accent">42</span>
        </h1>
        <p className="login-subtitle">Sign in to continue</p>

        <label className="login-field">
          <span>Username</span>
          <input
            type="text"
            autoComplete="username"
            autoFocus
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </label>
        <label className="login-field">
          <span>Password</span>
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>

        {error ? <div className="banner banner-error">{error}</div> : null}

        <button type="submit" disabled={busy}>
          {busy ? 'Signing in...' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}
