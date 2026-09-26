import { useMemo, useState } from 'react';
import { useTranslation, type I18nKey } from '../../i18n.ts';
import { Switch } from '../ui';

interface ToggleSwitchProps {
  checked: boolean;
  onChange: () => void;
  disabled?: boolean;
}

function ToggleSwitch({ checked, onChange, disabled = false }: ToggleSwitchProps) {
  return <Switch checked={checked} onChange={disabled ? undefined : onChange} disabled={disabled} />;
}

interface MCPToolInfo {
  name: string;
  description?: string;
}

interface MCPAccessViewProps {
  mcpInfo: {
    transport?: string;
    url?: string;
    tools?: MCPToolInfo[];
  };
  configText: string;
  configRows: number;
  title: string;
  titleSize?: number;
  showNotice?: boolean;
  showTools?: boolean;
  mcpEnabled?: boolean;
  mcpAllowBrowserCalls?: boolean;
  mcpRequireApproval?: boolean;
  mcpActivityVisible?: boolean;
  mcpTerminalFollowLatest?: boolean;
  onToggleMcpEnabled: () => void;
  onToggleMcpAllowBrowserCalls: () => void;
  onToggleMcpRequireApproval: () => void;
  onToggleMcpActivityVisible: () => void;
  onToggleMcpTerminalFollowLatest: () => void;
}

export default function MCPAccessView({
  mcpInfo,
  configText,
  configRows,
  title,
  titleSize = 14,
  showNotice = false,
  showTools = false,
  mcpEnabled = true,
  mcpAllowBrowserCalls = false,
  mcpRequireApproval = false,
  mcpActivityVisible = false,
  mcpTerminalFollowLatest = true,
  onToggleMcpEnabled,
  onToggleMcpAllowBrowserCalls,
  onToggleMcpRequireApproval,
  onToggleMcpActivityVisible,
  onToggleMcpTerminalFollowLatest,
}: MCPAccessViewProps) {
  const { t, lang } = useTranslation();

  // 发给外部 AI Agent 的一键配置话术：URL 跟随实际 MCP 地址
  // type 用客户端配置格式（streamable-http → http），与上方 configText 保持一致
  // 前缀文案跟随界面语言翻译，JSON 配置部分保持不变
  const agentPromptText = useMemo(() => {
    const url = mcpInfo.url || 'http://127.0.0.1:5779/mcp';
    const configType = mcpInfo.transport === 'streamable-http' ? 'http' : (mcpInfo.transport || 'http');
    const prefix = t('请帮我配置这个MCP');
    return `${prefix} "lumeterm": {
  "type": "${configType}",
  "url": "${url}",
  "oauth": false,
  "alwaysAllow": [],
  "disabled": false,
  "timeout": 0,
  "disabledForPrompts": false
}`;
  }, [mcpInfo.url, mcpInfo.transport, t, lang]);
  const [agentPromptCopied, setAgentPromptCopied] = useState(false);
  const copyAgentPrompt = () => {
    navigator.clipboard?.writeText(agentPromptText).then(() => {
      setAgentPromptCopied(true);
      window.setTimeout(() => setAgentPromptCopied(false), 1500);
    }).catch(() => {});
  };

  const getToolDescription = (tool: MCPToolInfo) => {
    // 动态 key：mcp.tool.* 为按工具名拼装的键，命中则翻，否则回退工具描述
    const key = `mcp.tool.${tool.name}`;
    const translated = t(key as I18nKey);
    return translated === key ? (tool.description || '-') : translated;
  };

  return (
    <>
      <div className="grid gap-1">
        {/* title 为后端动态描述（可能不在翻译表），t() 内部有兜底 */}
        <div className="font-bold text-primary leading-[1.3]" style={{ fontSize: titleSize }}>{t(title as I18nKey)}</div>
        <div className="text-sm text-tertiary leading-[1.5]">{t('可直接粘贴到支持 streamable-http 的 MCP 客户端配置中')}</div>
        {showNotice && <div className="text-xs text-tertiary leading-[1.5]">{t('该面板可在设置中关闭, 仅影响前端展示层, 不影响 MCP 服务的启动, 监听绑定或生命周期管理.')}</div>}
      </div>
      <div className="grid gap-2.5">
        <div className="p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line flex justify-between items-center gap-4">
          <div className="min-w-0">
            <div className="text-primary text-base font-bold">{t('启用 MCP 服务')}</div>
            <div className="text-tertiary text-sm leading-[1.6]">{t('控制本地 MCP 服务是否监听本机回环地址并提供工具能力')}</div>
          </div>
          <ToggleSwitch checked={mcpEnabled} onChange={onToggleMcpEnabled} />
        </div>
        <div className={`p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line flex justify-between items-center gap-4 ${mcpEnabled ? 'opacity-100' : 'opacity-65'}`}>
          <div className="min-w-0">
            <div className="text-primary text-base font-bold">{t('允许浏览器调用')}</div>
            <div className="text-tertiary text-sm leading-[1.6]">{t('允许带 Origin 的浏览器请求访问本地 MCP 服务。关闭后仅允许无 Origin 的本机客户端调用')}</div>
          </div>
          <ToggleSwitch checked={mcpAllowBrowserCalls} onChange={onToggleMcpAllowBrowserCalls} disabled={!mcpEnabled} />
        </div>
        <div className={`p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line flex justify-between items-center gap-4 ${mcpEnabled ? 'opacity-100' : 'opacity-65'}`}>
          <div className="min-w-0">
            <div className="text-primary text-base font-bold">{t('MCP 终端跟随最新')}</div>
            <div className="text-tertiary text-sm leading-[1.6]">{t('默认开启。同一服务器新开终端后，外部 AI 的命令自动改到最新打开的终端执行，不再滞留旧终端')}</div>
          </div>
          <ToggleSwitch checked={mcpTerminalFollowLatest} onChange={onToggleMcpTerminalFollowLatest} disabled={!mcpEnabled} />
        </div>
        <div className={`p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line flex justify-between items-center gap-4 ${mcpEnabled ? 'opacity-100' : 'opacity-65'}`}>
          <div className="min-w-0">
            <div className="text-primary text-base font-bold">{t('外部 MCP 操作弹窗')}</div>
            <div className="text-tertiary text-sm leading-[1.6]">{t('默认关闭。开启后弹出 MCP 活动弹窗，实时显示外部客户端（如 Claude Code）的操作痕迹：服务器、命令、状态、输出')}</div>
          </div>
          <ToggleSwitch checked={mcpActivityVisible} onChange={onToggleMcpActivityVisible} disabled={!mcpEnabled} />
        </div>
        <div className={`p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line flex justify-between items-center gap-4 ${mcpEnabled ? 'opacity-100' : 'opacity-65'}`}>
          <div className="min-w-0">
            <div className="text-primary text-base font-bold">{t('外部 MCP 写操作需审批')}</div>
            <div className="text-tertiary text-sm leading-[1.6]">{t('开启后，外部客户端的写操作（is_mutating）需在活动弹窗中手动批准才执行（会同时开启活动弹窗）。关闭则自动执行。')}</div>
          </div>
          <ToggleSwitch checked={mcpRequireApproval} onChange={onToggleMcpRequireApproval} disabled={!mcpEnabled} />
        </div>
      </div>
      <div className="grid gap-2.5 p-3.5 bg-canvas border border-line rounded-[var(--radius-md)]">
        <div className="grid gap-1">
          <div className="text-xs text-tertiary">{t('传输方式')}</div>
          <div className="text-base font-semibold text-primary">{mcpInfo.transport || 'streamable-http'}</div>
        </div>
        <div className="grid gap-1">
          <div className="text-xs text-tertiary">{t('MCP 地址')}</div>
          <div className="text-sm text-primary break-all font-mono">{mcpInfo.url || '-'}</div>
        </div>
      </div>
      <div className="p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line">
        <div className="text-xs text-tertiary mb-2">{t('MCP 配置片段')}</div>
        <textarea
          id="mcp-access-config"
          name="mcp-access-config"
          readOnly
          value={configText}
          rows={configRows}
          spellCheck={false}
          className="w-full resize-none overflow-x-auto overflow-y-hidden whitespace-pre border border-line rounded-[var(--radius-sm)] bg-raised text-primary px-3 py-2 box-border text-sm leading-[19px] font-mono outline-none block"
          style={{ height: `${configRows * 19 + 18}px` }}
        />
      </div>
      <div className="p-3.5 rounded-[var(--radius-md)] bg-canvas border border-line">
        <div className="flex items-center gap-2 mb-2">
          <div className="text-xs text-tertiary flex-1 min-w-0">{t('您可以将这一句话发送给您的 AI Agent')}</div>
          <button
            type="button"
            onClick={copyAgentPrompt}
            className={`text-xs py-[3px] px-2.5 rounded-[var(--radius-sm)] border border-line text-secondary cursor-pointer whitespace-nowrap transition-colors duration-[80ms] ${agentPromptCopied ? 'bg-raised' : 'bg-overlay'}`}
          >
            {agentPromptCopied ? t('已复制') : t('复制')}
          </button>
        </div>
        <div className="border border-line rounded-[var(--radius-sm)] bg-raised px-3 py-2 text-sm leading-[19px] font-mono text-primary whitespace-pre-wrap break-all max-h-40 overflow-y-auto">
          {agentPromptText}
        </div>
      </div>
      {showTools && (
        <div className="p-3 rounded-lg bg-overlay border border-line flex flex-col">
          <div className="text-xs text-tertiary mb-2">{t('本机MCP工具和用途')}</div>
          <div className="grid gap-2">
            {Array.isArray(mcpInfo.tools) && mcpInfo.tools.length > 0 ? mcpInfo.tools.map((tool) => (
              <div key={tool.name} className="p-2.5 rounded-lg border border-line bg-canvas">
                <div className="text-sm font-bold text-primary mb-1 font-mono break-all">{tool.name}</div>
                <div className="text-sm text-secondary leading-[1.7] break-words">{getToolDescription(tool)}</div>
              </div>
            )) : (
              <div className="text-sm text-tertiary">{t('暂无工具信息')}</div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
