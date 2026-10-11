import { useState } from "react";
import { Plus, ShieldCheck, ServerCog, Upload } from "lucide-react";
import { Logo } from "@/components/logo";
import { PageHeader } from "@/components/page-header";
import { ServerDialog } from "@/components/server-dialog";
import { Button } from "@/components/ui/button";

const features = [
  { icon: Upload, title: "SSH 上传", text: "任意 Linux 程序，拖进来就能发版" },
  { icon: ServerCog, title: "systemd 托管", text: "后台运行、开机自启、崩溃拉起" },
  { icon: ShieldCheck, title: "零侵入", text: "服务器上不安装任何常驻程序" },
];

export function Welcome() {
  const [adding, setAdding] = useState(false);
  return (
    <>
      <PageHeader>{null}</PageHeader>
      <div className="relative flex flex-1 flex-col items-center justify-center overflow-hidden p-8 text-center">
        {/* A soft glow of the palette behind the logo. */}
        <div
          aria-hidden
          className="pointer-events-none absolute top-1/2 left-1/2 size-[36rem] -translate-x-1/2 -translate-y-[60%] rounded-full opacity-60 blur-3xl"
          style={{ background: "radial-gradient(closest-side, color-mix(in oklab, var(--primary) 22%, transparent), transparent)" }}
        />
        <div className="relative flex flex-col items-center gap-6">
          <div className="bg-card shadow-raised rounded-[1.4rem] border p-3">
            <Logo className="size-16" />
          </div>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">把程序部署到你的服务器</h1>
            <p className="text-muted-foreground mx-auto mt-2 max-w-md text-sm leading-relaxed">
              通过 SSH 上传任意 Linux 程序，由服务器自带的 systemd 负责后台运行、开机自启和崩溃拉起。服务器上不安装任何东西。
            </p>
          </div>
          <Button size="lg" className="shadow-surface" onClick={() => setAdding(true)}>
            <Plus />
            添加服务器
          </Button>
          <div className="mt-4 grid max-w-2xl grid-cols-3 gap-3 text-left">
            {features.map((f) => (
              <div key={f.title} className="surface bg-card/70 p-4 backdrop-blur">
                <span className="bg-primary/10 text-primary mb-3 flex size-8 items-center justify-center rounded-lg">
                  <f.icon className="size-4" />
                </span>
                <div className="text-sm font-medium">{f.title}</div>
                <div className="text-muted-foreground mt-1 text-xs leading-relaxed">{f.text}</div>
              </div>
            ))}
          </div>
        </div>
      </div>
      <ServerDialog open={adding} onOpenChange={setAdding} />
    </>
  );
}
