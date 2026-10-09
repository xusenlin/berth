import { toast } from "sonner";

export function message(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return String(err);
}

export function toastError(err: unknown, title?: string) {
  if (title) toast.error(title, { description: message(err) });
  else toast.error(message(err));
}
