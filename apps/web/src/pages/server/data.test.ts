import { beforeEach, describe, expect, it, vi } from 'vitest'

const readContent = vi.fn()
const ask = vi.fn()
vi.mock('@/lib/api', () => ({ api: { files: { readContent: (...a: unknown[]) => readContent(...a) } } }))
vi.mock('@/ui/store', () => ({ ask: (...a: unknown[]) => ask(...a) }))

const { okToOverwrite } = await import('./data')

describe('saving over a file someone else changed', () => {
  beforeEach(() => {
    readContent.mockReset()
    ask.mockReset()
  })

  it('saves without asking when the file is as it was opened', async () => {
    readContent.mockResolvedValue('a=1')
    expect(await okToOverwrite('s', '/c.yml', 'a=1', 'a=2')).toBe(true)
    expect(ask).not.toHaveBeenCalled()
  })

  it('asks when it changed, and keeps theirs on no', async () => {
    readContent.mockResolvedValue('a=1\nb=2')
    ask.mockResolvedValue(false)
    expect(await okToOverwrite('s', '/config/c.yml', 'a=1', 'a=3')).toBe(false)
    expect(ask).toHaveBeenCalledWith(expect.objectContaining({ title: 'c.yml changed since you opened it' }))
  })

  it('does not ask when it already says what would be saved', async () => {
    readContent.mockResolvedValue('a=3')
    expect(await okToOverwrite('s', '/c.yml', 'a=1', 'a=3')).toBe(true)
    expect(ask).not.toHaveBeenCalled()
  })
})
