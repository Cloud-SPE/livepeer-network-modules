import { useQuery } from "@tanstack/react-query";
import { Activity, Clock, Hash } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api } from "@/lib/api";

export function UsagePage() {
  const summary = useQuery({ queryKey: ["usage-summary"], queryFn: () => api.usageSummary() });
  const recent = useQuery({ queryKey: ["usage-recent"], queryFn: () => api.usageRecent() });

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Usage</h1>
        <p className="text-sm text-muted-foreground">
          Last 30 days. No credits, no quota — this is informational.
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <StatCard
          icon={<Hash className="size-4" />}
          label="Sessions"
          value={summary.data?.total_sessions ?? "—"}
        />
        <StatCard
          icon={<Clock className="size-4" />}
          label="Total seconds"
          value={summary.data?.total_seconds ?? "—"}
        />
        <StatCard
          icon={<Activity className="size-4" />}
          label="Total minutes"
          value={
            summary.data
              ? Math.round(summary.data.total_seconds / 60)
              : "—"
          }
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Recent sessions</CardTitle>
          <CardDescription>50 most recent.</CardDescription>
        </CardHeader>
        <CardContent>
          {recent.isLoading && (
            <p className="text-sm text-muted-foreground">Loading…</p>
          )}
          {recent.data && recent.data.events.length === 0 && (
            <p className="text-sm text-muted-foreground">No sessions yet.</p>
          )}
          {recent.data && recent.data.events.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Session</TableHead>
                  <TableHead>Orchestrator</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead className="text-right">Duration (s)</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {recent.data.events.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className="font-mono">
                      {e.session_id.slice(0, 12)}…
                    </TableCell>
                    <TableCell className="font-mono text-muted-foreground">
                      {e.orchestrator ?? "—"}
                    </TableCell>
                    <TableCell>{new Date(e.started_at).toLocaleString()}</TableCell>
                    <TableCell className="text-right">
                      {e.duration_seconds ?? "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function StatCard({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: React.ReactNode;
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardDescription className="flex items-center gap-1.5">
          {icon}
          {label}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="text-3xl font-semibold tracking-tight">{value}</div>
      </CardContent>
    </Card>
  );
}
