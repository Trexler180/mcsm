import { type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import {
  MoreVertical,
  Crown,
  Shield,
  ShieldOff,
  Ban,
  CircleCheck,
  UserX,
  Copy,
  Eye,
  Trash2,
} from 'lucide-react'
import { clsx } from 'clsx'
import { usePermission } from '@/lib/server-permissions'
import type { Player, PlayerActionKind, ServerPermission } from '@/lib/types'

interface PlayerActionsMenuProps {
  player: Player
  serverOnline: boolean
  busy?: boolean
  onAction: (kind: PlayerActionKind) => void
  onOpen: () => void
  onCopyUuid?: () => void
  // Permanently delete the player's saved data. Only rendered when supplied.
  onDelete?: () => void
  trigger?: ReactNode
}

// Each entry here is a separate permission (players.op, players.ban, …), so the
// menu resolves them one by one. Denied entries stay listed but inert, with the
// reason on hover and a "no access" tag — a menu that silently loses items
// reads as a different menu, not as a narrower grant.
function Item({
  onSelect,
  icon,
  children,
  destructive,
  disabled,
  hint,
  need,
}: {
  onSelect: () => void
  icon: ReactNode
  children: ReactNode
  destructive?: boolean
  disabled?: boolean
  hint?: string
  need?: ServerPermission
}) {
  const permission = usePermission(need ?? 'view')
  const denied = need !== undefined && !permission.allowed
  const inert = disabled || denied
  return (
    <DropdownMenu.Item
      disabled={inert}
      title={denied && !permission.pending ? permission.reason : undefined}
      onSelect={(e) => {
        // Keep the menu's selection from also triggering the row's click.
        e.preventDefault()
        if (!inert) onSelect()
      }}
      className={clsx(
        'flex items-center gap-2.5 rounded px-2 py-1.5 text-sm outline-none cursor-pointer select-none',
        'data-[highlighted]:bg-surface-2 data-[disabled]:opacity-40 data-[disabled]:cursor-not-allowed',
        destructive ? 'text-red-400' : 'text-text-primary',
      )}
    >
      <span className="flex-shrink-0">{icon}</span>
      <span className="flex-1">{children}</span>
      {denied && !permission.pending ? (
        <span className="text-[10px] text-text-secondary">no access</span>
      ) : (
        hint && <span className="text-[10px] text-text-secondary">{hint}</span>
      )}
    </DropdownMenu.Item>
  )
}

export function PlayerActionsMenu({
  player,
  serverOnline,
  busy,
  onAction,
  onOpen,
  onCopyUuid,
  onDelete,
  trigger,
}: PlayerActionsMenuProps) {
  const offlineHint = serverOnline ? undefined : 'offline edit'
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        {trigger ?? (
          <button
            type="button"
            disabled={busy}
            title="Actions"
            // Larger touch target on phones (~36px); compact on pointer-precise
            // larger screens.
            className="inline-flex h-9 w-9 flex-shrink-0 items-center justify-center rounded text-text-secondary hover:bg-surface-2 hover:text-text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50 sm:h-7 sm:w-7"
          >
            <MoreVertical className="h-4 w-4" />
          </button>
        )}
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={4}
          collisionPadding={8}
          // Never exceed the viewport on small/short screens; scroll internally.
          className="z-50 max-h-[var(--radix-dropdown-menu-content-available-height)] min-w-[11rem] max-w-[calc(100vw-1rem)] overflow-y-auto rounded-md border border-border bg-surface p-1 shadow-xl"
        >
          <Item
            onSelect={onOpen}
            need="players.inspect"
            icon={<Eye className="h-3.5 w-3.5" />}
          >
            View saved data
          </Item>

          <DropdownMenu.Separator className="my-1 h-px bg-border/60" />

          {player.op ? (
            <Item
              onSelect={() => onAction('deop')}
              need="players.op"
              icon={<Crown className="h-3.5 w-3.5 text-text-secondary" />}
              hint={offlineHint}
            >
              Remove operator
            </Item>
          ) : (
            <Item
              onSelect={() => onAction('op')}
              need="players.op"
              icon={<Crown className="h-3.5 w-3.5 text-yellow-400" />}
              hint={offlineHint}
            >
              Make operator
            </Item>
          )}

          {player.whitelisted ? (
            <Item
              onSelect={() => onAction('whitelist_remove')}
              need="players.whitelist"
              icon={<ShieldOff className="h-3.5 w-3.5 text-blue-400" />}
              hint={offlineHint}
            >
              Remove from whitelist
            </Item>
          ) : (
            <Item
              onSelect={() => onAction('whitelist_add')}
              need="players.whitelist"
              icon={<Shield className="h-3.5 w-3.5 text-blue-400" />}
              hint={offlineHint}
            >
              Add to whitelist
            </Item>
          )}

          {player.banned ? (
            <Item
              onSelect={() => onAction('pardon')}
              need="players.ban"
              icon={<CircleCheck className="h-3.5 w-3.5 text-green-400" />}
              hint={offlineHint}
            >
              Pardon (unban)
            </Item>
          ) : (
            <Item
              onSelect={() => onAction('ban')}
              need="players.ban"
              icon={<Ban className="h-3.5 w-3.5" />}
              destructive
              hint={offlineHint}
            >
              Ban player
            </Item>
          )}

          {player.online && (
            <Item
              onSelect={() => onAction('kick')}
              need="players.kick"
              icon={<UserX className="h-3.5 w-3.5" />}
              destructive
              disabled={!serverOnline}
            >
              Kick
            </Item>
          )}

          {player.uuid && onCopyUuid && (
            <>
              <DropdownMenu.Separator className="my-1 h-px bg-border/60" />
              <Item onSelect={onCopyUuid} icon={<Copy className="h-3.5 w-3.5" />}>
                Copy UUID
              </Item>
            </>
          )}

          {onDelete && (
            <>
              <DropdownMenu.Separator className="my-1 h-px bg-border/60" />
              <Item
                onSelect={onDelete}
                need="players.delete"
                icon={<Trash2 className="h-3.5 w-3.5" />}
                destructive
                // Files are named by UUID, and a running server would rewrite the
                // .dat on its next save — so only offer this for an identifiable
                // player while the server is stopped.
                disabled={!player.uuid || serverOnline}
                hint={serverOnline ? 'server offline only' : undefined}
              >
                Delete saved data
              </Item>
            </>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
