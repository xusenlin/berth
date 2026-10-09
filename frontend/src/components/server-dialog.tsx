import { useEffect, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CircleAlert, FolderOpen, KeyRound, Loader2, ShieldCheck, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { message } from "@/lib/errors";
import { bytes } from "@/lib/format";
import { useNav } from "@/lib/nav";
import { keys } from "@/lib/queries";
import { Servers, type AuthType, type Server, type ServerView, type TestResult } from "@/mygo";

const empty: Server = {
  id: "",
  name: "",
  host: "",
  port: 22,
  user: "root",
  auth: "password",
  keyPath: "~/.ssh/id_ed25519",
  hostKey: "",
  portRange: [18000, 18999],
};

function Field({ label, hint, children, className }: { label: string; hint?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={className}>
      <Label className="mb-1.5">{label}</Label>
      {children}
      {hint && <p className="text-muted-foreground mt-1 text-xs">{hint}</p>}
    </div>
  );
}

/** ServerDialog adds a server, or edits one when given. */
export function ServerDialog({ open, onOpenChange, edit }: { open: boolean; onOpenChange: (o: boolean) => void; edit?: ServerView }) {
  const qc = useQueryClient();
  const { go } = useNav();
  const [form, setForm] = useState<Server>(empty);
  const [secret, setSecret] = useState("");
  const [test, setTest] = useState<TestResult | null>(null);
  const [testError, setTestError] = useState("");
  const [busy, setBusy] = useState<"" | "test" | "save">("");

  useEffect(() => {
    if (!open) return;
    setForm(edit ? { ...edit.server } : empty);
    setSecret("");
    setTest(null);
    setTestError("");
  }, [open, edit]);

  // Changing how to connect invalidates the test.
  const set = <K extends keyof Server>(k: K, v: Server[K]) => {
    setForm((f) => ({ ...f, [k]: v }));
    if (k !== "name" && k !== "portRange") {
      setTest(null);
      setTestError("");
    }
  };

  const runTest = async () => {
    setBusy("test");
    setTest(null);
    setTestError("");
    try {
      setTest(await Servers.test({ server: form, secret }));
    } catch (e) {
      setTestError(message(e));
    } finally {
      setBusy("");
    }
  };

  const save = async () => {
    if (!test) return;
    setBusy("save");
    try {
      const v = await Servers.save({ server: { ...form, hostKey: test.fingerprint }, secret });
      await qc.invalidateQueries({ queryKey: keys.servers });
      qc.removeQueries({ queryKey: keys.connect(v.server.id) });
      toast.success(edit ? "已保存" : `已添加 ${v.server.name}`);
      onOpenChange(false);
      go({ server: v.server.id });
    } catch (e) {
      toast.error(message(e));
    } finally {
      setBusy("");
    }
  };

  const secretLabel = form.auth === "password" ? "密码" : "私钥密码";
  const secretHint = edit?.hasSecret ? "留空表示不修改已保存的值" : form.auth === "key" ? "私钥未加密时留空" : undefined;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{edit ? "编辑服务器" : "添加服务器"}</DialogTitle>
          <DialogDescription>Berth 通过 SSH 连接服务器，服务器上无需安装任何程序。密码保存在系统钥匙串中。</DialogDescription>
        </DialogHeader>

        <div className="grid grid-cols-6 gap-3">
          <Field label="名称" className="col-span-6">
            <Input value={form.name} placeholder="如：阿里云-1（默认为主机地址）" onChange={(e) => set("name", e.target.value)} />
          </Field>
          <Field label="主机" className="col-span-4">
            <Input value={form.host} placeholder="1.2.3.4 或域名" onChange={(e) => set("host", e.target.value)} autoFocus={!edit} />
          </Field>
          <Field label="SSH 端口" className="col-span-2">
            <Input type="number" value={form.port} onChange={(e) => set("port", Number(e.target.value))} />
          </Field>
          <Field label="用户名" className="col-span-6">
            <Input value={form.user} onChange={(e) => set("user", e.target.value)} />
          </Field>
          <Field label="认证方式" className="col-span-6">
            <RadioGroup className="flex gap-5" value={form.auth} onValueChange={(v) => set("auth", v as AuthType)}>
              {(
                [
                  ["password", "密码"],
                  ["key", "私钥文件"],
                  ["agent", "ssh-agent"],
                ] as const
              ).map(([v, l]) => (
                <label key={v} className="flex items-center gap-2 text-sm">
                  <RadioGroupItem value={v} />
                  {l}
                </label>
              ))}
            </RadioGroup>
          </Field>
          {form.auth === "key" && (
            <Field label="私钥文件" className="col-span-6">
              <div className="flex gap-2">
                <Input value={form.keyPath} onChange={(e) => set("keyPath", e.target.value)} />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  title="选择文件"
                  onClick={async () => {
                    const p = await Servers.pickKeyFile();
                    if (p) set("keyPath", p);
                  }}
                >
                  <FolderOpen />
                </Button>
              </div>
            </Field>
          )}
          {form.auth !== "agent" && (
            <Field label={secretLabel} hint={secretHint} className="col-span-6">
              <Input type="password" value={secret} onChange={(e) => {
                setSecret(e.target.value);
                setTest(null);
              }} placeholder={edit?.hasSecret ? "••••••••" : ""} />
            </Field>
          )}
          <Field label="应用端口区间" hint="新建应用时从这里自动分配端口" className="col-span-6">
            <div className="flex items-center gap-2">
              <Input type="number" value={form.portRange[0]} onChange={(e) => set("portRange", [Number(e.target.value), form.portRange[1] ?? 0])} />
              <span className="text-muted-foreground">–</span>
              <Input type="number" value={form.portRange[1]} onChange={(e) => set("portRange", [form.portRange[0] ?? 0, Number(e.target.value)])} />
            </div>
          </Field>
        </div>

        {form.user === "root" && form.auth === "password" && (
          <p className="text-muted-foreground flex items-start gap-1.5 text-xs">
            <CircleAlert className="mt-0.5 size-3.5 shrink-0" />
            root 密码登录权限过大，建议改用普通用户加密钥。普通用户模式下应用运行在用户级 systemd 中。
          </p>
        )}

        {testError && (
          <Alert variant="destructive">
            <TriangleAlert />
            <AlertTitle>连接失败</AlertTitle>
            <AlertDescription className="selectable break-all">{testError}</AlertDescription>
          </Alert>
        )}
        {test && <TestSummary test={test} />}

        <DialogFooter>
          <Button variant="outline" onClick={runTest} disabled={!!busy || !form.host || !form.user}>
            {busy === "test" && <Loader2 className="animate-spin" />}
            测试连接
          </Button>
          <Button onClick={save} disabled={!test || !!busy}>
            {busy === "save" && <Loader2 className="animate-spin" />}
            {test?.changed ? "信任新密钥并保存" : "确认并保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TestSummary({ test }: { test: TestResult }) {
  const e = test.env;
  return (
    <div className="space-y-3">
      {test.changed ? (
        <Alert variant="destructive">
          <TriangleAlert />
          <AlertTitle>主机密钥已变化</AlertTitle>
          <AlertDescription>
            如果服务器没有重装过，这可能是中间人攻击。确认无误后才保存。
          </AlertDescription>
        </Alert>
      ) : null}
      <div className="bg-muted/50 rounded-lg border p-3 text-sm">
        <div className="mb-2 flex items-center gap-2 font-medium">
          <ShieldCheck className="text-success size-4" />
          连接成功，请核对主机密钥指纹
        </div>
        <div className="flex items-center gap-2 text-xs">
          <KeyRound className="text-muted-foreground size-3.5" />
          <span className="text-muted-foreground shrink-0 whitespace-nowrap">{test.keyType}</span>
          <code className="selectable font-mono break-all">{test.fingerprint}</code>
        </div>
        <div className="text-muted-foreground mt-3 grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
          <span>系统：{e.os || "未知"}</span>
          <span>架构：{e.arch}</span>
          <span>运行模式：{e.mode === "root" ? "root（系统服务）" : `普通用户 ${e.user}（用户服务）`}</span>
          <span>systemd：{e.systemd ? e.systemdVersion.replace(/ \(.*/, "") : "未检测到"}</span>
          <span>剩余空间：{bytes(e.diskFree)}</span>
          {e.mode === "user" && <span>linger：{e.linger ? "已开启" : "未开启（开机不会自启）"}</span>}
        </div>
        {!e.systemd && <p className="text-destructive mt-2 text-xs">这台服务器没有运行 systemd，Berth 无法管理其上的应用。</p>}
      </div>
    </div>
  );
}
