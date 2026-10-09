import { useState } from "react";
import { Plus } from "lucide-react";
import { Logo } from "@/components/logo";
import { PageHeader } from "@/components/page-header";
import { ServerDialog } from "@/components/server-dialog";
import { Button } from "@/components/ui/button";

export function Welcome() {
  const [adding, setAdding] = useState(false);
  return (
    <>
      <PageHeader>{null}</PageHeader>
      <div className="flex flex-1 flex-col items-center justify-center gap-4 p-8 text-center">
        <Logo className="size-16 drop-shadow-md" />
        <div>
          <h1 className="text-xl font-semibold">把程序部署到你的服务器</h1>
          <p className="text-muted-foreground mt-2 max-w-md text-sm">
            通过 SSH 上传任意 Linux 程序，由服务器自带的 systemd 负责后台运行、开机自启和崩溃拉起。服务器上不安装任何东西。
          </p>
        </div>
        <Button onClick={() => setAdding(true)}>
          <Plus />
          添加服务器
        </Button>
      </div>
      <ServerDialog open={adding} onOpenChange={setAdding} />
    </>
  );
}
