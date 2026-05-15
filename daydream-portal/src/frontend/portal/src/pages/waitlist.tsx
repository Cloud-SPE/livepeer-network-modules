import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { api, ApiError } from "@/lib/api";

export function WaitlistPage() {
  const [submittedEmail, setSubmittedEmail] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [reason, setReason] = useState("");

  const signup = useMutation({
    mutationFn: () =>
      api.signupWaitlist({
        email: email.trim().toLowerCase(),
        display_name: displayName.trim() || undefined,
        reason: reason.trim() || undefined,
      }),
    onSuccess: () => {
      setSubmittedEmail(email.trim().toLowerCase());
      toast.success("Request submitted. We'll review and reach out.");
    },
    onError: (err) =>
      toast.error(err instanceof ApiError ? err.message : "Submission failed"),
  });

  const status = useQuery({
    queryKey: ["waitlist-status", submittedEmail],
    queryFn: () => api.waitlistStatus(submittedEmail!),
    enabled: !!submittedEmail,
    refetchInterval: 15_000,
  });

  // Pause polling once we've reached a terminal status; no point in
  // hammering the endpoint when the answer won't change.
  useEffect(() => {
    if (status.data?.status === "approved" || status.data?.status === "rejected") {
      // no-op; we let the user navigate away themselves
    }
  }, [status.data?.status]);

  if (submittedEmail) {
    const s = status.data?.status ?? "pending";
    return (
      <Card className="max-w-xl">
        <CardHeader>
          <CardTitle>You're on the list</CardTitle>
          <CardDescription>
            Recorded <span className="font-mono">{submittedEmail}</span>
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Status:</span>
            <Badge
              variant={
                s === "approved" ? "default" : s === "rejected" ? "destructive" : "secondary"
              }
            >
              {s}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            An operator will review your request. On approval, you'll receive
            your API key out-of-band — keep an eye out.
          </p>
          <div className="flex gap-2">
            <Button asChild variant="outline">
              <Link to="/login">I have a key</Link>
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setSubmittedEmail(null);
                setEmail("");
                setDisplayName("");
                setReason("");
              }}
            >
              Submit another
            </Button>
          </div>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle>Request access</CardTitle>
        <CardDescription>
          Daydream-portal is invite-gated. Drop your email and an optional note;
          an operator will review and issue you an API key.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            signup.mutate();
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              type="email"
              required
              placeholder="you@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="display_name">Display name (optional)</Label>
            <Input
              id="display_name"
              placeholder="How should we address you?"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="reason">Why are you applying? (optional)</Label>
            <Input
              id="reason"
              placeholder="Briefly tell us what you want to build"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          <div className="flex gap-2 pt-2">
            <Button type="submit" disabled={signup.isPending}>
              {signup.isPending ? "Submitting…" : "Request access"}
            </Button>
            <Button asChild type="button" variant="ghost">
              <Link to="/login">I already have a key</Link>
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
