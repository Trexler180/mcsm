import type { Server } from "@/lib/types";
import { ServerIconOptionsPanel } from "./icon-panel";
import { ResourcePackOptionsPanel } from "./resource-pack-panel";
import { PanelOptionsPanel } from "./panel-options-panel";


export function OptionsTab({ server }: { server: Server }) {
  return (
    <div className="max-w-3xl space-y-5">
      <ServerIconOptionsPanel server={server} />
      <ResourcePackOptionsPanel server={server} />
      <PanelOptionsPanel server={server} />
    </div>
  );
}
