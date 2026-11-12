import { describe, expect, it, vi } from 'vitest'
import type { KeyboardEvent } from 'react'
import { rowPress } from './util'

const key = (k: string, onRow = true) => {
  const row = {}
  return { key: k, target: onRow ? row : {}, currentTarget: row, preventDefault: vi.fn() } as unknown as KeyboardEvent
}

describe('rowPress', () => {
  it('opens on Enter and Space as a click does', () => {
    const open = vi.fn()
    const props = rowPress(open, 'link')
    expect(props.role).toBe('link')
    expect(props.tabIndex).toBe(0)
    props.onClick?.()
    props.onKeyDown?.(key('Enter'))
    const space = key(' ')
    props.onKeyDown?.(space)
    expect(open).toHaveBeenCalledTimes(3)
    // Space would otherwise scroll the page.
    expect(space.preventDefault).toHaveBeenCalled()
  })

  it('leaves other keys, and keys pressed on a control inside the row, alone', () => {
    const open = vi.fn()
    const props = rowPress(open)
    props.onKeyDown?.(key('Tab'))
    props.onKeyDown?.(key('Enter', false))
    expect(open).not.toHaveBeenCalled()
  })

  it('adds nothing to a row that does not open', () => {
    expect(rowPress(undefined, 'link')).toEqual({})
  })
})
