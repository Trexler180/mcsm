import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FileEditor } from './editor'

const mocks = vi.hoisted(() => ({
  readContent: vi.fn(),
  writeContent: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: { files: { readContent: mocks.readContent, writeContent: mocks.writeContent } },
}))

vi.mock('@/store/notifications', () => ({
  useNotifications: () => ({ success: mocks.success, error: mocks.error }),
}))

vi.mock('@/components/ui/permission', () => ({
  PermissionButton: ({ need: _need, loading: _loading, children, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { need: string; loading?: boolean }) => (
    <button {...props}>{children}</button>
  ),
}))

vi.mock('codemirror', () => {
  class EditorView {
    static theme() {
      return {}
    }

    state: { doc: { toString: () => string } }
    private node: HTMLDivElement

    constructor({ state, parent }: { state: { doc: { toString: () => string } }; parent: HTMLElement }) {
      this.state = state
      this.node = document.createElement('div')
      this.node.textContent = state.doc.toString()
      parent.replaceChildren(this.node)
    }

    destroy() {
      this.node.remove()
    }
  }

  return { EditorView, basicSetup: {} }
})

vi.mock('@codemirror/state', () => ({
  EditorState: {
    create: ({ doc }: { doc: string }) => ({ doc: { toString: () => doc } }),
  },
}))
vi.mock('@codemirror/theme-one-dark', () => ({ oneDark: {} }))
vi.mock('@codemirror/lang-json', () => ({ json: () => ({}) }))
vi.mock('@codemirror/lang-yaml', () => ({ yaml: () => ({}) }))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('FileEditor', () => {
  it('ignores an older load that resolves after the selected path changes', async () => {
    const first = deferred<string>()
    const second = deferred<string>()
    mocks.readContent.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    mocks.writeContent.mockResolvedValue(undefined)

    const { rerender } = render(<FileEditor serverId="server-1" path="/first.txt" />)
    rerender(<FileEditor serverId="server-1" path="/second.txt" />)

    await act(async () => {
      second.resolve('second content')
      await second.promise
    })
    await waitFor(() => expect(screen.getByText('second content')).toBeInTheDocument())

    await act(async () => {
      first.resolve('stale first content')
      await first.promise
    })
    expect(screen.queryByText('stale first content')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => {
      expect(mocks.writeContent).toHaveBeenCalledWith('server-1', '/second.txt', 'second content')
    })
  })
})
