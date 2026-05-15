import { useQuery } from "@tanstack/react-query";
import { Activity, Clock, Hash, Users } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
} from "@/components/ui/card";
import { adminApi } from "@/lib/api";

export function UsagePage() {
  const summary = useQuery({
    queryKey: ["admin-usage"],
    queryFn: () => adminApi.usageSummary(),
  });

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Operator usage</h1>
        <p className="text-sm text-muted-foreground">
          Aggregated across all customers, last 30 days.
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-4 gap-4">
        <Tile
          icon={<Hash className="size-4" />}
          label="Sessions"
          value={summary.data?.total_sessions ?? "—"}
        />
        <Tile
          icon={<Users className="size-4" />}
          label="Unique customers"
          value={summary.data?.unique_customers ?? "—"}
        />
        <Tile
          icon={<Clock className="size-4" />}
          label="Total seconds"
          value={summary.data?.total_seconds ?? "—"}
        />
        <Tile
          icon={<Activity className="size-4" />}
          label="Total minutes"
          value={summary.data ? Math.round(summary.data.total_seconds / 60) : "—"}
        />
      </div>
    </div>
  );
}

function Tile({
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
