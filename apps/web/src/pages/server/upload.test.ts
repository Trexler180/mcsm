import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type UploadProgress } from '@/lib/api'
import { droppedItems, pickedItems, topFolders, uploadName, uploadRun, type UploadItem, type UploadMeter, type UploadSender } from './upload'

const file = (name: string, size = 10) => new File(['x'.repeat(size)], name)

// The parts of the browser's file entries a walk touches.
const fileEntry = (name: string) => ({ name, isFile: true, file: (ok: (f: File) => void) => ok(file(name)) })
const dirEntry = (name: string, pages: unknown[][]) => ({
  name,
  isFile: false,
  createReader: () => {
    const left = [...pages, []]
    return { readEntries: (ok: (page: unknown[]) => void) => ok(left.shift() ?? []) }
  },
})
const entryItem = (entry: unknown) => ({ kind: 'file', webkitGetAsEntry: () => entry, getAsFile: () => null })
const dropOf = (...items: unknown[]) => ({ items: items as unknown as DataTransferItemList })
const shape = (items: UploadItem[]) => items.map((i) => (i.file ? `${i.dir}|${i.file.name}` : `${i.dir}|`))

describe('pickedItems', () => {
  // jsdom's File doesn't have webkitRelativePath; a browser's is '' for a plain file.
  const picked = (name: string, path: string) => Object.defineProperty(file(name), 'webkitRelativePath', { value: path })

  it('leaves plain files where they were dropped', () => {
    expect(shape(pickedItems([picked('a.toml', '')]))).toEqual(['|a.toml'])
  })
  it('keeps a picked folder’s shape', () => {
    expect(shape(pickedItems([picked('b.toml', 'config/mod/b.toml')]))).toEqual(['config/mod|b.toml'])
  })
})

describe('droppedItems', () => {
  it('walks folders, files beside them, and keeps empty ones', async () => {
    const tree = dirEntry('config', [[fileEntry('a.toml'), dirEntry('mod', [[fileEntry('b.toml')]]), dirEntry('empty', [])]])
    const items = await droppedItems(dropOf(entryItem(tree), entryItem(fileEntry('c.toml'))))
    expect(shape(items)).toEqual(['config|a.toml', 'config/mod|b.toml', 'config/empty|', '|c.toml'])
  })
  it('reads a big folder page by page', async () => {
    const page = (from: number) => Array.from({ length: 100 }, (_, i) => fileEntry(`${from + i}.toml`))
    const items = await droppedItems(dropOf(entryItem(dirEntry('big', [page(0), page(100), [fileEntry('last.toml')]]))))
    expect(items).toHaveLength(201)
  })
  it('takes the items before the drop event ends', async () => {
    const items = [entryItem(fileEntry('a.toml'))]
    const reading = droppedItems({ items: items as unknown as DataTransferItemList })
    items.length = 0 // what the browser does once the event returns
    expect(shape(await reading)).toEqual(['|a.toml'])
  })
  it('falls back to the file for something not on disk, and ignores text', async () => {
    const attachment = { kind: 'file', webkitGetAsEntry: () => null, getAsFile: () => file('mail.txt') }
    const text = { kind: 'string', webkitGetAsEntry: () => null, getAsFile: () => null }
    expect(shape(await droppedItems(dropOf(attachment, text)))).toEqual(['|mail.txt'])
  })
  it('fails when a folder can’t be read', async () => {
    const entry = { name: 'locked', isFile: false, createReader: () => ({ readEntries: (_: unknown, no: (e: Error) => void) => no(new Error('denied')) }) }
    await expect(droppedItems(dropOf(entryItem(entry)))).rejects.toThrow('denied')
  })
})

describe('naming', () => {
  const inside = (dir: string, name = 'a.toml') => ({ dir, file: file(name) })
  it('finds the folders at the top', () => {
    expect(topFolders([inside('config/mod'), inside('config'), inside('plugins'), inside('')])).toEqual(['config', 'plugins'])
  })
  it('calls an upload what a person would', () => {
    expect(uploadName([inside('', 'a.toml')])).toBe('a.toml')
    expect(uploadName([inside('config/mod'), { dir: 'config/empty' }])).toBe('config')
    expect(uploadName([inside(''), inside('')])).toBe('2 files')
    expect(uploadName([inside('config'), inside('')])).toBe('2 files')
    expect(uploadName([{ dir: 'a' }, { dir: 'b' }])).toBe('2 folders')
  })
})

describe('uploadRun', () => {
  afterEach(() => vi.useRealTimers())

  const sender = (onFile: UploadSender['file'] = async () => true) => ({
    file: vi.fn(onFile),
    folder: vi.fn<UploadSender['folder']>(async () => true),
  })
  const sentNames = (s: ReturnType<typeof sender>) => s.file.mock.calls.map(([, f]) => f.name).sort()
  const noMeter = () => {}

  it('sends each file to its folder and makes the empty ones', async () => {
    const s = sender()
    await uploadRun([{ dir: '', file: file('a') }, { dir: 'config/mod', file: file('b') }, { dir: 'config/empty' }], s, noMeter)()
    expect(s.file.mock.calls.map(([dir, f]) => [dir, f.name])).toEqual([['', 'a'], ['config/mod', 'b']])
    expect(s.folder.mock.calls).toEqual([['config/empty']])
  })

  it('sends only a few at once', async () => {
    let now = 0
    let most = 0
    const s = sender(async () => {
      most = Math.max(most, ++now)
      await new Promise((r) => setTimeout(r, 1))
      now--
    })
    await uploadRun(Array.from({ length: 20 }, (_, i) => ({ dir: '', file: file(`${i}`) })), s, noMeter)()
    expect(s.file).toHaveBeenCalledTimes(20)
    expect(most).toBe(4)
  })

  it('waits out the rate limiter and loses nothing', async () => {
    vi.useFakeTimers()
    let refusals = 3
    const s = sender(async () => {
      if (refusals-- > 0) throw new ApiError(429, 'rate limit exceeded')
    })
    const run = uploadRun([{ dir: '', file: file('a') }], s, noMeter)()
    await vi.runAllTimersAsync()
    await expect(run).resolves.toBeUndefined()
    expect(s.file).toHaveBeenCalledTimes(4)
  })

  it('carries on past a refused file, says so once, and retries only that file', async () => {
    let refuse = true
    const s = sender(async (_, f) => {
      if (refuse && f.name === 'big.dat') throw new ApiError(413, 'upload exceeds 512 MiB limit')
    })
    const items = [{ dir: 'config', file: file('big.dat') }, ...Array.from({ length: 9 }, (_, i) => ({ dir: 'config', file: file(`${i}`) }))]
    const run = uploadRun(items, s, noMeter)

    const err = await run().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).message).toBe('1 file didn’t make it. config/big.dat: Upload exceeds 512 MiB limit.')
    expect((err as ApiError).retryable).toBe(true)
    expect(s.file).toHaveBeenCalledTimes(10)

    refuse = false
    s.file.mockClear()
    await run()
    expect(sentNames(s)).toEqual(['big.dat'])
  })

  it('stops when the panel goes away, and a retry sends the rest', async () => {
    let down = true
    const s = sender(async () => {
      if (down) throw new TypeError('Network error during upload')
    })
    const run = uploadRun(Array.from({ length: 50 }, (_, i) => ({ dir: '', file: file(`${i}`) })), s, noMeter)

    await expect(run()).rejects.toBeInstanceOf(TypeError)
    expect(s.file.mock.calls.length).toBeLessThanOrEqual(4)

    down = false
    s.file.mockClear()
    await run()
    expect(s.file).toHaveBeenCalledTimes(50)
  })

  it('passes a lone file’s failure through as it is', async () => {
    const refused = new ApiError(403, 'the server’s files don’t allow that')
    const s = sender(async () => {
      throw refused
    })
    await expect(uploadRun([{ dir: '', file: file('a') }], s, noMeter)()).rejects.toBe(refused)
  })

  it('meters the files’ own bytes, not the multipart wrapping around them', async () => {
    const s = sender(async (_, f, onProgress: (p: UploadProgress) => void) => {
      onProgress({ loaded: 150, total: 300 }) // a small file in a big envelope
      expect(f.size).toBe(10)
    })
    const meters: UploadMeter[] = []
    await uploadRun([{ dir: '', file: file('a') }, { dir: '', file: file('b') }, { dir: 'empty' }], s, (m) => meters.push(m))()
    expect(meters[0]).toEqual({ loaded: 0, total: 20, count: 2, done: 0 })
    expect(Math.max(...meters.map((m) => m.loaded))).toBe(20)
    expect(meters[meters.length - 1]).toEqual({ loaded: 20, total: 20, count: 2, done: 2 })
  })

  it('takes back what a failed file had counted', async () => {
    const s = sender(async (_, __, onProgress) => {
      onProgress({ loaded: 5, total: 10 })
      throw new ApiError(500, 'disk full')
    })
    const meters: UploadMeter[] = []
    await uploadRun([{ dir: '', file: file('a') }], s, (m) => meters.push(m))().catch(() => {})
    expect(meters[meters.length - 1]).toEqual({ loaded: 0, total: 10, count: 1, done: 0 })
  })
})
