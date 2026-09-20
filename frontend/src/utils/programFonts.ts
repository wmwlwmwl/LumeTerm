import * as AppGo from '../../wailsjs/go/wailsapp/App.js'
import type { programfonts } from '../../wailsjs/go/models.ts'

const PROGRAM_FONT_STORAGE_KEYS = {
  ui: 'programFont.ui.fileName',
  terminal: 'programFont.terminal.fileName',
  ai: 'programFont.ai.fileName',
} as const;


const DEFAULT_PROGRAM_FONT_STACKS = {
  ui: "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
  terminal: "'JetBrains Mono', 'Microsoft YaHei', 'PingFang SC', 'Noto Sans CJK SC', 'Fira Code', monospace",
  ai: "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
} as const;

/** 程序字体分配（目标 → 字体文件名） */
export interface ProgramFontAssignments {
  uiFileName: string;
  terminalFileName: string;
  aiFileName: string;
}

/** 解析后的程序字体偏好（含最终 font-family） */
export interface ProgramFontPreferences extends ProgramFontAssignments {
  uiFontFamily: string;
  terminalFontFamily: string;
  aiFontFamily: string;
}

const loadedProgramFontFamilies = new Map<string, string>()

let cachedProgramFontPreferences: ProgramFontPreferences = {
  uiFileName: '',
  terminalFileName: '',
  aiFileName: '',
  uiFontFamily: DEFAULT_PROGRAM_FONT_STACKS.ui,
  terminalFontFamily: DEFAULT_PROGRAM_FONT_STACKS.terminal,
  aiFontFamily: DEFAULT_PROGRAM_FONT_STACKS.ai,
}

function getProgramFontStorageKey(target: string): string {
  if (target === 'ui' || target === 'terminal' || target === 'ai') {
    return PROGRAM_FONT_STORAGE_KEYS[target]
  }
  return ''
}

function createProgramFontFaceFamily(fileName: string): string {
  const normalizedName = String(fileName || '').trim().replace(/[^a-zA-Z0-9_-]+/g, '_')
  return `LumeTermProgramFont_${normalizedName || 'Custom'}_${Date.now().toString(36)}`
}

function getStoredProgramFontFileName(target: string): string {
  const storageKey = getProgramFontStorageKey(target)
  if (!storageKey) {
    return ''
  }
  const storedValue = localStorage.getItem(storageKey)
  return typeof storedValue === 'string' ? storedValue.trim() : ''
}

function setStoredProgramFontFileName(target: string, fileName: string): void {
  const storageKey = getProgramFontStorageKey(target)
  if (!storageKey) {
    return
  }
  const normalizedFileName = typeof fileName === 'string' ? fileName.trim() : ''
  if (normalizedFileName) {
    localStorage.setItem(storageKey, normalizedFileName)
    return
  }
  localStorage.removeItem(storageKey)
}

function invalidateLoadedProgramFont(fileName: unknown): void {
  const normalizedFileName = typeof fileName === 'string' ? fileName.trim() : ''
  if (!normalizedFileName) {
    return
  }
  loadedProgramFontFamilies.delete(normalizedFileName)
}

async function ensureProgramFontLoaded(fileName: string): Promise<string> {
  const normalizedFileName = typeof fileName === 'string' ? fileName.trim() : ''
  if (!normalizedFileName) {
    return ''
  }
  const existingFamily = loadedProgramFontFamilies.get(normalizedFileName)
  if (existingFamily) {
    return existingFamily
  }
  const dataUrl = await AppGo.GetProgramFontDataURL(normalizedFileName)
  if (!dataUrl || typeof dataUrl !== 'string') {
    return ''
  }
  const familyName = createProgramFontFaceFamily(normalizedFileName)
  const fontFace = new FontFace(familyName, `url("${dataUrl}")`)
  const loadedFontFace = await fontFace.load()
  document.fonts.add(loadedFontFace)
  loadedProgramFontFamilies.set(normalizedFileName, familyName)
  return familyName
}

function buildResolvedProgramFontFamily(fileName: string, fallbackFontFamily: string): string {
  const normalizedFileName = typeof fileName === 'string' ? fileName.trim() : ''
  const fallback = typeof fallbackFontFamily === 'string' && fallbackFontFamily.trim() ? fallbackFontFamily : 'sans-serif'
  const familyName = normalizedFileName ? loadedProgramFontFamilies.get(normalizedFileName) || '' : ''
  if (!familyName) {
    return fallback
  }
  return `"${familyName}", ${fallback}`
}

function normalizeProgramFontAssignments(preferences: Partial<ProgramFontAssignments> = {}): ProgramFontAssignments {
  return {
    uiFileName: typeof preferences.uiFileName === 'string' ? preferences.uiFileName.trim() : '',
    terminalFileName: typeof preferences.terminalFileName === 'string' ? preferences.terminalFileName.trim() : '',
    aiFileName: typeof preferences.aiFileName === 'string' ? preferences.aiFileName.trim() : '',
  }
}

export function getResolvedProgramFontPreferences(): ProgramFontPreferences {
  return { ...cachedProgramFontPreferences }
}

export async function applyProgramFontPreferences(): Promise<ProgramFontPreferences> {
  const uiFileName = getStoredProgramFontFileName('ui')
  const terminalFileName = getStoredProgramFontFileName('terminal')
  const aiFileName = getStoredProgramFontFileName('ai')
  const targetFileNames = [uiFileName, terminalFileName, aiFileName].filter(Boolean)
  await Promise.all(targetFileNames.map((fileName) => ensureProgramFontLoaded(fileName).catch(() => '')))
  const uiFontFamily = buildResolvedProgramFontFamily(uiFileName, DEFAULT_PROGRAM_FONT_STACKS.ui)
  const terminalFontFamily = buildResolvedProgramFontFamily(terminalFileName, DEFAULT_PROGRAM_FONT_STACKS.terminal)
  const aiFontFamily = buildResolvedProgramFontFamily(aiFileName, DEFAULT_PROGRAM_FONT_STACKS.ai)
  document.documentElement.style.setProperty('--font-ui', uiFontFamily)
  document.documentElement.style.setProperty('--font-terminal', terminalFontFamily)
  document.documentElement.style.setProperty('--font-ai-panel', aiFontFamily)
  cachedProgramFontPreferences = {
    uiFileName,
    terminalFileName,
    aiFileName,
    uiFontFamily,
    terminalFontFamily,
    aiFontFamily,
  }
  window.dispatchEvent(new CustomEvent<ProgramFontPreferences>('program-font-settings-changed', {
    detail: { ...cachedProgramFontPreferences },
  }))
  return getResolvedProgramFontPreferences()
}

export async function setProgramFontPreference(target: string, fileName: string): Promise<ProgramFontPreferences> {
  setStoredProgramFontFileName(target, fileName)
  return applyProgramFontPreferences()
}


export async function listProgramFonts(): Promise<programfonts.ProgramFontInfo[]> {
  const fonts = await AppGo.ListProgramFonts()
  return Array.isArray(fonts) ? fonts : []
}

async function importProgramFontFiles(filePaths: unknown): Promise<programfonts.ProgramFontInfo[]> {
  const normalizedPaths = Array.isArray(filePaths)
    ? filePaths.filter((item) => typeof item === 'string' && item.trim()).map((item) => item.trim())
    : []
  if (normalizedPaths.length === 0) {
    return []
  }
  const importedFonts = await AppGo.ImportProgramFontFiles(normalizedPaths)
  const resolvedFonts = Array.isArray(importedFonts) ? importedFonts : []
  resolvedFonts.forEach((font) => invalidateLoadedProgramFont(font?.fileName))
  await applyProgramFontPreferences().catch(() => {})
  return resolvedFonts
}

export async function selectAndImportProgramFontFiles(): Promise<programfonts.ProgramFontInfo[]> {
  const selectedPaths = await AppGo.SelectProgramFontFiles()
  return importProgramFontFiles(selectedPaths)
}

export async function deleteProgramFont(fileName: string): Promise<ProgramFontAssignments> {
  const normalizedFileName = typeof fileName === 'string' ? fileName.trim() : ''
  if (!normalizedFileName) {
    return getProgramFontAssignmentSnapshot()
  }
  await AppGo.DeleteProgramFont(normalizedFileName)
  invalidateLoadedProgramFont(normalizedFileName)
  const targets = ['ui', 'terminal', 'ai']
  for (const target of targets) {
    if (getStoredProgramFontFileName(target) === normalizedFileName) {
      setStoredProgramFontFileName(target, '')
    }
  }
  await applyProgramFontPreferences().catch(() => {})
  return getProgramFontAssignmentSnapshot()
}

export function getProgramFontAssignmentSnapshot(): ProgramFontAssignments {
  return normalizeProgramFontAssignments(getResolvedProgramFontPreferences())
}
