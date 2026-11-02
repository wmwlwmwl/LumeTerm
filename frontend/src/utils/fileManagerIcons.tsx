import {
  Folder, File, FileText, FileCode, FileArchive, Settings, ClipboardList, Wrench,
  Image, Code, Globe, Palette, Database, Terminal, Film, Music, BookOpen,
  FolderSymlink, FileSymlink, KeyRound, FileSpreadsheet, Presentation, Type, Binary,
  type LucideIcon,
} from 'lucide-react';

// 文件图标（颜色统一走 CSS 变量，浅/深色主题一致切换）
export const ICON_SIZE = 16;
export function fileIcon(name: unknown, isDir: boolean, isSymlink = false) {
  if (isDir) {
    return (
      <span className={`file-icon-themed file-icon-folder${isSymlink ? ' file-icon-symlink' : ''}`}>
        {isSymlink ? <FolderSymlink size={ICON_SIZE} /> : <Folder size={ICON_SIZE} />}
      </span>
    );
  }
  if (isSymlink) {
    return (
      <span className="file-icon-themed file-icon-default file-icon-symlink">
        <FileSymlink size={ICON_SIZE} />
      </span>
    );
  }
  const lowerName = String(name || '').toLowerCase();
  let ext = (lowerName.split('.').pop() || '').toLowerCase();
  if (lowerName === 'dockerfile' || lowerName.startsWith('dockerfile.')) ext = 'dockerfile';
  if (lowerName === 'makefile') ext = 'makefile';
  if (lowerName === 'cmakelists.txt') ext = 'cmake';
  if (lowerName === 'nginx.conf') ext = 'nginx';
  // SSH 场景高频：密钥与 known_hosts
  if (lowerName === 'authorized_keys' || lowerName === 'known_hosts') ext = 'key';
  if (/^id_(rsa|dsa|ecdsa|ed25519)([._-]|$)/.test(lowerName)) ext = 'key';

  const iconMap: Record<string, LucideIcon> = {
    js: Code, jsx: Code, mjs: Code, cjs: Code, ts: Code, tsx: Code, vue: Code,
    py: Terminal, pyw: Terminal, pyi: Terminal, rb: Code, lua: Code, go: Code, rs: Code, java: Code,
    c: Code, cc: Code, cpp: Code, cxx: Code, h: Code, hpp: Code, hh: Code, hxx: Code, cs: Code,
    kt: Code, kts: Code, swift: Code, scala: Code, dart: Code, zig: Code, nim: Code,
    ex: Code, exs: Code, erl: Code, hrl: Code, hs: Code, clj: Code, r: Code, jl: Code, fs: Code,
    gradle: Code, tf: Code, tfvars: Code, hcl: Code, ipynb: FileCode,
    html: Globe, htm: Globe, css: Palette, scss: Palette, less: Palette,
    json: Settings, yaml: Settings, yml: Settings, toml: Settings, ini: Settings, env: Settings, cfg: Settings, conf: Settings,
    properties: Settings, config: Settings, gitignore: Settings, gitattributes: Settings, gitmodules: Settings,
    gitconfig: Settings, editorconfig: Settings, service: Settings, socket: Settings, timer: Settings,
    md: FileText, txt: File, log: ClipboardList,
    png: Image, jpg: Image, jpeg: Image, gif: Image, svg: Image, webp: Image,
    ico: Image, bmp: Image, tif: Image, tiff: Image, heic: Image, heif: Image, avif: Image, psd: Image, xcf: Image,
    // 压缩/打包：tar.xz 取后段 xz，故按末段后缀逐一覆盖
    zip: FileArchive, tar: FileArchive, gz: FileArchive, rar: FileArchive, '7z': FileArchive, tgz: FileArchive, bz2: FileArchive,
    xz: FileArchive, txz: FileArchive, zst: FileArchive, zstd: FileArchive, tzst: FileArchive,
    lzma: FileArchive, lz: FileArchive, br: FileArchive, lzo: FileArchive,
    tbz: FileArchive, tbz2: FileArchive, taz: FileArchive, cab: FileArchive,
    deb: FileArchive, rpm: FileArchive, iso: FileArchive, dmg: FileArchive, img: FileArchive,
    pem: KeyRound, key: KeyRound, crt: KeyRound, cer: KeyRound, csr: KeyRound,
    p12: KeyRound, pfx: KeyRound, pub: KeyRound, gpg: KeyRound, asc: KeyRound,
    kdbx: KeyRound, ppk: KeyRound,
    doc: FileText, docx: FileText, odt: FileText, rtf: FileText,
    xls: FileSpreadsheet, xlsx: FileSpreadsheet, ods: FileSpreadsheet, csv: FileSpreadsheet, tsv: FileSpreadsheet,
    ppt: Presentation, pptx: Presentation, odp: Presentation,
    epub: BookOpen, mobi: BookOpen,
    sh: Wrench, bash: Wrench, zsh: Wrench, ksh: Wrench, ps1: Wrench, psm1: Wrench, psd1: Wrench,
    bat: Wrench, cmd: Wrench, fish: Wrench,
    bashrc: Wrench, zshrc: Wrench, profile: Wrench, bash_profile: Wrench,
    zprofile: Wrench, zshenv: Wrench, bash_aliases: Wrench,
    pdf: BookOpen, sql: Database, xml: FileCode, php: Terminal,
    db: Database, sqlite: Database, sqlite3: Database, db3: Database,
    mp4: Film, mkv: Film, avi: Film, mov: Film, wmv: Film, flv: Film, webm: Film, m4v: Film,
    mpg: Film, mpeg: Film, '3gp': Film,
    mp3: Music, wav: Music, flac: Music, aac: Music, ogg: Music, m4a: Music, opus: Music, wma: Music,
    ttf: Type, otf: Type, woff: Type, woff2: Type, eot: Type,
    exe: Binary, msi: Binary, dll: Binary, so: Binary, dylib: Binary,
    a: Binary, o: Binary, bin: Binary, appimage: Binary,
    pl: Terminal, pm: Terminal, diff: FileCode, patch: FileCode,
    dockerfile: FileCode, makefile: FileCode, cmake: FileCode, nginx: FileCode,
  };
  // Sanitize class fragment: keep alnum/._- only
  const safeExt = (ext || 'default').replace(/[^a-z0-9._-]/gi, '') || 'default';
  const IconComp = iconMap[ext] || File;
  return (
    <span className={`file-icon-themed file-icon-${safeExt}`}>
      <IconComp size={ICON_SIZE} />
    </span>
  );
}
