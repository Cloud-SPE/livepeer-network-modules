import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, RefreshCw, X } from "lucide-react";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  adminApi,
  ApiError,
  type ApproveResult,
  type WaitlistEntry,
} from "@/lib/api";

type StatusFilter = "pending" | "approved" | "rejected" | "all";

export function SignupsPage() {
  const qc = useQueryClient();
  const [filter, setFilter] = useState<StatusFilter>("pending");
  const [issued, setIssued] = useState<{ email: string; result: ApproveResult } | null>(
    null,
  );

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ["admin-waitlist", filter],
    queryFn: () =>
      adminApi.listWaitlist(filter === "all" ? undefined : filter),
  });

  const approve = useMutation({
    mutationFn: (e: WaitlistEntry) => adminApi.approve(e.id),
    onSuccess: (result, e) => {
      setIssued({ email: e.email, result });
      qc.invalidateQueries({ queryKey: ["admin-waitlist"] });
    },
    onError: (err) =>
      toast.error(err instanceof ApiError ? err.message : "Approve failed"),
  });

  const reject = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason?: string }) =>
      adminApi.reject(id, reason),
    onSuccess: () => {
      toast.success("Rejected");
      qc.invalidateQueries({ queryKey: ["admin-waitlist"] });
    },
    onError: (err) =>
      toast.error(err instanceof ApiError ? err.message : "Reject failed"),
  });

  return (
    <>
      <div className="flex flex-col gap-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Signups</h1>
            <p className="text-sm text-muted-foreground">
              Approve creates a customer + issues an API key (shown once).
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <RefreshCw className={isFetching ? "size-3 animate-spin" : "size-3"} />
            Refresh
          </Button>
        </div>

        <Tabs value={filter} onValueChange={(v: string) => setFilter(v as StatusFilter)}>
          <TabsList>
            <TabsTrigger value="pending">Pending</TabsTrigger>
            <TabsTrigger value="approved">Approved</TabsTrigger>
            <TabsTrigger value="rejected">Rejected</TabsTrigger>
            <TabsTrigger value="all">All</TabsTrigger>
          </TabsList>
        </Tabs>

        <Card>
          <CardHeader>
            <CardTitle>Queue</CardTitle>
            <CardDescription>
              {data ? `${data.entries.length} entries` : "Loading…"}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
            {data && data.entries.length === 0 && (
              <p className="text-sm text-muted-foreground">Nothing here.</p>
            )}
            {data && data.entries.length > 0 && (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Email</TableHead>
                    <TableHead>Display</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Requested</TableHead>
                    <TableHead className="text-right">Action</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.entries.map((e) => (
                    <TableRow key={e.id}>
                      <TableCell className="font-medium">{e.email}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {e.display_name ?? "—"}
                      </TableCell>
                      <TableCell>
                        <StatusPill status={e.status} />
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {new Date(e.created_at).toLocaleString()}
                      </TableCell>
                      <TableCell className="text-right">
                        {e.status === "pending" ? (
                          <div className="flex justify-end gap-2">
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={() => approve.mutate(e)}
                              disabled={approve.isPending}
                            >
                              <Check className="size-3" />
                              Approve
                            </Button>
                            <Button
                              size="sm"
                              variant="destructive"
                              onClick={() => {
                                const reason = window.prompt(
                                  "Rejection reason (optional)",
                                ) ?? "";
                                reject.mutate({
                                  id: e.id,
                                  reason: reason || undefined,
                                });
                              }}
                            >
                              <X className="size-3" />
                              Reject
                            </Button>
                          </div>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>

      <Dialog open={!!issued} onOpenChange={(o) => !o && setIssued(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>API key issued</DialogTitle>
            <DialogDescription>
              Customer created for{" "}
              <span className="font-medium text-foreground">{issued?.email}</span>.
              This key is shown <strong>only once</strong>. Copy and share
              out-of-band — the system does not store the plaintext.
            </DialogDescription>
          </DialogHeader>
          <div className="rounded-md bg-muted p-3 font-mono text-xs break-all">
            {issued?.result.api_key}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                if (issued) {
                  void navigator.clipboard.writeText(issued.result.api_key);
                  toast.success("Copied");
                }
              }}
            >
              <Copy className="size-3" />
              Copy
            </Button>
            <Button onClick={() => setIssued(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function StatusPill({ status }: { status: WaitlistEntry["status"] }) {
  if (status === "approved") return <Badge variant="default">approved</Badge>;
  if (status === "rejected") return <Badge variant="destructive">rejected</Badge>;
  return <Badge variant="secondary">pending</Badge>;
}
