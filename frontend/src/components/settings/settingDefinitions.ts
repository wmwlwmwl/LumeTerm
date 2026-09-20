// 桥接模块（自 .js 收编后类型化）：设置树定义与注册表构建
import type { I18nKey } from '../../i18n.ts'
import type { SettingsDefinitionNode } from './SharedComponents.tsx'


/** 设置节点（宽松形状：config/extra 任意字段透传；含 normalize 产出字段） */
export interface SettingsTreeNode extends SettingsDefinitionNode {
  tab?: string;
  section?: string;
  parentId?: string;
  providerId?: string;
  targetId?: string;
  /** 静态翻译键（空串表示无标题） */
  titleKey: I18nKey | '';
  descriptionKey: I18nKey | '';
  breadcrumbTitleKeys?: I18nKey[];
  children: SettingsTreeNode[];
}

/** 单个 Tab 的注册表（按 alias 索引 sections/fields） */
export interface SettingsTabRegistry {
  node: SettingsTreeNode
  sections: Record<string, SettingsTreeNode>
  fields: Record<string, SettingsTreeNode>
}

/** 搜索索引定义（buildSearchDefinitions 输出，字段归一化后必填） */
export interface SettingsSearchDefinition {
  id: string
  type: string
  tab: string
  section: string
  providerId: string
  targetId: string
  /** 进入搜索索引的节点 titleKey 必非空 */
  titleKey: I18nKey
  descriptionKey: I18nKey | ''
  breadcrumbTitleKeys: I18nKey[]
}

function createSettingsNode(config: Record<string, unknown> | null | undefined): SettingsTreeNode {
  return {
    titleKey: '',
    descriptionKey: '',
    targetId: (config?.id as string | undefined) || '',
    ...config,
    keywords: Array.isArray(config?.keywords) ? config.keywords : [],
    children: Array.isArray(config?.children) ? config.children as SettingsTreeNode[] : [],
  };
}


function createSettingsSectionDefinition(config: Record<string, unknown> | null | undefined): SettingsTreeNode {
  return createSettingsNode({
    type: 'section',
    targetId: (config?.targetId as string | undefined) || (config?.id as string | undefined) || '',
    ...config,
  });
}

const rootNode = (...children: SettingsTreeNode[]): SettingsTreeNode => createSettingsNode({ type: 'root', id: 'settings', children });
const tabNode = (id: string, titleKey: I18nKey | '', icon: string, children: SettingsTreeNode[] = []): SettingsTreeNode => createSettingsNode({ type: 'tab', id, alias: id, titleKey, icon, children });
const sectionNode = (tabId: string, alias: string, titleKey: I18nKey | '', children: SettingsTreeNode[] = [], extra: Record<string, unknown> = {}): SettingsTreeNode => createSettingsSectionDefinition({
  id: `${tabId}.section.${alias}`,
  tab: tabId,
  alias,
  titleKey,
  children,
  ...extra,
});
const panelNode = (id: string, children: SettingsTreeNode[] = []): SettingsTreeNode => createSettingsNode({ type: 'panel', id, children });
const subsectionNode = (id: string, titleKey: I18nKey | '', children: SettingsTreeNode[] = []): SettingsTreeNode => createSettingsNode({ type: 'subsection', id, titleKey, children });
const conditionalNode = (id: string, when: unknown, children: SettingsTreeNode[] = []): SettingsTreeNode => createSettingsNode({ type: 'conditional', id, when, children });
const fieldNode = (id: string, alias: string, titleKey: I18nKey | '', descriptionKey: I18nKey | '' = '', extra: Record<string, unknown> = {}): SettingsTreeNode => createSettingsNode({
  type: 'field',
  id,
  alias,
  titleKey,
  descriptionKey,
  targetId: extra.targetId || id,
  ...extra,
});
const fieldGroupNode = (
  id: string,
  alias: string,
  titleKey: I18nKey | '',
  descriptionKey: I18nKey | '' = '',
  ...rest: [children?: SettingsTreeNode[], extra?: Record<string, unknown>]
): SettingsTreeNode => {
  const [children = [], extra = {}] = rest;
  return createSettingsNode({
    type: 'field-group',
    id,
    alias,
    titleKey,
    descriptionKey,
    children,
    targetId: (extra.targetId as string | undefined) || id,
    ...extra,
  });
};
const optionNode = (id: string, alias: string, titleKey: I18nKey | '', descriptionKey: I18nKey | '' = '', extra: Record<string, unknown> = {}): SettingsTreeNode => createSettingsNode({
  type: 'option',
  id,
  alias,
  titleKey,
  descriptionKey,
  targetId: extra.targetId || id,
  ...extra,
});
const actionNode = (id: string, alias: string, titleKey: I18nKey | '', descriptionKey: I18nKey | '' = '', extra: Record<string, unknown> = {}): SettingsTreeNode => createSettingsNode({
  type: 'action',
  id,
  alias,
  titleKey,
  descriptionKey,
  targetId: extra.targetId || id,
  ...extra,
});

interface SettingsTreeContext {
  tab: string;
  section: string;
  providerId: string;
  breadcrumbs: I18nKey[];
}

function normalizeSettingsTree(node: SettingsTreeNode, parent: SettingsTreeNode | null = null, context: SettingsTreeContext = { tab: '', section: '', providerId: '', breadcrumbs: [] }): SettingsTreeNode {
  const nextTab = node.type === 'tab' ? node.id || '' : context.tab;
  const nextSection = node.type === 'section' ? node.id || '' : context.section;
  const nextProviderId = node.providerId || context.providerId || '';
  const breadcrumbTitleKeys = context.breadcrumbs;
  const childBreadcrumbs = (node.type ? ['root', 'panel', 'conditional'].includes(node.type) : false)
    ? context.breadcrumbs
    : (node.titleKey ? [...context.breadcrumbs, node.titleKey] : context.breadcrumbs);
  const normalizedNode = {
    ...node,
    tab: node.type === 'root' ? '' : nextTab,
    section: node.type === 'tab' ? '' : nextSection,
    parentId: parent?.id || '',
    providerId: nextProviderId,
    breadcrumbTitleKeys,
  };
  const children = (node.children || []).map((child) => normalizeSettingsTree(child, normalizedNode, {
    tab: nextTab,
    section: nextSection,
    providerId: nextProviderId,
    breadcrumbs: childBreadcrumbs,
  }));
  return Object.freeze({
    ...normalizedNode,
    children,
  }) as SettingsTreeNode;
}

const settingsTreeSource = rootNode(
  tabNode('general', '通用', 'SlidersHorizontal', [
    sectionNode('general', 'language', '语言', [
      panelNode('general.panel.language', [
        fieldNode('general.language', 'language', '界面语言', '选择界面显示语言', { control: 'select', stateKey: 'language' }),
      ]),
    ], { targetId: 'general.language' }),
    sectionNode('general', 'confirmation', '操作确认', [
      panelNode('general.panel.confirmation', [
        subsectionNode('general.confirmation.common', '常规操作确认', [
          fieldNode('general.close-session', 'closeSession', '关闭连接时确认', '关闭单个 SSH 连接前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmCloseSession' }),
          fieldNode('general.close-all', 'closeAll', '关闭全部时确认', '批量关闭所有连接前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmCloseAll' }),
          fieldNode('general.file-delete', 'fileDelete', '文件管理器删除时确认', '删除文件或文件夹前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmFileDelete' }),
          fieldNode('general.process-kill', 'processKill', '终止进程时确认', '终止进程前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmProcessKill' }),
        ]),
        subsectionNode('general.confirmation.git', 'Git操作确认', [
          fieldNode('general.git-discard', 'gitDiscard', 'Git放弃更改时确认', '执行 Git 放弃更改前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmGitDiscard' }),
          fieldNode('general.git-force-push', 'gitForcePush', 'Git强制推送时确认', '执行 Git 强制推送前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmGitForcePush' }),
          fieldNode('general.git-untrack', 'gitUntrack', 'Git取消跟踪时确认', '执行 Git 取消跟踪前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmGitUntrack' }),
        ]),
        subsectionNode('general.confirmation.terminal', '终端与窗口确认', [
          fieldNode('general.terminal-selection-paste-confirm', 'terminalSelectionPasteConfirm', '粘贴所选项时确认', '粘贴超过3行的终端选区前弹出确认弹窗', { control: 'toggle', stateKey: 'confirmTerminalSelectionPaste' }),
          fieldNode('general.window-close', 'windowClose', '关闭窗口时', '选择关闭窗口时的默认行为', { control: 'select', stateKey: 'windowCloseAction' }),
        ]),
      ]),
    ], { targetId: 'general.close-session' }),
    sectionNode('general', 'interaction', '交互偏好', [
      panelNode('general.panel.interaction', [
        fieldNode('general.right-click-paste', 'rightClickPaste', '右键直接粘贴', '开启后, 右键粘贴快捷操作会按下面选项触发', { control: 'toggle', stateKey: 'terminalRightClickPasteOnEmpty' }),
        conditionalNode('general.conditional.right-click-paste', { field: 'general.right-click-paste', equals: true }, [
          fieldGroupNode('general.right-click-paste-mode', 'rightClickPasteMode', '右键粘贴触发方式', '选择右键直接粘贴的触发范围', [
            optionNode('general.right-click-paste-empty', 'rightClickPasteEmpty', '仅无选区时直接粘贴', '当前行为, 有选区时仍打开右键菜单', { value: 'empty' }),
            optionNode('general.right-click-paste-always', 'rightClickPasteAlways', '无论是否有选区都直接粘贴', '右键始终直接粘贴到终端, 不再显示右键菜单', { value: 'always' }),
          ], { control: 'radio-group', stateKey: 'terminalRightClickPasteMode' }),
        ]),
        fieldNode('general.left-click-copy', 'leftClickCopy', '左键选区自动复制', '开启后, 左键选区相关的复制快捷操作会按下面选项触发; 有选区时右键仍打开菜单', { control: 'toggle', stateKey: 'terminalLeftClickCopyOnSelection' }),
        conditionalNode('general.conditional.left-click-copy', { field: 'general.left-click-copy', equals: true }, [
          fieldGroupNode('general.left-click-copy-mode', 'leftClickCopyMode', '左键复制触发方式', '选择左键自动复制的触发时机', [
            optionNode('general.left-click-copy-click', 'leftClickCopyClick', '点击已选中的文字时复制', '先完成选区, 再左键点击已选中的文字区域时自动复制', { value: 'click' }),
            optionNode('general.left-click-copy-mouseup', 'leftClickCopyMouseup', '选中后松开鼠标时立即复制', '左键拖拽选中后, 松开鼠标按键时立即自动复制', { value: 'mouseup' }),
          ], { control: 'radio-group', stateKey: 'terminalLeftClickCopyOnSelectionMode' }),
        ]),
        fieldNode('general.terminal-double-click', 'terminalDoubleClick', '为终端标签页添加双击行为', '开启后, 双击普通终端标签时会执行下面选择的动作; 分屏组标签不生效', { control: 'toggle', stateKey: 'terminalTabDoubleClickActionEnabled' }),
        conditionalNode('general.conditional.terminal-double-click', { field: 'general.terminal-double-click', equals: true }, [
          fieldGroupNode('general.terminal-double-click-mode', 'terminalDoubleClickMode', '双击动作', '选择双击终端标签时要执行的动作', [
            optionNode('general.close-tab', 'closeTab', '关闭标签页', '双击普通终端标签时直接关闭该标签页', { value: 'close' }),
            optionNode('general.duplicate-tab', 'duplicateTab', '复制标签页', '双击普通终端标签时在同一连接下复制一个新终端标签, 并同步当前工作目录和文件资源管理器标签页', { value: 'duplicate' }),
          ], { control: 'radio-group', stateKey: 'terminalTabDoubleClickAction' }),
        ]),
      ]),
    ], { targetId: 'general.right-click-paste' }),
    sectionNode('general', 'workspace', '工作区', [
      panelNode('general.panel.workspace', [
        fieldNode('general.remember-workspace', 'rememberWorkspace', '记忆工作区', '重新启动后自动恢复上次的连接、终端标签和分屏布局', { control: 'toggle', stateKey: 'rememberWorkspace' }),
        conditionalNode('general.conditional.workspace', { field: 'general.remember-workspace', equals: true }, [
          fieldGroupNode('general.persistence-level', 'persistenceLevel', '持久化级别', '选择工作区状态的额外持久化粒度', [
            optionNode('general.program-persistence', 'programPersistence', '程序', '仅保留当前的程序级工作区恢复行为', { value: 'program' }),
            optionNode('general.session-persistence', 'sessionPersistence', '会话', '在保留程序级恢复的同时, 为每个服务器单独保存最近一次会话状态; 重新连接该服务器时优先恢复', { value: 'session' }),
          ], { control: 'radio-group', stateKey: 'workspacePersistenceLevel' }),
        ]),
      ]),
    ], { targetId: 'general.remember-workspace' }),
    sectionNode('general', 'update', '更新下载', [
      panelNode('general.panel.update', [
        fieldNode('general.update-use-proxy', 'updateUseProxy', '优先使用镜像下载', '优先通过多个镜像地址下载 GitHub 更新,失败后自动回退为官方直连下载', { control: 'toggle', stateKey: 'updateUseProxy' }),
      ]),
    ], { targetId: 'general.update-use-proxy' }),
    sectionNode('general', 'rendering', '渲染', [
      panelNode('general.panel.rendering', [
        fieldNode('general.webview-gpu', 'webviewGpu', '禁用硬件加速', '关闭 WebView GPU 加速，重启应用后生效', { control: 'toggle', stateKey: 'webviewGpuDisabled' }),
      ]),
    ], { targetId: 'general.webview-gpu' }),
  ]),
  tabNode('network', '网络', 'Globe', [
    sectionNode('network', 'latency', '延迟检测', [
      panelNode('network.panel.latency', [
        fieldNode('network.ping-enabled', 'pingEnabled', '启用延迟检测', '定期向服务器发起轻量级探测，实时了解服务器的在线状态和响应速度', { control: 'toggle', stateKey: 'pingEnabled' }),
      ]),
    ], { targetId: 'network.ping-enabled' }),
    sectionNode('network', 'mode', '检测方式', [
      panelNode('network.panel.mode', [
        fieldGroupNode('network.detection-mode', 'detectionMode', '检测方式', '选择延迟检测的探测方式，不同方式适用于不同网络环境', [
          optionNode('network.smart-detection', 'smartDetection', '智能检测', '直连用 TCP 测延迟；检测到代理/TUN 时低频 Banner 确认可达性，避免 Clash 等环境下不可达主机显示 0 毫秒在线', { value: 'auto' }),
          optionNode('network.banner-rtt', 'bannerRtt', 'SSH Banner RTT', '所有连接都读取 SSH 握手响应测速，准确反映真实可达性，能穿透 TUN/代理；选择后会自动将延迟检测间隔调整为至少 15 秒', { value: 'banner' }),
          optionNode('network.tcp-dial', 'tcpDial', 'TCP Dial', '仅检测 TCP 端口连通性，速度最快，但在 TUN/代理下可能把不可达服务器误判为在线', { value: 'tcp' }),
        ], { control: 'radio-group', stateKey: 'pingMode' }),
      ]),
    ], { targetId: 'network.detection-mode' }),
    sectionNode('network', 'refresh', '监控刷新频率', [
      panelNode('network.panel.refresh', [
        fieldNode('network.probe-interval', 'probeInterval', '探针刷新间隔', '设置探针数据和延迟测试的自动刷新间隔', { control: 'button-group', stateKey: 'probeInterval' }),
        fieldNode('network.ping-interval', 'pingInterval', '延迟检测间隔', '设置探针数据和延迟测试的自动刷新间隔', { control: 'button-group', stateKey: 'pingInterval' }),
      ]),
    ], { targetId: 'network.probe-interval' }),
    sectionNode('network', 'proxy', '代理节点管理', [
      panelNode('network.panel.proxy', [
        fieldNode('network.proxy-nodes', 'proxyNodes', '代理节点管理', '添加并管理本地代理节点，可供 AI 请求与服务器 SSH/SFTP 连接复用', { control: 'collection' }),
        fieldNode('network.proxy-name', 'proxyName', '代理名称（备注）', '仅用于区分代理节点，不参与连接逻辑', { control: 'input', stateKey: 'proxyForm.name' }),
        fieldNode('network.proxy-type', 'proxyType', '协议类型', '', { control: 'select', stateKey: 'proxyForm.type' }),
        fieldNode('network.proxy-host', 'proxyHost', '主机地址', '', { control: 'input', stateKey: 'proxyForm.host' }),
        fieldNode('network.proxy-port', 'proxyPort', '端口', '', { control: 'input', stateKey: 'proxyForm.port' }),
        fieldNode('network.proxy-username', 'proxyUsername', '用户名', '', { control: 'input', stateKey: 'proxyForm.username' }),
        fieldNode('network.proxy-password', 'proxyPassword', '密码', '', { control: 'input', stateKey: 'proxyForm.password' }),
      ]),
    ], { targetId: 'network.proxy-nodes' }),
  ]),
  tabNode('fileManager', '文件管理器', 'Folder', [
    sectionNode('fileManager', 'preferences', '偏好设置', [
      panelNode('fileManager.panel.preferences', [
        fieldNode('fileManager.compressed-transfer', 'compressedTransfer', '压缩传输', '多文件或文件夹上传时先在本机打包为 tar.gz，上传后远端自动解压', { control: 'toggle', stateKey: 'fileManagerCompressedTransfer' }),
        fieldNode('fileManager.auto-open-transfer-queue', 'autoOpenTransferQueue', '发起传输任务时自动打开传输队列', '上传或下载新建传输任务后自动展开传输队列面板', { control: 'toggle', stateKey: 'fileManagerAutoOpenTransferQueue' }),
        fieldNode('fileManager.show-tab-icons', 'showTabIcons', '显示文件资源管理器标签页目录图标', '关闭后只隐藏目录图标,仍显示固定图标', { control: 'toggle', stateKey: 'fileManagerShowTabIcons' }),
        fieldNode('fileManager.hide-tab-close-button', 'hideTabCloseButton', '文件资源管理器不显示关闭图标按钮', '开启后,文件资源管理器标签页不显示关闭图标按钮,仅可双击关闭', { control: 'toggle', stateKey: 'fileManagerHideTabCloseButton' }),
        fieldNode('fileManager.shared-pinned-tabs', 'sharedPinnedTabs', '固定标签跨终端共享', '开启后,同一服务器下所有终端共用一组文件资源管理器固定标签', { control: 'toggle', stateKey: 'fileManagerSharedPinnedTabs' }),
        fieldGroupNode('fileManager.layout-mode', 'layoutMode', '文件资源管理器视图', '选择顶部标签单栏,或左侧标签双面板视图', [
          optionNode('fileManager.classic-layout', 'classicLayout', '经典顶部标签', '保留当前顶部横向标签栏与单内容区', { value: 'classic' }),
          optionNode('fileManager.dual-layout', 'dualLayout', '左侧标签双面板', '左侧显示历史标签,主内容区同时显示左右两个文件列表', { value: 'sidebar_dual' }),
        ], { control: 'radio-group', stateKey: 'fileManagerLayoutMode' }),
        conditionalNode('fileManager.conditional.layout-mode', { field: 'fileManager.layout-mode', equals: 'sidebar_dual' }, [
          fieldNode('fileManager.dual-drag-transfer', 'dualDragTransfer', '允许左右面板互相拖拽文件', '开启后,可在双栏之间直接拖拽文件;默认复制,按住 Ctrl 为移动', { control: 'toggle', stateKey: 'fileManagerDualPaneDragTransferEnabled' }),
          fieldNode('fileManager.dual-drag-prompt', 'dualDragPrompt', '拖拽文件夹时先询问', '开启后,拖拽内容包含文件夹时先确认是否继续', { control: 'toggle', stateKey: 'fileManagerDualPaneDragPromptOnDirectory' }),
          fieldNode('fileManager.dual-drag-invert', 'dualDragInvert', '反转 Ctrl 拖拽语义', '开启后,默认移动,按住 Ctrl 为复制', { control: 'toggle', stateKey: 'fileManagerDualPaneDragInvertModifier' }),
        ]),
        fieldNode('fileManager.chmod-auto-apply', 'chmodAutoApply', '默认应用上次权限设置', '开启后,修改权限弹窗会默认套用上次保存的权限模式和包含子目录选项', { control: 'toggle', stateKey: 'fileManagerChmodAutoApplyLastSettings' }),
        fieldNode('fileManager.double-click-uncompress', 'doubleClickUncompress', '双击解压压缩包', '开启后,双击压缩包会直接解压;右键“解压”也会使用同样的智能解压规则', { control: 'toggle', stateKey: 'fileManagerDoubleClickUncompressArchive' }),
        fieldNode('fileManager.auto-refresh', 'autoRefresh', '自动刷新', '在终端执行命令后、或切回文件管理器时自动刷新当前目录。关闭可减少对远程服务器的请求', { control: 'toggle', stateKey: 'fileManagerAutoRefreshDisabled' }),
        fieldNode('fileManager.max-edit-size', 'maxEditSize', '文件编辑大小上限 (MB)', '双击或用编辑器打开文件时的最大文件大小，超过将拒绝打开以避免卡顿或内存溢出。范围 1-50，默认 5', { control: 'input', stateKey: 'fileManagerMaxEditSizeMB' }),
        fieldGroupNode('fileManager.uncompress-conflict', 'uncompressConflict', '智能解压遇到同名文件夹时', '如果准备解压到“压缩包同名文件夹”,但这个文件夹已经存在,就按这里处理', [
          optionNode('fileManager.uncompress-overwrite', 'uncompressOverwrite', '覆盖', '继续解压到现有同名文件夹,里面同名文件会被替换', { value: 'overwrite' }),
          optionNode('fileManager.uncompress-rename', 'uncompressRename', '自动重命名', '保留已有文件夹,自动新建“压缩包名 (2)”这类文件夹', { value: 'auto_rename' }),
          optionNode('fileManager.uncompress-prompt', 'uncompressPrompt', '每次都询问我', '每次遇到同名文件夹时都弹窗让我选', { value: 'prompt' }),
        ], { control: 'radio-group', stateKey: 'fileManagerSmartUncompressConflictStrategy' }),
        fieldGroupNode('fileManager.default-open-mode', 'defaultOpenMode', '打开文件默认方式', '双击或点“编辑”时的默认打开方式；编辑器内仍可随时切换到系统/指定编辑器', [
          optionNode('fileManager.builtin-editor', 'builtinEditor', '内置编辑器', '使用 LumeTerm 内置编辑器打开，支持高亮与保存回远端', { value: 'builtin' }),
          optionNode('fileManager.system-editor', 'systemEditor', '系统编辑器', '用系统默认程序打开临时文件，保存后自动同步回远端', { value: 'system' }),
          optionNode('fileManager.external-editor', 'externalEditor', '指定外部编辑器', '始终使用你选择的编辑器程序打开，例如 VS Code / Notepad++', { value: 'external' }),
        ], { control: 'radio-group', stateKey: 'fileManagerDefaultOpenMode' }),
        fieldGroupNode('fileManager.initial-path', 'initialPath', '进入服务器默认路径', '首次打开文件管理器时的初始目录来源', [
          optionNode('fileManager.session-initial-path', 'sessionInitialPath', '服务器初始目录', '优先使用当前服务器配置中的文件管理器初始目录，未填写时使用当前终端启动目录', { value: 'session_initial_path' }),
          optionNode('fileManager.root-initial-path', 'rootInitialPath', '根目录', '首次进入时从根目录开始', { value: 'root' }),
          optionNode('fileManager.terminal-initial-path', 'terminalInitialPath', '当前终端目录', '使用当前终端最近一次上报的工作目录', { value: 'terminal_cwd' }),
        ], { control: 'radio-group', stateKey: 'fileManagerInitialPathMode' }),
        fieldGroupNode('fileManager.new-tab-path', 'newTabPath', '新建标签默认路径', '手动新建文件管理器标签时的初始目录来源', [
          optionNode('fileManager.inherit-current-path', 'inheritCurrentPath', '继承当前标签所在目录', '新标签默认打开当前标签所在目录', { value: 'inherit_current' }),
          optionNode('fileManager.new-tab-root-path', 'newTabRootPath', '根目录', '新标签始终从根目录开始', { value: 'root' }),
          optionNode('fileManager.new-tab-session-path', 'newTabSessionPath', '服务器初始目录', '优先使用当前服务器配置中的文件管理器初始目录，未填写时使用当前终端启动目录', { value: 'session_initial_path' }),
          optionNode('fileManager.new-tab-terminal-path', 'newTabTerminalPath', '当前终端目录', '使用当前终端最近一次上报的工作目录', { value: 'terminal_cwd' }),
        ], { control: 'radio-group', stateKey: 'fileManagerNewTabPathMode' }),
      ]),
    ], { targetId: 'fileManager.compressed-transfer' }),
    sectionNode('fileManager', 'concurrency', '传输并发', [
      panelNode('fileManager.panel.concurrency', [
        fieldNode('fileManager.chunk-size', 'chunkSize', '单文件分块大小 (KiB)', '控制单个文件上传时的默认分块大小', { control: 'input', stateKey: 'fileManagerUploadChunkSizeKiB' }),
        fieldNode('fileManager.max-transfer-tasks', 'maxTransferTasks', '最大传输任务数量', '控制当前会话内同时进行的上传和下载任务数量,每个文件或文件夹都算一个任务', { control: 'input', stateKey: 'fileManagerUploadMaxFiles' }),
        fieldNode('fileManager.max-chunks-per-file', 'maxChunksPerFile', '单文件分块传输最大数量', '控制单个文件在同一时间允许并发传输的分块数量', { control: 'input', stateKey: 'fileManagerUploadMaxChunksPerFile' }),
        fieldNode('fileManager.global-inflight-limit', 'globalInflightLimit', '全局在途块上限', '控制所有上传任务共享的在途分块总数', { control: 'input', stateKey: 'fileManagerUploadGlobalInflightLimit' }),
        fieldNode('fileManager.max-packet-size', 'maxPacketSize', 'SFTP 单包大小 (KiB)', '单个 SFTP 数据包的载荷上限,高延迟链路上调大可显著提速;如果服务器不接受会自动回退', { control: 'input', stateKey: 'transferMaxPacketKiB' }),
        fieldNode('fileManager.request-pipeline-depth', 'requestPipelineDepth', 'SFTP 单文件请求流水线深度', '单个文件同时保持在链路上的 SFTP 请求数量', { control: 'input', stateKey: 'transferMaxRequestsPerFile' }),
        fieldNode('fileManager.concurrent-writes', 'concurrentWrites', 'SFTP 并发写入', '开启后单次写入内部并行发包,不再逐包等待服务器确认;关闭则退回逐包串行,速度会明显变慢', { control: 'toggle', stateKey: 'transferConcurrentWrites' }),
        fieldNode('fileManager.apply-shared-client', 'applySharedClient', '下载与文件操作也使用上述调优', '开启后下载,文件列表,读写文件都使用同一套调优参数;如果服务器兼容性较差可关闭,仅上传使用调优', { control: 'toggle', stateKey: 'transferApplyToSharedClient' }),
      ]),
    ], { targetId: 'fileManager.chunk-size' }),
    sectionNode('fileManager', 'download', '下载保存', [
      panelNode('fileManager.panel.download', [
        fieldNode('fileManager.ask-download-every-time', 'askDownloadEveryTime', '每次下载都询问', '开启后，每次下载文件或文件夹前都先询问保存位置；关闭后直接保存到默认位置', { control: 'toggle', stateKey: 'fileManagerAskDownloadEveryTime' }),
        fieldGroupNode('fileManager.download-conflict', 'downloadConflict', '下载遇到同名时', '', [
          optionNode('fileManager.diff-overwrite', 'diffOverwrite', '差异覆盖', '目录下载时逐文件比较，大小或修改时间任一不同即覆盖，相同则跳过', { value: 'diff_overwrite' }),
          optionNode('fileManager.force-overwrite', 'forceOverwrite', '强制覆盖', '文件直接覆盖；文件夹保留多余本地文件，仅覆盖远端存在的同名内容', { value: 'force_overwrite' }),
          optionNode('fileManager.prompt-conflict', 'promptConflict', '每次都询问我', '首次遇到冲突时询问，并可应用到本次剩余冲突', { value: 'prompt' }),
          optionNode('fileManager.auto-rename-conflict', 'autoRenameConflict', '自动重命名', '保留已有文件，下载结果自动追加后缀', { value: 'auto_rename' }),
        ], { control: 'radio-group', stateKey: 'fileManagerDownloadConflictStrategy' }),
        conditionalNode('fileManager.conditional.download-conflict-diff', { field: 'fileManager.download-conflict', equals: 'diff_overwrite' }, [
          fieldNode('fileManager.compare-size', 'compareSize', '比较文件大小', '大小不同即判定为差异', { control: 'toggle', stateKey: 'fileManagerDownloadConflictDiffBySize' }),
          fieldNode('fileManager.compare-mtime', 'compareMtime', '比较修改时间', '修改时间不同即判定为差异', { control: 'toggle', stateKey: 'fileManagerDownloadConflictDiffByMtime' }),
        ]),
        conditionalNode('fileManager.conditional.download-conflict-rename', { field: 'fileManager.download-conflict', equals: 'auto_rename' }, [
          fieldGroupNode('fileManager.rename-suffix', 'renameSuffix', '自动重命名后缀', '', [
            optionNode('fileManager.timestamp-suffix', 'timestampSuffix', '高精度时间戳', '格式：name_yyyymmdd_hhmmss_nnnnnnnnn.ext', { value: 'timestamp' }),
            optionNode('fileManager.random-suffix', 'randomSuffix', '随机数', '格式：name_ab12cd34.ext', { value: 'random' }),
            optionNode('fileManager.sequence-suffix', 'sequenceSuffix', '顺序号 +1', '格式：name_1.ext、name_2.ext，自动在已有最大序号上加 1', { value: 'sequence' }),
          ], { control: 'radio-group', stateKey: 'fileManagerDownloadRenameSuffixMode' }),
        ]),
        fieldNode('fileManager.download-default-dir', 'downloadDefaultDir', '下载默认保存位置', '支持变量：{value}（程序所在目录）', { control: 'input', stateKey: 'fileManagerDownloadDefaultDir' }),
      ]),
    ], { targetId: 'fileManager.ask-download-every-time' }),
  ]),
  tabNode('runtimeEnvironment', '运行环境', 'Database', [
    sectionNode('runtimeEnvironment', 'environment', '环境依赖', [
      panelNode('runtimeEnvironment.panel.environment', [
        fieldNode('runtimeEnvironment.uv', 'uv', '环境依赖', '管理应用运行所需的二进制工具与运行时依赖', { control: 'status' }),
        fieldNode('runtimeEnvironment.uv-binary', 'uvBinary', 'uv 可执行文件', '', { control: 'readonly' }),
      ]),
    ], { targetId: 'runtimeEnvironment.uv' }),
  ]),
  tabNode('appearance', '外观', 'Palette', [
    sectionNode('appearance', 'terminal', '终端显示', [
      panelNode('appearance.panel.terminal', [
        fieldGroupNode('appearance.font-manager', 'fontManager', '字体管理器', '从字体目录拖拽字体到右侧区域，为界面文本、终端输出和 AI 面板分别分配字体', [
          fieldNode('appearance.ui-font', 'uiFont', '界面文本', '作用于应用界面中的普通文本', { control: 'drop-target' }),
          fieldNode('appearance.terminal-font', 'terminalFont', '终端输出', '只作用于终端输出区域，不影响界面控件', { control: 'drop-target' }),
          fieldNode('appearance.ai-font', 'aiFont', 'AI面板', '作用于 AI 面板普通文本与输入区，代码块保持默认等宽字体', { control: 'drop-target' }),
        ], { control: 'group' }),
        fieldNode('appearance.terminal-font-size', 'terminalFontSize', '终端字体大小', '调节终端的字符显示大小', { control: 'range', stateKey: 'terminalFontSize' }),
        fieldNode('appearance.terminal-local-echo', 'terminalLocalEcho', '终端输入回显', '关闭后输入密码等敏感内容时不会显示字符', { control: 'toggle', stateKey: 'terminalLocalEcho' }),
        fieldNode('appearance.terminal-timestamps', 'terminalTimestamps', '每行显示时间', '在终端每行输出前添加时间戳', { control: 'toggle', stateKey: 'terminalTimestamps' }),
        fieldNode('appearance.terminal-command-blocks', 'terminalCommandBlocks', '命令块边框', '左侧显示可折叠命令块，点击收起输出', { control: 'toggle', stateKey: 'terminalCommandBlocks' }),
        fieldNode('appearance.terminal-default-mouse-cursor', 'terminalDefaultMouseCursor', '终端输出保持默认鼠标指针', '开启后, 终端输出区域使用系统默认鼠标指针, 不显示工字型文本光标', { control: 'toggle', stateKey: 'terminalDefaultMouseCursor' }),
        fieldNode('appearance.terminal-keyword-highlight', 'terminalKeywordHighlight', '日志关键字高亮', '对 error、warning、info、success 等关键字着色显示', { control: 'toggle', stateKey: 'terminalKeywordHighlight' }),
      ]),
    ], { targetId: 'appearance.font-manager' }),
    sectionNode('appearance', 'theme', '主题包', [
      panelNode('appearance.panel.theme', [
        fieldNode('appearance.theme', 'theme', '主题', '浅色、深色和系统模式分别决定当前应用哪一套主题包', { control: 'segmented', stateKey: 'themeMode' }),
        fieldNode('appearance.light-theme-package', 'lightThemePackage', '浅色主题包', '当主题为浅色或系统切换到浅色时使用', { control: 'theme-package' }),
        fieldNode('appearance.dark-theme-package', 'darkThemePackage', '深色主题包', '当主题为深色或系统切换到深色时使用', { control: 'theme-package' }),
        fieldNode('appearance.monitor-panel', 'monitorPanel', '监控面板位置', '', { control: 'segmented', stateKey: 'probePanelPosition' }),
      ]),
    ], { targetId: 'appearance.theme' }),
    sectionNode('appearance', 'topbar', '顶栏按钮', [
      panelNode('appearance.panel.topbar', [
        fieldNode('appearance.topbar-big-screen', 'topbarBigScreen', '数据大屏', '在顶栏显示数据大屏按钮', { control: 'toggle', stateKey: 'showBigScreenQuickEntry' }),
        fieldNode('appearance.topbar-theme', 'topbarTheme', '主题切换', '在顶栏显示主题切换按钮', { control: 'toggle', stateKey: 'showThemeQuickEntry' }),
        fieldNode('appearance.topbar-ai', 'topbarAI', 'AI助手', '在顶栏显示 AI 助手按钮', { control: 'toggle', stateKey: 'showAIQuickEntry' }),
      ]),
    ], { targetId: 'appearance.topbar-big-screen' }),
    sectionNode('appearance', 'preferences', '偏好设置', [
      panelNode('appearance.panel.preferences', [
        fieldNode('appearance.toolbar-icon-only', 'toolbarIconOnly', '终端工具栏仅显示图标', '开启后终端工具栏的进程管理、网络监控等按钮只显示图标', { control: 'toggle', stateKey: 'terminalToolbarIconOnly' }),
        fieldNode('appearance.editor-word-wrap', 'editorWordWrap', '编辑器与查看器自动换行', '开启后变更审阅台差异视图与文件管理器打开的文件编辑器超长行自动折行显示', { control: 'toggle', stateKey: 'editorWordWrap' }),
      ]),
    ], { targetId: 'appearance.toolbar-icon-only' }),
    sectionNode('appearance', 'background', '背景', [
      panelNode('appearance.panel.background', [
        fieldNode('appearance.terminal-wallpaper', 'terminalWallpaper', '终端背景', '设置终端显示区域的背景图片', { control: 'upload', stateKey: 'termBgImage' }),
        fieldNode('appearance.wallpaper-opacity', 'wallpaperOpacity', '终端背景可见度', '', { control: 'range', stateKey: 'termBgOpacity' }),
      ]),
    ], { targetId: 'appearance.terminal-wallpaper' }),
    sectionNode('appearance', 'window', '窗口大小', [
      panelNode('appearance.panel.window', [
        fieldNode('appearance.remember-window-size', 'rememberWindowSize', '记住窗口大小', '下次启动时恢复上次调整的窗口尺寸', { control: 'toggle', stateKey: 'rememberWindowSize' }),
        actionNode('appearance.reset-window-size', 'resetWindowSize', '恢复默认大小', '下次启动时恢复上次调整的窗口尺寸', { actionKey: 'resetWindowSize' }),
      ]),
    ], { targetId: 'appearance.remember-window-size' }),
  ]),
  tabNode('shortcuts', '快捷键', 'Keyboard', [
    sectionNode('shortcuts', 'terminal', '终端快捷键', [
      panelNode('shortcuts.panel.terminal', [
        fieldNode('shortcuts.server-search', 'serverSearch', '搜索服务器', '', { control: 'shortcut' }),
        fieldNode('shortcuts.copy', 'copy', '从终端复制', '', { control: 'shortcut' }),
        fieldNode('shortcuts.paste', 'paste', '粘贴到终端', '', { control: 'shortcut' }),
        fieldNode('shortcuts.paste-selection', 'pasteSelection', '粘贴所选项', '', { control: 'shortcut' }),
        fieldNode('shortcuts.clear', 'clear', '清空终端缓冲区', '', { control: 'shortcut' }),
        fieldNode('shortcuts.new-tab', 'newTab', '新建本地标签页', '', { control: 'shortcut' }),
        fieldNode('shortcuts.find', 'find', '查找终端内容', '', { control: 'shortcut' }),
        fieldNode('shortcuts.sigint', 'sigint', '打断当前指令 (SIGINT)', '', { control: 'shortcut' }),
        fieldNode('shortcuts.eof', 'eof', '结束终端会话 (EOF)', '', { control: 'shortcut' }),
        fieldNode('shortcuts.suspend', 'suspend', '后台挂起进程 (SIGTSTP)', '', { control: 'shortcut' }),
        fieldNode('shortcuts.clear-line', 'clearLine', '清空当前输入行', '', { control: 'shortcut' }),
        actionNode('shortcuts.reset-shortcuts', 'resetShortcuts', '恢复默认', '', { actionKey: 'resetShortcuts' }),
      ]),
    ], { targetId: 'shortcuts.copy' }),
  ]),
  tabNode('sync', '同步与云', 'Cloud', [
    sectionNode('sync', 'sync', '自动同步', [
      panelNode('sync.panel.sync', [
        fieldNode('sync.auto-sync', 'autoSync', '自动同步', '', { control: 'toggle', stateKey: 'autoSyncEnabled' }),
        fieldNode('sync.auto-sync-mode', 'autoSyncMode', '自动同步模式', '', { control: 'button-group', stateKey: 'syncMode' }),
        fieldNode('sync.encryption', 'encryption', '同步加密', '默认明文同步，选择加密后需设置恢复密码', { control: 'password-flow' }),
      ]),
    ], { targetId: 'sync.auto-sync' }),
    sectionNode('sync', 'provider', '同步服务', [
      panelNode('sync.panel.provider', [
        fieldGroupNode('sync.webdav', 'webdav', 'WebDAV 配置', '配置 WebDAV 端点用于加密同步服务器列表', [
          fieldNode('sync.webdav-url', 'endpoint', '端点地址 (URL)', '', { control: 'input', stateKey: 'webdavForm.url', providerId: 'webdav' }),
          fieldNode('sync.webdav-username', 'webdavUsername', '用户名', '', { control: 'input', stateKey: 'webdavForm.username', providerId: 'webdav' }),
          fieldNode('sync.webdav-password', 'webdavPassword', '密码 / 授权码', '', { control: 'input', stateKey: 'webdavForm.password', providerId: 'webdav' }),
          fieldNode('sync.webdav-remote-directory', 'webdavRemoteDirectory', '远程保存目录', '', { control: 'input', stateKey: 'webdavForm.remotePath', providerId: 'webdav' }),
          fieldNode('sync.webdav-max-backups', 'webdavMaxBackups', '保留份数 (0=不限)', '', { control: 'input', stateKey: 'webdavForm.maxBackups', providerId: 'webdav' }),
        ], { control: 'provider-card', providerId: 'webdav', targetId: 'sync.webdav' }),
        fieldGroupNode('sync.r2', 'r2', 'R2 (S3 兼容) 配置', '配置 Cloudflare R2 或任意 S3 兼容对象存储用于加密同步', [
          fieldNode('sync.access-key', 'accessKey', '访问密钥 ID (Access Key ID)', '', { control: 'input', stateKey: 'r2Form.accessKeyId', providerId: 'r2' }),
          fieldNode('sync.r2-secret-access-key', 'r2SecretAccessKey', '秘密访问密钥 (Secret Access Key)', '', { control: 'input', stateKey: 'r2Form.secretAccessKey', providerId: 'r2' }),
          fieldNode('sync.bucket', 'bucket', '存储桶 (Bucket)', '', { control: 'input', stateKey: 'r2Form.bucket', providerId: 'r2' }),
          fieldNode('sync.r2-endpoint', 'r2Endpoint', '端点地址 (Endpoint)', '', { control: 'input', stateKey: 'r2Form.endpoint', providerId: 'r2' }),
          fieldNode('sync.r2-region', 'r2Region', '区域 (Region)', '', { control: 'input', stateKey: 'r2Form.region', providerId: 'r2' }),
          fieldNode('sync.r2-prefix', 'r2Prefix', '前缀 (Prefix)', '', { control: 'input', stateKey: 'r2Form.prefix', providerId: 'r2' }),
          fieldNode('sync.r2-max-backups', 'r2MaxBackups', '保留份数 (0=不限)', '', { control: 'input', stateKey: 'r2Form.maxBackups', providerId: 'r2' }),
        ], { control: 'provider-card', providerId: 'r2', targetId: 'sync.r2' }),
        fieldGroupNode('sync.ftp', 'ftp', 'FTP 配置', '配置 FTP 服务器用于加密同步服务器列表', [
          fieldNode('sync.ftp-mode', 'ftpMode', '连接模式', '', { control: 'select', stateKey: 'ftpForm.mode', providerId: 'ftp' }),
          fieldNode('sync.host', 'host', '主机地址', '', { control: 'input', stateKey: 'ftpForm.host', providerId: 'ftp' }),
          fieldNode('sync.port', 'port', '端口', '', { control: 'input', stateKey: 'ftpForm.port', providerId: 'ftp' }),
          fieldNode('sync.username', 'username', '用户名', '', { control: 'input', stateKey: 'ftpForm.username', providerId: 'ftp' }),
          fieldNode('sync.password', 'password', '密码', '', { control: 'input', stateKey: 'ftpForm.password', providerId: 'ftp' }),
          fieldNode('sync.remote-directory', 'remoteDirectory', '远程保存目录', '', { control: 'input', stateKey: 'ftpForm.remoteDir', providerId: 'ftp' }),
          fieldNode('sync.ftp-max-backups', 'ftpMaxBackups', '保留份数 (0=不限)', '', { control: 'input', stateKey: 'ftpForm.maxBackups', providerId: 'ftp' }),
        ], { control: 'provider-card', providerId: 'ftp', targetId: 'sync.ftp' }),
        fieldGroupNode('sync.sftp', 'sftp', 'SFTP (SSH) 配置', '配置 SFTP 服务器用于加密同步服务器列表', [
          fieldNode('sync.sftp-host', 'sftpHost', '主机地址', '', { control: 'input', stateKey: 'sftpForm.host', providerId: 'sftp' }),
          fieldNode('sync.sftp-port', 'sftpPort', '端口', '', { control: 'input', stateKey: 'sftpForm.port', providerId: 'sftp' }),
          fieldNode('sync.sftp-username', 'sftpUsername', '用户名', '', { control: 'input', stateKey: 'sftpForm.username', providerId: 'sftp' }),
          fieldNode('sync.auth-method', 'authMethod', '认证方式', '', { control: 'select', stateKey: 'sftpForm.authMethod', providerId: 'sftp' }),
          fieldNode('sync.sftp-password', 'sftpPassword', '密码', '', { control: 'input', stateKey: 'sftpForm.password', providerId: 'sftp' }),
          fieldNode('sync.private-key', 'privateKey', '私钥内容', '', { control: 'input', stateKey: 'sftpForm.privateKey', providerId: 'sftp' }),
          fieldNode('sync.sftp-remote-directory', 'sftpRemoteDirectory', '远程保存目录', '', { control: 'input', stateKey: 'sftpForm.remoteDir', providerId: 'sftp' }),
          fieldNode('sync.sftp-max-backups', 'sftpMaxBackups', '保留份数 (0=不限)', '', { control: 'input', stateKey: 'sftpForm.maxBackups', providerId: 'sftp' }),
        ], { control: 'provider-card', providerId: 'sftp', targetId: 'sync.sftp' }),
      ]),
    ], { targetId: 'sync.webdav' }),
    sectionNode('sync', 'cloud', '云端同步', [
      panelNode('sync.panel.cloud', [
        fieldNode('sync.cloud-backup', 'cloudBackup', '云端同步', '同步将写入 .lumeterm2 加密备份', { control: 'status' }),
        fieldNode('sync.tombstones', 'tombstones', '删除记录', '用于多设备同步删除，一般无需处理。', { control: 'maintenance' }),
        actionNode('sync.merge-sync', 'mergeSync', '合并同步', '', { actionKey: 'syncNow' }),
        actionNode('sync.restore', 'restore', '从云端恢复', '', { actionKey: 'restoreFromCloud' }),
      ]),
    ], { targetId: 'sync.cloud-backup' }),
  ]),
  tabNode('app', '关于', 'Info', [
    sectionNode('app', 'about', '关于', [
      panelNode('app.panel.about', [
        actionNode('app.check-update', 'checkUpdate', '检查更新', '', { actionKey: 'checkUpdate' }),
        actionNode('app.feedback', 'feedback', '反馈问题', '生成预填的 GitHub issue', { actionKey: 'openIssues' }),
        actionNode('app.github', 'github', 'GitHub', '源代码', { actionKey: 'openRepository' }),
        actionNode('app.android', 'android', 'Android 客户端', '', { actionKey: 'openAndroidRepository' }),
        actionNode('app.releases', 'releases', '更新内容', '查看发布说明', { actionKey: 'openReleases' }),
        fieldNode('app.cross-platform', 'crossPlatform', '跨端说明', '本产品为桌面端。Android 客户端独立仓库、分开发版，数据可通过云同步互通。', { control: 'info' }),
        fieldNode('app.contributors', 'contributors', '特别鸣谢', '', { control: 'list' }),
      ]),
    ], { targetId: 'app.check-update' }),
  ]),
);

function buildSettingsRegistry(root: SettingsTreeNode): Record<string, SettingsTabRegistry> {
  const registry: Record<string, SettingsTabRegistry> = {};
  root.children?.forEach((tabItem) => {
    const tabRegistry: SettingsTabRegistry = { node: tabItem, sections: {}, fields: {} };
    const stack: SettingsTreeNode[] = [...(tabItem.children || [])];
    while (stack.length > 0) {
      const current = stack.shift()!;
      if (current.type === 'section' && current.alias) {
        tabRegistry.sections[current.alias] = current;
      }
      if (current.type && ['field', 'field-group', 'option', 'action'].includes(current.type) && current.alias) {
        tabRegistry.fields[current.alias] = current;
      }
      stack.unshift(...(current.children || []));
    }
    registry[tabItem.id || ''] = Object.freeze(tabRegistry);
  });
  return Object.freeze(registry);
}

function buildSearchDefinitions(root: SettingsTreeNode): readonly SettingsSearchDefinition[] {
  const results: SettingsSearchDefinition[] = [];
  const stack: SettingsTreeNode[] = [root];
  while (stack.length > 0) {
    const node = stack.shift()!;
    if (node.type && ['section', 'field', 'field-group', 'option', 'action'].includes(node.type) && node.titleKey) {
      results.push(Object.freeze({
        id: node.id || '',
        type: node.type,
        tab: node.tab || '',
        section: node.section || '',
        providerId: node.providerId || '',
        targetId: node.targetId || node.id || '',
        titleKey: node.titleKey,
        descriptionKey: node.descriptionKey || '',
        breadcrumbTitleKeys: Array.isArray(node.breadcrumbTitleKeys) ? node.breadcrumbTitleKeys : [],
      }) as SettingsSearchDefinition);
    }
    stack.unshift(...(node.children || []));
  }
  return Object.freeze(results);
}

export type SettingsTabId = 'general' | 'network' | 'fileManager' | 'runtimeEnvironment' | 'appearance' | 'shortcuts' | 'sync' | 'app';

const SETTINGS_TREE: SettingsTreeNode = normalizeSettingsTree(settingsTreeSource);
export const settings: Record<SettingsTabId, SettingsTabRegistry> = buildSettingsRegistry(SETTINGS_TREE);
export const SETTINGS_SECTIONS: readonly SettingsTreeNode[] = Object.freeze(Object.values(settings).flatMap((group) => Object.values(group.sections)));
export const SETTINGS_SEARCH_DEFINITIONS: readonly SettingsSearchDefinition[] = Object.freeze(buildSearchDefinitions(SETTINGS_TREE));
