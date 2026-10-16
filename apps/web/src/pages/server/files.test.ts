import { describe, expect, it } from 'vitest'
import { TAG_BYTE, TAG_DOUBLE, TAG_INT, TAG_LONG } from '@/lib/nbt'
import { entryNameError, join, openable, parentOf } from './files'
import { parseScalar } from './nbt-view'

const file = (name: string, size = 100) => ({ name, size, type: 'file' as const })

describe('openable', () => {
  it('opens text by extension and small files with none', () => {
    expect(openable(file('server.properties'))).toBe('text')
    expect(openable(file('config/paper-global.yml'))).toBe('text')
    expect(openable(file('eula'))).toBe('text')
    expect(openable(file('LICENSE', 10 * 1024 * 1024))).toBeNull()
  })
  it('opens NBT as a tree and leaves binaries alone', () => {
    expect(openable(file('level.dat'))).toBe('nbt')
    expect(openable(file('level.dat_old'))).toBe('nbt')
    expect(openable(file('server.jar'))).toBeNull()
    expect(openable(file('r.0.0.mca'))).toBeNull()
    expect(openable({ name: 'world', size: 0, type: 'dir' })).toBeNull()
  })
  it('won’t load a huge log into the browser', () => {
    expect(openable(file('latest.log', 50 * 1024 * 1024))).toBeNull()
  })
})

describe('paths', () => {
  it('joins and walks up', () => {
    expect(join('/', 'plugins')).toBe('/plugins')
    expect(join('/plugins', 'a.jar')).toBe('/plugins/a.jar')
    expect(parentOf('/plugins/a.jar')).toBe('/plugins')
    expect(parentOf('/plugins')).toBe('/')
    expect(parentOf('/')).toBe('/')
  })
})

describe('entryNameError', () => {
  it('refuses empty, separators and names in use', () => {
    expect(entryNameError(' ', [])).not.toBeNull()
    expect(entryNameError('a/b', [])).not.toBeNull()
    expect(entryNameError('..', [])).not.toBeNull()
    expect(entryNameError('Config', ['config'])).not.toBeNull()
    expect(entryNameError('notes.txt', ['config'])).toBeNull()
  })
})

describe('parseScalar', () => {
  it('keeps numbers inside their tag’s range', () => {
    expect(parseScalar(TAG_BYTE, '127')).toBe(127)
    expect(parseScalar(TAG_BYTE, '128')).toBeNull()
    expect(parseScalar(TAG_INT, '-5')).toBe(-5)
    expect(parseScalar(TAG_INT, '1.5')).toBeNull()
    expect(parseScalar(TAG_DOUBLE, '0.25')).toBe(0.25)
    expect(parseScalar(TAG_DOUBLE, 'x')).toBeNull()
    expect(parseScalar(TAG_LONG, '9223372036854775807')).toBe(9223372036854775807n)
    expect(parseScalar(TAG_LONG, '9223372036854775808')).toBeNull()
  })
})
