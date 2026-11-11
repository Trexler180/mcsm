import { ApiError, type UploadProgress } from '@/lib/api'
import { failureOf } from '@/ui/failure'
import { plural } from '@/ui/util'

// Uploading what someone picked or dropped, folders included. The agent makes
// whatever folder an upload names, so each file simply goes to its own folder
// and the shape rebuilds itself; only a folder with nothing in it needs asking
// for. One file is one request, which keeps a failure down to the file it was.

/** At once: quick for a folder of small configs, and leaves the browser room
 *  for the panel's own requests. */
const AT_ONCE = 4
/** The API's rate limiter asks for a second. A folder of small files can spend
 *  its whole burst, and that's a wait rather than a failure. */
const LIMITED_WAIT_MS = 1000
const LIMITED_TRIES = 30

export interface UploadItem {
  /** The folder it lands in, below the one being uploaded into. '' is that one. */
  dir: string
  /** None for a folder with nothing in it, which no file would bring about. */
  file?: File
}

export interface UploadMeter {
  loaded: number
  total: number
  /** Files in this run, and how many have landed. */
  count: number
  done: number
}

export interface UploadSender {
  file: (dir: string, file: File, onProgress: (p: UploadProgress) => void) => Promise<unknown>
  folder: (dir: string) => Promise<unknown>
}

/** From a file input. A folder's files carry their path from the folder down. */
export function pickedItems(files: File[]): UploadItem[] {
  return files.map((file) => ({ file, dir: file.webkitRelativePath.split('/').slice(0, -1).join('/') }))
}

/**
 * From a drop. `dataTransfer.files` lists a folder as one unreadable file, so
 * folders have to be walked through their entries.
 *
 * The items empty out when the drop event ends, so they're read before anything
 * is awaited. Something dragged that isn't on disk (an attachment from a mail
 * app) has no entry, only a file.
 */
export function droppedItems(data: Pick<DataTransfer, 'items'>): Promise<UploadItem[]> {
  const dropped = Array.from(data.items)
    .filter((i) => i.kind === 'file')
    .map((i) => i.webkitGetAsEntry() ?? i.getAsFile())
  return (async () => {
    const items: UploadItem[] = []
    for (const d of dropped) {
      if (d instanceof File) items.push({ file: d, dir: '' })
      else if (d) await walk(d, '', items)
    }
    return items
  })()
}

async function walk(entry: FileSystemEntry, dir: string, items: UploadItem[]) {
  if (entry.isFile) {
    const file = await new Promise<File>((ok, no) => (entry as FileSystemFileEntry).file(ok, no))
    items.push({ file, dir })
    return
  }
  const folder = dir ? `${dir}/${entry.name}` : entry.name
  const children = await readAll(entry as FileSystemDirectoryEntry)
  if (!children.length) items.push({ dir: folder })
  for (const child of children) await walk(child, folder, items)
}

/** A reader answers a page at a time, and only an empty page means the end. */
async function readAll(folder: FileSystemDirectoryEntry) {
  const reader = folder.createReader()
  const all: FileSystemEntry[] = []
  for (;;) {
    const page = await new Promise<FileSystemEntry[]>((ok, no) => reader.readEntries(ok, no))
    if (!page.length) return all
    all.push(...page)
  }
}

/** The folders at the top of what's being uploaded. */
export function topFolders(items: UploadItem[]): string[] {
  return [...new Set(items.filter((i) => i.dir).map((i) => i.dir.split('/')[0]))]
}

/** What a person would call it: the one thing's name, or how many. */
export function uploadName(items: UploadItem[]): string {
  const folders = topFolders(items)
  const loose = items.filter((i) => !i.dir)
  if (folders.length === 1 && !loose.length) return folders[0]
  if (!folders.length && loose.length === 1) return (loose[0].file as File).name
  const files = items.filter((i) => i.file).length
  return files ? plural(files, 'file') : plural(folders.length, 'folder')
}

const itemPath = (i: UploadItem) => (i.file ? (i.dir ? `${i.dir}/${i.file.name}` : i.file.name) : i.dir)

async function throughLimits<T>(send: () => Promise<T>): Promise<T> {
  for (let tries = 1; ; tries++) {
    try {
      return await send()
    } catch (e) {
      if (tries === LIMITED_TRIES || !(e instanceof ApiError && e.status === 429)) throw e
      await new Promise((r) => setTimeout(r, LIMITED_WAIT_MS))
    }
  }
}

/** One error for a whole run, so a folder can't raise a toast per file. */
function runError(failed: { item: UploadItem; error: unknown }[], size: number): unknown {
  const { item, error } = failed[0]
  const f = failureOf(error)
  // Not about any one file, so it reads the same however many were caught up in it.
  if (size === 1 || f.why !== 'refused') return error
  return new ApiError(0, `${plural(failed.length, 'file')} didn’t make it. ${itemPath(item)}: ${f.message}`, { retryable: true })
}

/**
 * An upload that can be picked up again: calling it sends what hasn't landed
 * yet, and throws if something still hasn't, so a Try again sends only that.
 *
 * A file the panel refuses doesn't stop the rest. Losing the panel or the
 * session does, since every file after it would fail the same way.
 */
export function uploadRun(items: UploadItem[], send: UploadSender, onMeter: (m: UploadMeter) => void) {
  let left = items
  return async () => {
    const todo = left
    const count = todo.filter((i) => i.file).length
    const total = todo.reduce((n, i) => n + (i.file?.size ?? 0), 0)
    let loaded = 0
    let done = 0
    const meter = () => onMeter({ loaded, total, count, done })
    meter()

    const failed: { item: UploadItem; error: unknown }[] = []
    let next = 0
    let stopped = false
    const worker = async () => {
      while (!stopped && next < todo.length) {
        const item = todo[next++]
        const { file, dir } = item
        let credited = 0
        // Progress counts the multipart wrapping too, which outweighs a small
        // config, so it's read as a share of the file.
        const credit = (bytes: number) => {
          loaded += bytes - credited
          credited = bytes
          meter()
        }
        try {
          if (file) {
            await throughLimits(() => send.file(dir, file, (p) => credit(p.total ? file.size * (p.loaded / p.total) : 0)))
            done++
            credit(file.size)
          } else {
            await throughLimits(() => send.folder(dir))
          }
        } catch (error) {
          credit(0)
          failed.push({ item, error })
          if (failureOf(error).why !== 'refused') stopped = true
        }
      }
    }
    await Promise.all(Array.from({ length: Math.min(AT_ONCE, todo.length) }, worker))

    left = [...failed.map((f) => f.item), ...todo.slice(next)]
    if (failed.length) throw runError(failed, todo.length)
  }
}
