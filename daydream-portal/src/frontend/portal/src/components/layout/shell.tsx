// App chrome. Sidebar pattern matches the dashboard nav in
// apps/streamdiffusion/src/components/Dashboard/SideNav.tsx — collapsed
// left rail, brand at top, simple link list, account/sign-out at bottom.

import {
  Activity,
  KeyRound,
  LayoutDashboard,
  LogOut,
  MessageSquareText,
  PlayCircle,
} from "lucide-react";
import { NavLink, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { clearSession } from "@/lib/session";
import { useSession } from "@/hooks/useSession";

const SIGNED_IN_NAV = [
  { to: "/playground", label: "Playground", icon: PlayCircle },
  { to: "/prompts", label: "Prompts", icon: MessageSquareText },
  { to: "/usage", label: "Usage", icon: Activity },
  { to: "/account", label: "Account", icon: KeyRound },
];

const SIGNED_OUT_NAV = [
  { to: "/waitlist", label: "Request access", icon: LayoutDashboard },
  { to: "/login", label: "Sign in", icon: KeyRound },
];

export function Shell({ children }: { children: React.ReactNode }) {
  const session = useSession();
  const navigate = useNavigate();
  const items = session ? SIGNED_IN_NAV : SIGNED_OUT_NAV;

  return (
    <div className="min-h-screen bg-neutral-950 text-foreground flex">
      <aside className="w-60 shrink-0 border-r border-border bg-sidebar text-sidebar-foreground flex flex-col">
        <div className="px-5 py-6">
          <div className="text-base font-semibold tracking-tight">Daydream</div>
          <div className="text-xs text-sidebar-foreground/60">Portal</div>
        </div>
        <Separator />
        <nav className="flex-1 px-3 py-4 flex flex-col gap-1">
          {items.map((i) => (
            <NavLink
              key={i.to}
              to={i.to}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-sidebar-accent text-sidebar-accent-foreground"
                    : "hover:bg-sidebar-accent/60",
                )
              }
            >
              <i.icon className="size-4" />
              <span>{i.label}</span>
            </NavLink>
          ))}
        </nav>
        {session && (
          <>
            <Separator />
            <div className="p-3 flex flex-col gap-2">
              <div className="px-2 py-1 text-xs text-sidebar-foreground/60 truncate">
                {session.email}
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  clearSession();
                  navigate("/login");
                }}
                className="justify-start gap-2"
              >
                <LogOut className="size-4" />
                Sign out
              </Button>
            </div>
          </>
        )}
      </aside>
      <main className="flex-1 min-w-0">
        <div className="mx-auto max-w-5xl px-8 py-10">{children}</div>
      </main>
    </div>
  );
}
