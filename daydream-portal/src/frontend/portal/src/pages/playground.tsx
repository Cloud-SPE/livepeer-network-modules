// Live AI stream surface. Opens a session on daydream-gateway, then
// iframes the scope_url returned by the gateway — Scope itself handles
// the WebRTC handshake, so no media touches this portal.

import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Play, Square } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { api, type OpenSessionResponse, ApiError } from "@/lib/api";

interface ActiveSession {
  sessionId: string;
  scopeUrl: string;
  orchestrator: string | null;
  startedAt: number;
}

export function PlaygroundPage() {
  const [active, setActive] = useState<ActiveSession | null>(null);
  const activeRef = useRef<ActiveSession | null>(null);
  useEffect(() => {
    activeRef.current = active;
  }, [active]);

  const open = useMutation({
    mutationFn: () => api.openSession(),
    onSuccess: (res: OpenSessionResponse) => {
      setActive({
        sessionId: res.session_id,
        scopeUrl: res.scope_url,
        orchestrator: res.orchestrator,
        startedAt: Date.now(),
      });
      toast.success("Session opened");
    },
    onError: (err) =>
      toast.error(
        err instanceof ApiError
          ? `Could not open session: ${err.message}`
          : "Could not open session",
      ),
  });

  const close = useMutation({
    mutationFn: (id: string) => api.closeSession(id),
    onSettled: () => setActive(null),
  });

  // sendBeacon on unload as a best-effort close. The backend handler
  // is idempotent so a duplicate close from a slow tab is harmless.
  useEffect(() => {
    const handler = () => {
      const a = activeRef.current;
      if (!a) return;
      try {
        navigator.sendBeacon(
          `/portal/sessions/${encodeURIComponent(a.sessionId)}/close`,
        );
      } catch {
        // ignore
      }
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, []);

  if (active) {
    return (
      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between">
              <span>Live session</span>
              <Button
                variant="destructive"
                size="sm"
                onClick={() => close.mutate(active.sessionId)}
                disabled={close.isPending}
              >
                <Square className="size-3" />
                Stop
              </Button>
            </CardTitle>
            <CardDescription className="flex flex-wrap gap-3 text-xs">
              <span>
                Session:{" "}
                <span className="font-mono text-foreground">
                  {active.sessionId}
                </span>
              </span>
              {active.orchestrator && (
                <span>
                  Orchestrator:{" "}
                  <span className="font-mono text-foreground">
                    {active.orchestrator}
                  </span>
                </span>
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <iframe
              src={active.scopeUrl}
              allow="camera; microphone; autoplay; clipboard-read; clipboard-write"
              className="w-full h-[600px] rounded-md border border-border bg-black"
              title="Daydream Scope session"
            />
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Badge variant="secondary">Idle</Badge>
          <span>Playground</span>
        </CardTitle>
        <CardDescription>
          Open a live AI streaming session. Your browser connects directly to
          the orchestrator selected by daydream-gateway; no media flows through
          this portal.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Button onClick={() => open.mutate()} disabled={open.isPending} size="lg">
          <Play className="size-4" />
          {open.isPending ? "Opening session…" : "Start session"}
        </Button>
      </CardContent>
    </Card>
  );
}
