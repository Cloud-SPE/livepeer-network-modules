import { Activity, BarChart3, LogOut, Users } from "lucide-react";
import { NavLink, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { clearCreds } from "@/lib/creds";
import { useCreds } from "@/hooks/useCreds";

const NAV = [
  { to: "/signups", label: "Signups", icon: Users },
  { to: "/usage", label: "Usage", icon: Activity },
];

export function Shell({ children }: { children: React.ReactNode }) {
  const creds = useCreds();
  const navigate = useNavigate();

  return (
    <div className="min-h-screen bg-neutral-950 text-foreground flex">
      <aside className="w-60 shrink-0 border-r border-border bg-sidebar text-sidebar-foreground flex flex-col">
        <div className="px-5 py-6">
          <div className="text-base font-semibold tracking-tight">Daydream</div>
          <div className="text-xs text-sidebar-foreground/60 flex items-center gap-1">
            <BarChart3 className="size-3" />
            Admin
          </div>
        </div>
        <Separator />
        {creds && (
          <nav className="flex-1 px-3 py-4 flex flex-col gap-1">
            {NAV.map((i) => (
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
                {i.label}
              </NavLink>
            ))}
          </nav>
        )}
        {!creds && <div className="flex-1" />}
        {creds && (
          <>
            <Separator />
            <div className="p-3 flex flex-col gap-2">
              <div className="px-2 py-1 text-xs text-sidebar-foreground/60 truncate">
                {creds.actor}
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  clearCreds();
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
        <div className="mx-auto max-w-6xl px-8 py-10">{children}</div>
      </main>
    </div>
  );
}
