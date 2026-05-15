import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { writeCreds } from "@/lib/creds";

export function LoginPage() {
  const [actor, setActor] = useState("");
  const [token, setToken] = useState("");
  const navigate = useNavigate();

  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle>Operator sign-in</CardTitle>
        <CardDescription>
          Token comes from <code>DAYDREAM_PORTAL_ADMIN_TOKENS</code> on the
          backend. Actor is recorded in the audit log for every admin action.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!actor.trim() || !token.trim()) {
              toast.error("Both actor and token are required.");
              return;
            }
            writeCreds({ actor: actor.trim(), token: token.trim() });
            toast.success(`Signed in as ${actor.trim()}`);
            navigate("/signups");
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="actor">Actor (your name)</Label>
            <Input
              id="actor"
              required
              value={actor}
              onChange={(e) => setActor(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="token">Admin token</Label>
            <Input
              id="token"
              type="password"
              required
              value={token}
              onChange={(e) => setToken(e.target.value)}
              className="font-mono"
            />
          </div>
          <Button type="submit">Sign in</Button>
        </form>
      </CardContent>
    </Card>
  );
}
