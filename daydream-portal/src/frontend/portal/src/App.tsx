import type { JSX } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Shell } from "@/components/layout/shell";
import { useSession } from "@/hooks/useSession";
import { WaitlistPage } from "@/pages/waitlist";
import { LoginPage } from "@/pages/login";
import { PlaygroundPage } from "@/pages/playground";
import { PromptsPage } from "@/pages/prompts";
import { UsagePage } from "@/pages/usage";
import { AccountPage } from "@/pages/account";

function Protected({ children }: { children: JSX.Element }) {
  const session = useSession();
  if (!session) return <Navigate to="/login" replace />;
  return children;
}

export function App() {
  const session = useSession();

  return (
    <Shell>
      <Routes>
        <Route
          path="/"
          element={<Navigate to={session ? "/playground" : "/waitlist"} replace />}
        />
        <Route path="/waitlist" element={<WaitlistPage />} />
        <Route path="/login" element={<LoginPage />} />
        <Route
          path="/playground"
          element={
            <Protected>
              <PlaygroundPage />
            </Protected>
          }
        />
        <Route
          path="/prompts"
          element={
            <Protected>
              <PromptsPage />
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
        <Route
          path="/account"
          element={
            <Protected>
              <AccountPage />
            </Protected>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Shell>
  );
}
