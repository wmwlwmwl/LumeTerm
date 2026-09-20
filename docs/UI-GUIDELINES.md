# LumeTerm 桌面端 UI 设计规范与组件指南 (UI Design System Specification)

> **适用范围**：`frontend/src` 全站组件与页面。  
> **设计哲学**：现代工业级桌面开发工具质感（参考 Linear / Raycast / VS Code / Zed 设计语言）。  
> **核心目标**：统一全站视觉规范，消除过度胶囊药丸/突兀圆角，杜绝系统原生直角控件拼装感，实现色彩、圆角、层次与微动效的高度协调。  
> **技术底座**：React 19 + Tailwind CSS v4（CSS-first Token 映射）+ 语义化设计系统（`src/index.css`）。

---

## 1. 核心黄金尺寸与圆角层级体系 (Radius & Sizing Standards)

全站严格遵循「**外大内小、层级递减、克制微圆角**」的几何规律，禁止在普通矩形控件、按钮或输入框上使用 `rounded-full` 或 `rounded-xl`（20px+）导致长扁椭圆药丸感。

| 层次级别 | 适用容器 / 控件对象 | 标准圆角 (Tailwind Class) | 高度 / 内边距基线 |
|---|---|---|---|
| **L1: 外层弹窗与浮层** | 全局对话框 (Modal)、设置大面板、右键菜单、悬浮操作条 | `rounded-[var(--radius-md)]` (12px) 或 `rounded-[var(--radius-lg)]` (16px) | 自带毛玻璃背景 `backdrop-blur-md` 与阴影 `shadow-xl` |
| **L2: 功能卡片与容器** | 服务器卡片 (ServerCard)、监控图表卡片、AI 消息卡片、端口转发卡片 | `rounded-[var(--radius-md)]` (12px) | 统一边框 `border border-line`，支持 `bg-raised` / `bg-canvas` |
| **L3: 标准交互控件** | 按钮 (Button)、输入框 (Input)、下拉选择器 (Select)、常用标签 (Tag/Chip) | `rounded-[var(--radius-sm)]` (8px) | 标准输入框 `h-8`~`h-8.5` (32~34px)；紧凑按钮 `h-7`~`h-7.5` (28~30px) |
| **L4: 分段选择器 (Segment)** | 外层滑槽底座 (`bg-sunken border-line-subtle`)<br>内部切换选项项 (`bg-accent text-white`) | 外槽：`rounded-[var(--radius-sm)]` (8px)<br>内项：`rounded-[6px]` (6px) | 外槽 `h-8`~`h-8.5`，内项 `h-7`，内边距 `p-0.5 gap-0.5` |
| **L5: 标签页 (Tabs)** | 顶栏主会话标签、终端子标签、文件管理器标签、AI 对话标签 | `rounded-[var(--radius-sm)]` (8px) | **统一锁定 28px 高度**，激活态底边附带 2px 主题色底线 |
| **L6: 微型图标与状态点** | 极简方形复选框 (`.custom-checkbox`)、状态指示小圆点 | 复选框：`14px × 14px` (3px 圆角)<br>指示圆点：`w-1.5 h-1.5` (`rounded-full`) | 复选框选中呈现实心主题色与 10px 粗体 Check 图标 |

---

## 2. 基础组件规范与使用范式 (`src/components/ui/`)

新开发功能与重构存量功能时，**一律使用封装好的标准 UI 组件**，禁止手写原生 `<select>`、`<input type="checkbox">` 或原生遮罩。

### 2.1 现代下拉选择器 (`Select.tsx`)
> 彻底替代 Windows 原生 0px 黑灰直角 `<select><option>`。

```tsx
import { Select } from '@/components/ui';

<Select
  value={authType}
  onChange={(val) => setAuthType(val)}
  options={[
    { value: 'password', label: t('密码认证') },
    { value: 'key', label: t('私钥认证') },
  ]}
  placeholder={t('选择认证方式')}
  size="sm"
/>
```
* **特性**：具备自适应视口防遮挡翻转、磨砂毛玻璃浮层、主题色微光圈、Check 选中标记及键盘方向键 / Esc 导航。

---

### 2.2 标准按钮组件 (`Button.tsx`)
```tsx
import { Button } from '@/components/ui';

// 主操作按钮 (Primary)
<Button variant="primary" size="sm" onClick={handleSave}>{t('保存')}</Button>

// 次级/通用按钮 (Secondary)
<Button variant="secondary" size="sm" onClick={handleCancel}>{t('取消')}</Button>

// 危险操作按钮 (Danger)
<Button variant="danger" size="sm" onClick={handleDelete}><Trash2 size={13} /></Button>

// 图标/幽灵按钮 (Ghost)
<Button variant="ghost" size="icon" aria-label={t('关闭')} onClick={onClose}><X size={14} /></Button>
```
* **规范**：默认圆角为 `rounded-[var(--radius-sm)]` (8px)，支持 `interactive` 顶光与微动效。

---

### 2.3 现代分段选择器规范 (Segmented Control)
全站分段器（主题切换、终端历史范围、AI 执行规则等）统一使用如下类名模板：

```tsx
<div className="inline-flex items-center gap-0.5 rounded-[var(--radius-sm)] border border-line-subtle bg-sunken p-0.5 shrink-0">
  <button
    type="button"
    onClick={() => setMode('server')}
    className={cn(
      'h-7 px-3 rounded-[6px] text-xs font-medium transition-colors cursor-pointer',
      mode === 'server'
        ? 'bg-accent text-white font-semibold shadow-xs'
        : 'bg-transparent text-secondary hover:text-primary hover:bg-hover/60'
    )}
  >
    {t('当前服务器')}
  </button>
  <button
    type="button"
    onClick={() => setMode('global')}
    className={cn(
      'h-7 px-3 rounded-[6px] text-xs font-medium transition-colors cursor-pointer',
      mode === 'global'
        ? 'bg-accent text-white font-semibold shadow-xs'
        : 'bg-transparent text-secondary hover:text-primary hover:bg-hover/60'
    )}
  >
    {t('全部服务器')}
  </button>
</div>
```

---

### 2.4 极简复选框 (`.custom-checkbox`)
```tsx
import { Check } from 'lucide-react';

<div
  className={cn('custom-checkbox', isChecked && 'checked')}
  onClick={() => toggleSelect(id)}
>
  {isChecked && <Check size={10} strokeWidth={4} />}
</div>
```
* **规格**：`14px × 14px`，未选中为浅灰微框，选中态为充满主题色背景与白色纯正微勾。

---

### 2.5 现代悬浮批量操作栏 (Floating / Docked Batch Bar)
在列表多选管理时，批量操作栏统一采用**独立停靠（Docked）或悬浮（Floating）卡片**，禁止与短列表内容粘连悬空：

```tsx
<div className="shrink-0 p-2 border-t border-line bg-raised grid gap-1.5">
  <div className="flex items-center gap-1.5">
    {/* 统一圆形主题色计数徽标 */}
    <div className="flex items-center gap-1 pl-1 pr-1.5 shrink-0 text-xs font-semibold text-primary">
      <span className="inline-flex items-center justify-center w-5 h-5 rounded-full bg-accent text-white text-[11px] font-bold">
        {selectedCount}
      </span>
      <span>{t('项')}</span>
    </div>
    <Button variant="secondary" size="sm" onClick={handleBatchAction} className="flex-1 h-7 text-xs rounded-[var(--radius-sm)]">
      {t('移动到分组')}
    </Button>
    <Button variant="danger" size="sm" onClick={handleBatchDelete} className="w-7 h-7 p-0 shrink-0 rounded-[var(--radius-sm)]">
      <Trash2 size={13} />
    </Button>
  </div>
</div>
```

---

## 3. 设计 Token 与语义化颜色系统

| 语义维度 | Token 工具类 | 语义用途与场景 |
|---|---|---|
| **表面背景 (Surface)** | `bg-canvas` | 底层基础画布背景 |
| | `bg-raised` | 一级浮起卡片、工具栏、侧边栏背景 |
| | `bg-overlay` | 模态框、悬浮菜单、Tooltip、Toast 磨砂背景 |
| | `bg-sunken` | 输入框底槽、分段器滑槽、代码内嵌区沉降背景 |
| | `bg-hover` / `bg-active` | 列表项鼠标悬浮态 / 按下点击态背景 |
| **文字颜色 (Text)** | `text-primary` | 核心标题、正文文本、高对比度强调字 |
| | `text-secondary` | 次要描述、标签名、常规图标颜色 |
| | `text-tertiary` | 提示说明文本、辅助小字、空状态提示 |
| | `text-muted` | 占位说明、极低对比度时间戳、禁用文字 |
| **边框体系 (Border)** | `border-line` | 标准组件与卡片外边框 |
| | `border-line-subtle` | 内部细分割线、次级控件内嵌微边框 |
| | `border-focus` | 聚焦输入框边框 |
| **主题强调 (Accent)** | `bg-accent` / `text-accent` | 品牌主题色（紫雾/经典蓝等），随用户主题动态生效 |
| | `bg-accent-dim` | 8%~12% 浅淡主题色背景，用于高光行与微徽章 |
| | `border-accent-border` | 主题色关联边框 |

---

## 4. 严禁事项与自动化 Lint 规则 (Strict Anti-Patterns)

开发新功能或修改现有界面时，必须严格遵守以下红线规则（CI 及 `styles:check` 会自动校验）：

- ❌ **严禁使用原生 `<select>` 标签**：必须统一迁移至 `<Select />`。
- ❌ **严禁在矩形控件上使用 `rounded-full` 或 `rounded-xl`**：按钮、输入框、标签、卡片必须使用 `rounded-[var(--radius-sm)]` (8px) 或 `rounded-[var(--radius-md)]` (12px)。
- ❌ **严禁在 TSX 中内联硬编码十六进制颜色 (`#ffffff`、`#1e293b`)**：一律使用语义化 Tailwind Token（如 `bg-canvas`、`text-primary`、`text-accent`）。
- ❌ **严禁使用 `zIndex` 魔数**：一律引用 `constants/zIndex.ts` 中的枚举对象 `Z.*`。
- ❌ **严禁手写固定悬浮背景**：列表项键盘/鼠标高亮必须采用 `hover:bg-hover` 与条件类 `isSelected && 'bg-accent-dim text-accent'`，默认未选中项不得留存固化背景色。

---

## 5. 质量验证与自动化构建命令

在提交任何代码前，请在 `frontend/` 目录下依次执行以下三项测试命令：

```powershell
# 1. 样式违规与基线检查 (必须 0 违规，且未超出基线)
npm run styles:check

# 2. 静态代码分析与类型检查 (408 文件必须 0 警告 0 错误)
npx oxlint src

# 3. 前端完整生产编译打包
npm run build
```
