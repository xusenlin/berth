import { Check, Loader2, Minus, TriangleAlert, X } from "lucide-react";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
import type { State } from "@/mygo";

/** StepEvent is the shape of deploy and backup progress events. */
export interface StepEvent {
  step: string;
  state: State;
  message?: string;
  progress?: number;
}

export interface StepView {
  step: string;
  state: State;
  message?: string;
  progress?: number;
  warnings: string[];
}

/** applyEvent folds a progress event into the list of steps. */
export function applyEvent(steps: StepView[], e: StepEvent): StepView[] {
  const i = steps.findIndex((s) => s.step === e.step);
  const prev = i >= 0 ? steps[i]! : { step: e.step, state: e.state, warnings: [] };
  const next: StepView =
    e.state === "warning"
      ? { ...prev, warnings: [...prev.warnings, e.message ?? ""] }
      : { ...prev, state: e.state, message: e.message ?? (e.state === "running" ? undefined : prev.message), progress: e.progress };
  return i >= 0 ? steps.map((s, j) => (j === i ? next : s)) : [...steps, next];
}

function StepIcon({ state }: { state: State }) {
  const c = "size-4 shrink-0";
  switch (state) {
    case "running":
      return <Loader2 className={cn(c, "text-muted-foreground animate-spin")} />;
    case "done":
      return <Check className={cn(c, "text-success")} />;
    case "failed":
      return <X className={cn(c, "text-destructive")} />;
    case "skipped":
      return <Minus className={cn(c, "text-muted-foreground")} />;
    default:
      return <TriangleAlert className={cn(c, "text-warning")} />;
  }
}

/** Steps lists the steps of a deploy, backup or restore as they run. */
export function Steps({ steps, labels }: { steps: StepView[]; labels: Record<string, string> }) {
  return (
    <ol className="space-y-1.5">
      {steps.map((s) => (
        <li key={s.step} className="text-sm">
          <div className="flex items-center gap-2">
            <StepIcon state={s.state} />
            <span className={cn("shrink-0", s.state === "skipped" && "text-muted-foreground")}>{labels[s.step] ?? s.step}</span>
            {s.message && <span className={cn("text-muted-foreground selectable truncate text-xs", s.state === "failed" && "text-destructive")}>{s.message}</span>}
          </div>
          {s.progress != null && s.state === "running" && (
            <div className="mt-1 ml-6 flex items-center gap-2">
              <Progress value={s.progress * 100} className="h-1.5 max-w-xs" />
              <span className="text-muted-foreground text-xs tabular-nums">{Math.round(s.progress * 100)}%</span>
            </div>
          )}
          {s.warnings.map((w, i) => (
            <div key={i} className="text-warning mt-0.5 ml-6 text-xs">
              {w}
            </div>
          ))}
        </li>
      ))}
    </ol>
  );
}
