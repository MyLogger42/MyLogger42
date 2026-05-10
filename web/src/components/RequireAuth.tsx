import type { ReactNode } from 'react';
import { Navigate } from 'react-router-dom';
import { useAuth } from '../hooks/useAuth';

interface Props {
  children: ReactNode;
}

// RequireAuth gates everything inside the AppShell. Status === 'unknown'
// renders a small placeholder so the bare login page doesn't flash on
// every reload while /me is in flight.
//
// The auth state itself lives in the useAuth hook; this component only
// renders the routing decision.
export function RequireAuth({ children }: Props) {
  const { status } = useAuth();
  if (status === 'unknown') {
    return <div className="boot">Loading...</div>;
  }
  if (status === 'anon') {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}
