import { createContext, useContext, type ReactNode } from 'react'
import { can, deniedReason } from '@/lib/permissions'
import type { MyServerPermissions, ServerPermission } from '@/lib/types'

// Controls that need a permission (Btn's `need`, MenuItem's `need`) check it
// against the server whose page they are on. Outside a server there is no
// scope, and the API is the judge.

const PermContext = createContext<MyServerPermissions | null | undefined>(undefined)

/** Sets the permissions controls below check. Pass null while they load:
 *  nothing beyond viewing is allowed until they arrive. */
export function PermScope({ perms, children }: { perms: MyServerPermissions | null; children: ReactNode }) {
  return <PermContext.Provider value={perms}>{children}</PermContext.Provider>
}

export function useCan(): (need?: ServerPermission) => boolean {
  const perms = useContext(PermContext)
  return (need) => {
    if (!need || perms === undefined) return true
    return can(perms ?? undefined, need)
  }
}

export function deniedTip(need: ServerPermission): string {
  return deniedReason(need)
}
