import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, ApiError } from "@/lib/api";
import { writeSession } from "@/lib/session";

export function LoginPage() {
  const [apiKey, setApiKey] = useState("");
  const [actor, setActor] = useState("");
  const navigate = useNavigate();

  const login = useMutation({
    mutationFn: () =>
      api.loginByKey({ api_key: apiKey.trim(), actor: actor.trim() }),
    onSuccess: (res) => {
      writeSession({
        token: res.auth_token,
        actor: actor.trim(),
        customerId: res.customer.id,
        email: res.customer.email,
      });
      toast.success(`Signed in as ${res.customer.email}`);
      navigate("/playground");
    },
    onError: (err) =>
      toast.error(err instanceof ApiError ? err.message : "Sign-in failed"),
  });

  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle>Sign in</CardTitle>
        <CardDescription>
          Paste the API key your operator issued you.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            login.mutate();
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="actor">Display name</Label>
            <Input
              id="actor"
              required
              placeholder="What should we call you?"
              value={actor}
              onChange={(e) => setActor(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="api_key">API key</Label>
            <Input
              id="api_key"
              type="password"
              required
              placeholder="sk-live-…"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              className="font-mono"
            />
          </div>
          <Button type="submit" disabled={login.isPending}>
            {login.isPending ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
