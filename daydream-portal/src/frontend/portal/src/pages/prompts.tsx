import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Pencil, Plus, Trash2 } from "lucide-react";
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
import { Separator } from "@/components/ui/separator";
import { api, ApiError, type SavedPrompt } from "@/lib/api";

interface EditingState {
  id: string | "new";
  label: string;
  body: string;
}

export function PromptsPage() {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<EditingState | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["prompts"],
    queryFn: () => api.listPrompts(),
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: ["prompts"] });

  const create = useMutation({
    mutationFn: (input: { label: string; body: string }) => api.createPrompt(input),
    onSuccess: () => {
      toast.success("Prompt saved");
      setEditing(null);
      invalidate();
    },
    onError: (err) => toast.error(err instanceof ApiError ? err.message : "Save failed"),
  });
  const update = useMutation({
    mutationFn: ({ id, input }: { id: string; input: { label: string; body: string } }) =>
      api.updatePrompt(id, input),
    onSuccess: () => {
      toast.success("Prompt updated");
      setEditing(null);
      invalidate();
    },
    onError: (err) => toast.error(err instanceof ApiError ? err.message : "Update failed"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.deletePrompt(id),
    onSuccess: () => {
      toast.success("Deleted");
      invalidate();
    },
    onError: (err) => toast.error(err instanceof ApiError ? err.message : "Delete failed"),
  });

  if (editing) {
    return (
      <Card className="max-w-2xl">
        <CardHeader>
          <CardTitle>{editing.id === "new" ? "New prompt" : "Edit prompt"}</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              const label = editing.label.trim();
              const body = editing.body.trim();
              if (!label || !body) {
                toast.error("Both label and body are required");
                return;
              }
              if (editing.id === "new") {
                create.mutate({ label, body });
              } else {
                update.mutate({ id: editing.id, input: { label, body } });
              }
            }}
          >
            <div className="flex flex-col gap-2">
              <Label htmlFor="label">Label</Label>
              <Input
                id="label"
                value={editing.label}
                onChange={(e) => setEditing({ ...editing, label: e.target.value })}
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="body">Body</Label>
              <textarea
                id="body"
                rows={8}
                className="border-input bg-transparent dark:bg-input/30 placeholder:text-muted-foreground rounded-md border px-3 py-2 text-sm font-mono shadow-xs outline-none focus-visible:ring-ring/50 focus-visible:ring-[3px]"
                value={editing.body}
                onChange={(e) => setEditing({ ...editing, body: e.target.value })}
              />
            </div>
            <div className="flex gap-2 pt-2">
              <Button type="submit" disabled={create.isPending || update.isPending}>
                Save
              </Button>
              <Button type="button" variant="ghost" onClick={() => setEditing(null)}>
                Cancel
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    );
  }

  const prompts = data?.prompts ?? [];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Saved prompts</h1>
          <p className="text-sm text-muted-foreground">
            Personal scratchpad. Stays on your account.
          </p>
        </div>
        <Button onClick={() => setEditing({ id: "new", label: "", body: "" })}>
          <Plus className="size-4" />
          New prompt
        </Button>
      </div>

      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {!isLoading && prompts.length === 0 && (
        <Card>
          <CardContent className="py-12 flex flex-col items-center gap-2 text-center">
            <p className="text-sm text-muted-foreground">No saved prompts yet.</p>
          </CardContent>
        </Card>
      )}

      <div className="flex flex-col gap-3">
        {prompts.map((p) => (
          <PromptRow
            key={p.id}
            prompt={p}
            onEdit={() => setEditing({ id: p.id, label: p.label, body: p.body })}
            onDelete={() => {
              if (window.confirm("Delete this prompt?")) remove.mutate(p.id);
            }}
          />
        ))}
      </div>
    </div>
  );
}

function PromptRow({
  prompt,
  onEdit,
  onDelete,
}: {
  prompt: SavedPrompt;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center justify-between">
          <span>{prompt.label}</span>
          <div className="flex gap-1">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                void navigator.clipboard.writeText(prompt.body);
                toast.success("Copied to clipboard");
              }}
            >
              <Copy className="size-3" />
              Copy
            </Button>
            <Button size="sm" variant="ghost" onClick={onEdit}>
              <Pencil className="size-3" />
              Edit
            </Button>
            <Button size="sm" variant="ghost" onClick={onDelete}>
              <Trash2 className="size-3" />
              Delete
            </Button>
          </div>
        </CardTitle>
      </CardHeader>
      <Separator />
      <CardContent>
        <pre className="text-xs font-mono text-muted-foreground whitespace-pre-wrap">
          {prompt.body}
        </pre>
      </CardContent>
    </Card>
  );
}
