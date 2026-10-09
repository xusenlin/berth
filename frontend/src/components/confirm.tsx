import { useState, type ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

/** Confirm asks before an action; with typeToConfirm, the user types it. */
export function Confirm({
  trigger,
  title,
  description,
  action = "确定",
  destructive,
  typeToConfirm,
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: ReactNode;
  action?: string;
  destructive?: boolean;
  typeToConfirm?: string;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState("");
  return (
    <AlertDialog onOpenChange={() => setTyped("")}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description && <AlertDialogDescription asChild><div>{description}</div></AlertDialogDescription>}
        </AlertDialogHeader>
        {typeToConfirm && (
          <div className="space-y-2 text-sm">
            <p>
              输入 <code className="bg-muted rounded px-1 font-mono">{typeToConfirm}</code> 确认：
            </p>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus />
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            disabled={!!typeToConfirm && typed !== typeToConfirm}
            className={destructive ? buttonVariants({ variant: "destructive" }) : undefined}
            onClick={onConfirm}
          >
            {action}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
