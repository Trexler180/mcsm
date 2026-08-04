import { forwardRef, type ReactNode } from "react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Lock } from "lucide-react";
import { clsx } from "clsx";
import { Button } from "./button";
import { usePermission } from "@/lib/server-permissions";
import { useNotifications } from "@/store/notifications";
import type { ServerPermission } from "@/lib/types";

// Controls the user isn't allowed to use stay on screen but go inert, and say
// why. Hiding them reads as "this panel has no such feature", which is a
// different and more confusing claim than "you can't do this one" — and it
// leaves someone with a narrow grant unable to tell a missing feature from a
// missing permission.

/** Wraps an inert control so hover/focus explains the denial, and a tap says it
 *  out loud — tooltips never open on touch, which is where this panel is most
 *  used. */
function DeniedShell({
  reason,
  className,
  children,
}: {
  reason: string;
  className?: string;
  children: ReactNode;
}) {
  const { warning } = useNotifications();
  return (
    <Tooltip.Root delayDuration={120}>
      <Tooltip.Trigger asChild>
        {/* The disabled control below swallows nothing — a disabled button
            emits no pointer events, so this span is what hover, focus and tap
            actually land on. */}
        <span
          tabIndex={0}
          role="button"
          aria-disabled="true"
          onClick={() => warning("Permission required", reason)}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              warning("Permission required", reason);
            }
          }}
          className={clsx(
            "inline-flex cursor-not-allowed rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent",
            className,
          )}
        >
          {children}
        </span>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          side="top"
          sideOffset={6}
          collisionPadding={8}
          className="z-50 max-w-[16rem] rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-text-secondary shadow-xl"
        >
          <span className="flex items-start gap-1.5">
            <Lock className="mt-px h-3 w-3 flex-shrink-0" />
            <span>{reason}</span>
          </span>
          <Tooltip.Arrow className="fill-border" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

type ButtonProps = React.ComponentPropsWithoutRef<typeof Button>;

/** A Button that goes inert, with an explanation, unless the surrounding server
 *  grants `need`. Otherwise identical to Button. */
export const PermissionButton = forwardRef<
  HTMLButtonElement,
  ButtonProps & { need: ServerPermission | ServerPermission[]; wrapperClassName?: string }
>(({ need, wrapperClassName, ...props }, ref) => {
  const { allowed, pending, reason } = usePermission(need);
  if (allowed) return <Button ref={ref} {...props} />;

  const inert = (
    <Button
      ref={ref}
      {...props}
      disabled
      loading={false}
      // The wrapper owns the interaction now; leaving these on would fight it.
      onClick={undefined}
      title={undefined}
      className={clsx(props.className, "pointer-events-none")}
    />
  );
  // Still loading: inert, but making a claim about permissions we haven't
  // fetched would be a coin flip.
  if (pending) return inert;
  return (
    <DeniedShell reason={reason} className={wrapperClassName}>
      {inert}
    </DeniedShell>
  );
});
PermissionButton.displayName = "PermissionButton";

/** Gates a control this component can't construct itself — a raw <button>, a
 *  Select, a file-input label. Renders `children` untouched when allowed, and
 *  otherwise wraps them dimmed and click-proof with the same explanation. */
export function PermissionGate({
  need,
  children,
  className,
}: {
  need: ServerPermission | ServerPermission[];
  children: ReactNode;
  className?: string;
}) {
  const { allowed, pending, reason } = usePermission(need);
  if (allowed) return <>{children}</>;

  const inert = (
    <span className="pointer-events-none block opacity-50">{children}</span>
  );
  if (pending) return inert;
  return (
    <DeniedShell reason={reason} className={className}>
      {inert}
    </DeniedShell>
  );
}

/** Renders `children` only when the permission is held. For things a denial
 *  message can't usefully attach to — a whole toolbar, a bulk-select column —
 *  where an inert placeholder would be noise rather than information. */
export function IfPermitted({
  need,
  children,
}: {
  need: ServerPermission | ServerPermission[];
  children: ReactNode;
}) {
  const { allowed } = usePermission(need);
  return allowed ? <>{children}</> : null;
}
