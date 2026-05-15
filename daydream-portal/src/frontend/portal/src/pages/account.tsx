import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { useSession } from "@/hooks/useSession";

export function AccountPage() {
  const session = useSession();
  if (!session) return null;

  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle>Account</CardTitle>
        <CardDescription>
          Daydream-portal only knows about your active UI token. Your durable
          API key was shown once at issuance — if you've lost it, ask your
          operator to revoke and re-issue.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <Field label="Email" value={session.email} />
        <Separator />
        <Field label="Customer ID" value={session.customerId} mono />
        <Separator />
        <Field label="Display name" value={session.actor} />
      </CardContent>
    </Card>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-sm" : "text-sm"}>{value}</span>
    </div>
  );
}
