import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '@/lib/api'
import type { Server } from '@/lib/types'
import { toast } from '@/ui/toasts'
import { WorldUploadDialog } from './worlds'

vi.mock('@/ui/toasts', () => ({ toast: vi.fn() }))

const server = { id: 'srv-1', name: 'Survival', port: 25565 } as Server

function renderDialog() {
  const onClose = vi.fn()
  const onDone = vi.fn()
  const { container } = render(
    <WorldUploadDialog server={server} live={false} worlds={['world']} folders={['world']} loaded="world" onClose={onClose} onDone={onDone} />,
  )
  const input = container.ownerDocument.querySelector('input[type=file]') as HTMLInputElement
  fireEvent.change(input, { target: { files: [new File(['zip'], 'Island.zip', { type: 'application/zip' })] } })
  fireEvent.click(screen.getByRole('button', { name: 'Upload' }))
  return { onClose, onDone }
}

describe('WorldUploadDialog', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.mocked(toast).mockReset()
  })

  it('reports an installed world as uploaded when it cannot be set to load', async () => {
    vi.spyOn(api.worlds, 'upload').mockResolvedValue({ name: 'Island', replaced: false, files: 3, bytes: 300 } as never)
    vi.spyOn(api.files, 'readContent').mockResolvedValue('level-name=world\n')
    vi.spyOn(api.files, 'writeContent').mockRejectedValue(new ApiError(502, 'the node could not be reached'))

    const { onClose, onDone } = renderDialog()

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(onDone).toHaveBeenCalledWith('Island')
    expect(toast).toHaveBeenCalledWith('bad', 'Uploaded Island, but it isn’t set to load', expect.stringContaining('Load'))
    expect(toast).not.toHaveBeenCalledWith('bad', expect.stringContaining('Couldn’t upload'), expect.anything())
  })

  it('keeps the dialog open when the upload itself fails', async () => {
    vi.spyOn(api.worlds, 'upload').mockRejectedValue(new ApiError(400, 'no level.dat in the archive'))

    const { onClose, onDone } = renderDialog()

    await waitFor(() => expect(toast).toHaveBeenCalledWith('bad', 'Couldn’t upload Island', expect.stringMatching(/level.dat/i)))
    expect(onClose).not.toHaveBeenCalled()
    expect(onDone).not.toHaveBeenCalled()
  })
})
