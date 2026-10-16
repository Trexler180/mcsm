import { describe, expect, it } from 'vitest'
import { emailError, newPassword } from './people'

describe('newPassword', () => {
  it('is long, varied and free of look-alike characters', () => {
    const a = newPassword()
    expect(a).toHaveLength(16)
    expect(a).not.toMatch(/[0O1lI]/)
    expect(newPassword()).not.toBe(a)
    expect(newPassword(24)).toHaveLength(24)
  })
})

describe('emailError', () => {
  it('wants something that looks like an address', () => {
    expect(emailError('')).not.toBeNull()
    expect(emailError('someone')).not.toBeNull()
    expect(emailError('someone@example.com')).toBeNull()
    expect(emailError(' someone@example.com ')).toBeNull()
  })
})
