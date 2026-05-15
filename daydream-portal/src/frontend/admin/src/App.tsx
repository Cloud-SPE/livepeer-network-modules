import type { JSX } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Shell } from "@/components/layout/shell";
import { useCreds } from "@/hooks/useCreds";
import { LoginPage } from "@/pages/login";
import { SignupsPage } from "@/pages/signups";
import { UsagePage } from "@/pages/usage";

function Protected({ children }: { children: JSX.Element }) {
  const creds = useCreds();
  if (!creds) return <Navigate to="/login" replace />;
  return children;
}

export function App() {
  const creds = useCreds();
  return (
    <Shell>
      <Routes>
        <Route
          path="/"
          element={<Navigate to={creds ? "/signups" : "/login"} replace />}
        />
        <Route path="/login" element={<LoginPage />} />
        <Route
          path="/signups"
          element={
            <Protected>
              <SignupsPage />
            </Protected>
          }
        />
        <Route
          path="/usage"
          element={
            <Protected>
              <UsagePage />
            </Protected>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Shell>
  );
}
