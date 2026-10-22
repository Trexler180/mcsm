import { PageHead } from '@/ui/page'
import { AgentsSection } from './agents'
import { KeysSection } from './keys'
import { YouSection } from './you'

// Your own account: who you are and how you sign in, the access keys you've
// made, and the AI agents you've connected. Everything here belongs to you,
// whatever your role.

import type { AccountSection } from './sections'
export { accountSectionOf, type AccountSection } from './sections'

const SECTIONS: { value: AccountSection; label: string }[] = [
  { value: 'you', label: 'You' },
  { value: 'keys', label: 'Access keys' },
  { value: 'agents', label: 'AI agents' },
]


export function AccountPage({ section, setSection }: { section: AccountSection; setSection: (s: AccountSection) => void }) {
  return (
    <div className="scroll">
      <div className="page">
        <PageHead title="Your account" />
        <div className="with-subnav">
          <nav className="subnav" aria-label="Account">
            {SECTIONS.map((s) => (
              <button key={s.value} type="button" aria-current={section === s.value} onClick={() => setSection(s.value)}>
                {s.label}
              </button>
            ))}
          </nav>
          <div style={{ minWidth: 0 }}>
            {section === 'you' && <YouSection />}
            {section === 'keys' && <KeysSection />}
            {section === 'agents' && <AgentsSection />}
          </div>
        </div>
      </div>
    </div>
  )
}
