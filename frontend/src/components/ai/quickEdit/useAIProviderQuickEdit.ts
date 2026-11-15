import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation, type I18nKey } from '../../../i18n.ts';
import {
  getAIGlobalSettings,
  saveAIGlobalSettings,
  type AISystemPromptPreset,
} from '../aiGlobalSettingsBridge.ts';
import { getAIProviderPromptCachePolicy, normalizeAIProviderCustomHeaders, type AIProviderPromptCachePolicy } from '../aiProviderBridge.ts';
import {
  availableAIProviders,
  canUseDedicatedWebSearchCandidate,
  getAIProviderDefinition,
} from '../providers/index.ts';
import {
  aiChannelPresets,
  customChannelPresetValue,
  getAIChannelPreset,
  matchAIChannelPresetByBaseUrl,
  type AIChannelPreset,
} from '../providers/channelPresets.ts';
import type { AIProviderLike } from '../AIProviderSelector.tsx';
import {
  buildDisplayModelCapability,
  buildDraft,
  buildInitialModelOptions,
  buildReasoningOptionsForCapability,
  defaultCacheOptions,
  DEFAULT_MAX_OUTPUT_TOKENS,
  DEFAULT_MAX_THINKING_TOKENS,
  getAppBridge,
  getProviderDisplayLabel,
  normalizeOptionalNumber,
  normalizePositiveInteger,
  type ProviderDraft,
  resolveEffortReasoningSelection,
  responsePromptCacheStrategyLabelKeys,
  selfWebSearchProviderValue,
} from './quickEditTypes.ts';

export interface UseAIProviderQuickEditOptions {
  open: boolean;
  provider?: AIProviderLike | null;
  providers?: AIProviderLike[];
  onSave?: (draft: Record<string, unknown>) => void | Promise<void>;
}

export function buildChannelPresetModelOptions(preset: AIChannelPreset): string[] {
  const models = preset.models.filter((model) => typeof model === 'string' && model.trim());
  const defaultModel = preset.defaultModel.trim();
  if (defaultModel && !models.includes(defaultModel)) {
    return [defaultModel, ...models];
  }
  return models;
}

export function isPresetDerivedName(name: unknown): boolean {
  const nextName = typeof name === 'string' ? name.trim() : '';
  return Boolean(nextName) && aiChannelPresets.some((preset) => preset.label === nextName);
}

export function useAIProviderQuickEdit({
  open,
  provider,
  providers = [],
  onSave,
}: UseAIProviderQuickEditOptions) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<ProviderDraft>(buildDraft());
  const [modelQuery, setModelQuery] = useState('');
  const [modelOptions, setModelOptions] = useState<string[]>(buildInitialModelOptions(getAIProviderDefinition('Compatible'), ''));
  const [modelRefreshError, setModelRefreshError] = useState('');
  const [modelRefreshing, setModelRefreshing] = useState(false);
  const [providerMenuOpen, setProviderMenuOpen] = useState(false);
  const [channelPreset, setChannelPreset] = useState(customChannelPresetValue);
  const [channelPresetMenuOpen, setChannelPresetMenuOpen] = useState(false);
  const [dedicatedProviderMenuOpen, setDedicatedProviderMenuOpen] = useState(false);
  const [dedicatedProviderSearch, setDedicatedProviderSearch] = useState('');
  const [validatingWebSearch, setValidatingWebSearch] = useState(false);
  const [webSearchValidationMessage, setWebSearchValidationMessage] = useState('');
  const [webSearchValidationPassed, setWebSearchValidationPassed] = useState(false);
  const [proxyNodes, setProxyNodes] = useState<Array<{ id?: string; name?: string; type?: string; host?: string; port?: number }>>([]);
  const [systemPromptPresets, setSystemPromptPresets] = useState<AISystemPromptPreset[]>([]);
  const [systemPromptPresetsSaving, setSystemPromptPresetsSaving] = useState(false);
  const [systemPromptPresetsError, setSystemPromptPresetsError] = useState('');
  const [proxyMenuOpen, setProxyMenuOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<'basic' | 'advanced'>('basic');
  const [modelPromptCachePolicy, setModelPromptCachePolicy] = useState<AIProviderPromptCachePolicy | null>(null);
  const providerFieldRef = useRef<HTMLDivElement | null>(null);
  const channelPresetFieldRef = useRef<HTMLDivElement | null>(null);
  const dedicatedProviderFieldRef = useRef<HTMLDivElement | null>(null);
  const dedicatedProxyFieldRef = useRef<HTMLDivElement | null>(null);
  const autoRefreshTimerRef = useRef<number | null>(null);
  const lastAutoRefreshKeyRef = useRef('');

  const providerDefinition = useMemo(
    () => getAIProviderDefinition(draft.provider),
    [draft.provider],
  );

  const providerOptions = useMemo(
    () => availableAIProviders.map((item: { value: string; label?: string }) => ({
      value: item.value,
      label: getProviderDisplayLabel(item, t),
    })),
    [t],
  );

  const channelPresetOptions = useMemo(
    () => [
      { value: customChannelPresetValue, label: t('自定义') },
      ...aiChannelPresets.map((preset) => ({ value: preset.value, label: preset.label })),
    ],
    [t],
  );

  const activeChannelPreset = useMemo(
    () => getAIChannelPreset(channelPreset),
    [channelPreset],
  );

  const modelCapability = useMemo(() => {
    const baseCapability = providerDefinition.getModelCapability(draft.model || '');
    return buildDisplayModelCapability(draft.provider, baseCapability);
  }, [draft.provider, providerDefinition, draft.model]);

  const effortReasoningOptions = useMemo(
    () => buildReasoningOptionsForCapability(modelCapability),
    [modelCapability],
  );

  const currentEffortReasoningSelection = useMemo(
    () => resolveEffortReasoningSelection(draft, modelCapability),
    [draft, modelCapability],
  );

  const resolvedMaxTokens = useMemo(
    () => normalizePositiveInteger(draft.modelMaxTokens, modelCapability.maxTokens || DEFAULT_MAX_OUTPUT_TOKENS),
    [draft.modelMaxTokens, modelCapability.maxTokens],
  );

  const maxThinkingTokenLimit = useMemo(
    () => Math.max(1024, Math.floor(resolvedMaxTokens * 0.8)),
    [resolvedMaxTokens],
  );

  const resolvedThinkingTokens = useMemo(() => {
    const fallbackValue = modelCapability.maxThinkingTokens || DEFAULT_MAX_THINKING_TOKENS;
    return Math.min(
      normalizePositiveInteger(draft.modelMaxThinkingTokens, fallbackValue),
      maxThinkingTokenLimit,
    );
  }, [draft.modelMaxThinkingTokens, maxThinkingTokenLimit, modelCapability.maxThinkingTokens]);

  const supportsPromptCacheSettings = providerDefinition.supportsPromptCacheSettings === true;
  const usePromptCacheRetention =
    providerDefinition.value === 'Responses' && draft.openAiResponsesUsePromptCacheRetention === true;
  const activeModelPromptCachePolicy = useMemo(() => {
    const currentModelId = draft.model.trim().toLowerCase();
    if (!currentModelId || modelPromptCachePolicy?.modelId.trim().toLowerCase() !== currentModelId) {
      return null;
    }
    return modelPromptCachePolicy;
  }, [draft.model, modelPromptCachePolicy]);
  const responsePromptCacheOptionsReady =
    providerDefinition.value !== 'Responses' || (activeModelPromptCachePolicy?.availableFormats?.length ?? 0) > 0;
  const promptCacheOptions = useMemo(() => {
    if (!supportsPromptCacheSettings) {
      return [] as Array<{ value: string; labelKey: I18nKey }>;
    }
    if (providerDefinition.value === 'Responses') {
      const format = usePromptCacheRetention ? 'prompt_cache_retention' : 'prompt_cache_options';
      const durations = activeModelPromptCachePolicy?.availableFormats
        .find((option) => option.format === format)
        ?.durations || [];
      return ['off', 'model', ...durations].map((value) => ({
        value,
        labelKey: responsePromptCacheStrategyLabelKeys[value] || (value as I18nKey),
      }));
    }
    return defaultCacheOptions;
  }, [activeModelPromptCachePolicy, providerDefinition, supportsPromptCacheSettings, usePromptCacheRetention]);
  const selectedPromptCacheStrategy = useMemo(() => {
    const values = promptCacheOptions.map((option) => option.value);
    if (providerDefinition.value === 'Responses' && !responsePromptCacheOptionsReady && draft.cacheStrategy) {
      return draft.cacheStrategy;
    }
    if (values.includes(draft.cacheStrategy)) {
      return draft.cacheStrategy;
    }
    if (values.includes('model')) {
      return 'model';
    }
    return values[0] || 'model';
  }, [draft.cacheStrategy, promptCacheOptions, providerDefinition.value, responsePromptCacheOptionsReady]);
  const promptCacheOfficialSupport = useMemo(() => {
    if (providerDefinition.value !== 'Responses' || !/^gpt-/i.test(draft.model.trim())) {
      return '';
    }
    if (!activeModelPromptCachePolicy?.known || !activeModelPromptCachePolicy.format || activeModelPromptCachePolicy.supportedDurations.length === 0) {
      return t('当前模型暂无已维护的官方缓存时长');
    }
    const defaultDuration = activeModelPromptCachePolicy.defaultDuration
      ? `,${t('模型默认')}:${activeModelPromptCachePolicy.defaultDuration}`
      : '';
    return `${t('当前模型官方支持')}:${activeModelPromptCachePolicy.format}=${activeModelPromptCachePolicy.supportedDurations.join('/')}${defaultDuration}`;
  }, [activeModelPromptCachePolicy, draft.model, providerDefinition.value, t]);
  const supportsWebSearch = providerDefinition.supportsWebSearch === true;
  const dedicatedProviderOptions = useMemo(
    () => ([
      { value: selfWebSearchProviderValue, label: t('自身') },
      ...providers
        .filter((item) => item.id !== draft.id)
        .filter((item) => canUseDedicatedWebSearchCandidate(item.provider))
        .map((item) => ({
          value: item.id || '',
          label: item.model ? `${item.name || ''} · ${item.model}` : (item.name || ''),
        })),
    ]),
    [providers, draft.id, t],
  );

  const filteredDedicatedProviderOptions = useMemo(() => {
    const keyword = dedicatedProviderSearch.trim().toLowerCase();
    if (!keyword) {
      return dedicatedProviderOptions;
    }
    return dedicatedProviderOptions.filter((item) => item.label.toLowerCase().includes(keyword));
  }, [dedicatedProviderOptions, dedicatedProviderSearch]);

  const currentDedicatedProviderOption = useMemo(
    () => dedicatedProviderOptions.find((item) => item.value === draft.dedicatedWebSearchProviderId) || dedicatedProviderOptions[0] || null,
    [dedicatedProviderOptions, draft.dedicatedWebSearchProviderId],
  );

  const dedicatedProxyOptions = useMemo(() => ([
    { value: '', label: t('不使用') },
    ...proxyNodes.map((node) => ({
      value: node.id || '',
      label: [
        node.name || t('未命名节点'),
        `${node.type === 'http' || node.type === 'https' ? node.type : 'socks5'}://${node.host}:${node.port}`,
      ].join(' · '),
    })),
  ]), [proxyNodes, t]);

  const currentDedicatedProxyOption = useMemo(
    () => dedicatedProxyOptions.find((item) => item.value === draft.dedicatedProxyId) || dedicatedProxyOptions[0] || null,
    [dedicatedProxyOptions, draft.dedicatedProxyId],
  );

  const selectedWebSearchProviderValue = currentDedicatedProviderOption?.value || selfWebSearchProviderValue;
  const usingDedicatedWebSearchProvider = selectedWebSearchProviderValue !== selfWebSearchProviderValue;
  const canValidateWebSearch = draft.webSearchEnabled && (
    usingDedicatedWebSearchProvider
      ? Boolean(selectedWebSearchProviderValue)
      : Boolean(draft.baseUrl && draft.apiKey && draft.model)
  );

  const refreshModelsWithCredentials = async (providerValue: string, baseUrlValue: string, apiKeyValue: string, selectedModel = '') => {
    const trimmedProvider = typeof providerValue === 'string' ? providerValue.trim() : '';
    const trimmedBaseUrl = typeof baseUrlValue === 'string' ? baseUrlValue.trim() : '';
    const trimmedApiKey = typeof apiKeyValue === 'string' ? apiKeyValue.trim() : '';

    if (!trimmedBaseUrl || !trimmedApiKey) {
      setModelRefreshError('');
      return false;
    }

    const refreshKey = `${trimmedProvider}::${trimmedBaseUrl}::${trimmedApiKey}::${JSON.stringify(draft.customHeaders)}`;
    lastAutoRefreshKeyRef.current = refreshKey;
    setModelRefreshing(true);
    setModelRefreshError('');

    const bridge = getAppBridge();
    if (!bridge?.RequestAIProviderModels) {
      setModelRefreshing(false);
      setModelRefreshError(t('当前环境不支持刷新模型'));
      return false;
    }

    try {
      const requestProfile = {
        ...draft,
        provider: trimmedProvider || draft.provider,
        baseUrl: trimmedBaseUrl,
        apiKey: trimmedApiKey,
        model: selectedModel || draft.model,
        dedicatedProxyEnabled: Boolean(draft.dedicatedProxyEnabled),
        dedicatedProxyId: draft.dedicatedProxyId || '',
      };
      const models = bridge?.RequestAIProviderModelsWithProfile
        ? await bridge.RequestAIProviderModelsWithProfile(JSON.stringify(requestProfile))
        : await bridge.RequestAIProviderModels(trimmedBaseUrl, trimmedApiKey);
      const normalizedModels = Array.isArray(models)
        ? models.filter((item) => typeof item === 'string' && item.trim()).map((item) => item.trim())
        : [];

      // ponytail: race guard — 用户切换 preset/provider/baseUrl 后丢弃旧请求结果
      if (refreshKey !== lastAutoRefreshKeyRef.current) {
        return false;
      }

      if (normalizedModels.length === 0) {
        throw new Error(t('未获取到任何模型'));
      }

      const nextModels = selectedModel && !normalizedModels.includes(selectedModel)
        ? [selectedModel, ...normalizedModels]
        : normalizedModels;

      setModelOptions(nextModels);
      return true;
    } catch (error) {
      // ponytail: race guard — 与 try 块对称，旧请求的失败也不应覆盖新状态
      if (refreshKey !== lastAutoRefreshKeyRef.current) {
        return false;
      }
      const fallbackPreset = matchAIChannelPresetByBaseUrl(trimmedBaseUrl);
      const selectedModelId = selectedModel || draft.model;
      if (fallbackPreset) {
        const presetModels = buildChannelPresetModelOptions(fallbackPreset);
        setModelOptions(selectedModelId && !presetModels.includes(selectedModelId)
          ? [selectedModelId, ...presetModels]
          : presetModels);
      } else {
        setModelOptions(buildInitialModelOptions(getAIProviderDefinition(trimmedProvider || draft.provider), selectedModelId));
      }
      setModelRefreshError(error instanceof Error ? error.message : t('刷新模型失败'));
      return false;
    } finally {
      setModelRefreshing(false);
    }
  };

  useEffect(() => {
    if (!open) {
      return;
    }
    const initialDraft = buildDraft(provider);
    const initialProviderDefinition = getAIProviderDefinition(initialDraft.provider);
    setDraft(initialDraft);
    setModelOptions(buildInitialModelOptions(initialProviderDefinition, initialDraft.model));
    setModelRefreshError('');
    setModelRefreshing(false);
    setModelQuery('');
    setProviderMenuOpen(false);
    setChannelPreset(matchAIChannelPresetByBaseUrl(initialDraft.baseUrl)?.value || customChannelPresetValue);
    setChannelPresetMenuOpen(false);
    setDedicatedProviderMenuOpen(false);
    setDedicatedProviderSearch('');
    setProxyMenuOpen(false);
    setActiveTab('basic');
    setValidatingWebSearch(false);
    setWebSearchValidationMessage('');
    setWebSearchValidationPassed(false);
    setSystemPromptPresetsSaving(false);
    setSystemPromptPresetsError('');
    getAIGlobalSettings()
      .then((settings) => {
        const nextProxyNodes = Array.isArray(settings?.proxyNodes) ? settings.proxyNodes : [];
        setProxyNodes(nextProxyNodes);
        setSystemPromptPresets(Array.isArray(settings?.systemPromptPresets) ? settings.systemPromptPresets : []);
      })
      .catch(() => {
        setProxyNodes([]);
        setSystemPromptPresets([]);
      });
    if (initialDraft.baseUrl.trim() && initialDraft.apiKey.trim()) {
      void refreshModelsWithCredentials(initialDraft.provider, initialDraft.baseUrl, initialDraft.apiKey, initialDraft.model);
    } else {
      lastAutoRefreshKeyRef.current = '';
    }
  }, [open, provider]);

  useEffect(() => {
    let cancelled = false;
    if (!open || providerDefinition.value !== 'Responses' || !draft.model.trim()) {
      setModelPromptCachePolicy(null);
      return () => {
        cancelled = true;
      };
    }
    void getAIProviderPromptCachePolicy(draft.model).then((policy) => {
      if (!cancelled) {
        setModelPromptCachePolicy(policy);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [draft.model, open, providerDefinition.value]);

  useEffect(() => {
    if (!open) return undefined;
    const handler = (event: Event) => {
      const newProxyNodes = (event as CustomEvent<unknown>).detail;
      if (Array.isArray(newProxyNodes)) setProxyNodes(newProxyNodes as Array<{ id?: string; name?: string; type?: string; host?: string; port?: number }>);
    };
    window.addEventListener('lumin:proxy-nodes-changed', handler);
    return () => window.removeEventListener('lumin:proxy-nodes-changed', handler);
  }, [open]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const matchedPreset = matchAIChannelPresetByBaseUrl(draft.baseUrl);
    const matchedValue = matchedPreset?.value || customChannelPresetValue;
    setChannelPreset((prev) => (getAIChannelPreset(prev) || customChannelPresetValue) === matchedValue ? prev : matchedValue);
  }, [open, draft.baseUrl]);

  useEffect(() => {
    if (!providerMenuOpen && !channelPresetMenuOpen && !dedicatedProviderMenuOpen && !proxyMenuOpen) {
      return undefined;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (providerFieldRef.current && !providerFieldRef.current.contains(event.target as Node)) {
        setProviderMenuOpen(false);
      }
      if (channelPresetFieldRef.current && !channelPresetFieldRef.current.contains(event.target as Node)) {
        setChannelPresetMenuOpen(false);
      }
      if (dedicatedProviderFieldRef.current && !dedicatedProviderFieldRef.current.contains(event.target as Node)) {
        setDedicatedProviderMenuOpen(false);
      }
      if (dedicatedProxyFieldRef.current && !dedicatedProxyFieldRef.current.contains(event.target as Node)) {
        setProxyMenuOpen(false);
      }
    };

    window.addEventListener('pointerdown', handlePointerDown);
    return () => window.removeEventListener('pointerdown', handlePointerDown);
  }, [providerMenuOpen, channelPresetMenuOpen, dedicatedProviderMenuOpen, proxyMenuOpen]);

  useEffect(() => {
    setWebSearchValidationMessage('');
    setWebSearchValidationPassed(false);
  }, [
    draft.provider,
    draft.baseUrl,
    draft.apiKey,
    draft.model,
    draft.webSearchEnabled,
    draft.dedicatedWebSearchEnabled,
    draft.dedicatedWebSearchProviderId,
  ]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const trimmedBaseUrl = draft.baseUrl.trim();
    const trimmedApiKey = draft.apiKey.trim();

    if (!trimmedBaseUrl || !trimmedApiKey) {
      lastAutoRefreshKeyRef.current = '';
      if (autoRefreshTimerRef.current) {
        window.clearTimeout(autoRefreshTimerRef.current);
      }
      return undefined;
    }

    const refreshKey = `${draft.provider.trim()}::${trimmedBaseUrl}::${trimmedApiKey}::${JSON.stringify(draft.customHeaders)}`;
    if (refreshKey === lastAutoRefreshKeyRef.current) {
      return undefined;
    }

    if (autoRefreshTimerRef.current) {
      window.clearTimeout(autoRefreshTimerRef.current);
    }

    autoRefreshTimerRef.current = window.setTimeout(() => {
      void refreshModelsWithCredentials(draft.provider, trimmedBaseUrl, trimmedApiKey, draft.model);
    }, 1000);

    return () => {
      if (autoRefreshTimerRef.current) {
        window.clearTimeout(autoRefreshTimerRef.current);
      }
    };
  }, [open, draft.provider, draft.baseUrl, draft.apiKey, draft.model, draft.customHeaders]);

  const filteredModels = useMemo(() => {
    const keyword = modelQuery.trim().toLowerCase();
    if (!keyword) {
      return modelOptions;
    }
    return modelOptions.filter((item) => item.toLowerCase().includes(keyword));
  }, [modelOptions, modelQuery]);

  const handleProviderSelect = (nextProvider: string) => {
    const nextProviderDefinition = getAIProviderDefinition(nextProvider);
    setDraft((prev) => {
      const nextModel = typeof prev.model === 'string' ? prev.model.trim() : '';
      const nextCapability = nextProviderDefinition.getModelCapability(nextModel);
      return {
        ...prev,
        provider: nextProviderDefinition.value,
        model: nextModel,
        cacheStrategy: nextProviderDefinition.value === 'Responses' && prev.provider !== 'Responses'
          ? 'model'
          : (prev.cacheStrategy || '5m'),
        reasoningEffort: prev.reasoningEffort || (typeof nextCapability.reasoningEffort === 'string' ? nextCapability.reasoningEffort : '') || 'disable',
        enableReasoningEffort: nextCapability.requiredReasoningBudget || nextCapability.requiredReasoningEffort
          ? true
          : prev.enableReasoningEffort,
        modelMaxTokens: prev.modelMaxTokens || nextCapability.maxTokens || DEFAULT_MAX_OUTPUT_TOKENS,
        modelMaxThinkingTokens: prev.modelMaxThinkingTokens || nextCapability.maxThinkingTokens || DEFAULT_MAX_THINKING_TOKENS,
      };
    });
    setModelOptions(buildInitialModelOptions(nextProviderDefinition, typeof draft.model === 'string' ? draft.model.trim() : ''));
    setModelQuery('');
    setProviderMenuOpen(false);
  };
  const handleChannelPresetSelect = (value: string) => {
    setChannelPresetMenuOpen(false);
    const preset = getAIChannelPreset(value);
    if (!preset) {
      setChannelPreset(customChannelPresetValue);
      return;
    }
    setChannelPreset(preset.value);
    const presetProviderDef = getAIProviderDefinition(preset.provider);
    const presetCapability = presetProviderDef.getModelCapability(preset.defaultModel);
    setDraft((prev) => ({
      ...prev,
      name: !prev.name.trim() || isPresetDerivedName(prev.name) ? preset.label : prev.name,
      provider: preset.provider,
      baseUrl: preset.baseUrl,
      model: preset.defaultModel,
      cacheStrategy: preset.cacheStrategy || (preset.provider === 'Responses' ? 'model' : '5m'),
      reasoningEffort: presetCapability.reasoningEffort || 'disable',
      enableReasoningEffort: presetCapability.requiredReasoningBudget || presetCapability.requiredReasoningEffort,
      modelMaxTokens: presetCapability.maxTokens || DEFAULT_MAX_OUTPUT_TOKENS,
      modelMaxThinkingTokens: presetCapability.maxThinkingTokens || DEFAULT_MAX_THINKING_TOKENS,
    }));
    setModelOptions(buildChannelPresetModelOptions(preset));
    setModelQuery('');
    setModelRefreshError('');
  };

  const handleWebSearchProviderSelect = (nextProviderId: string) => {
    const normalizedProviderId = dedicatedProviderOptions.some((item) => item.value === nextProviderId)
      ? nextProviderId
      : selfWebSearchProviderValue;
    setDraft((prev) => ({
      ...prev,
      dedicatedWebSearchEnabled: normalizedProviderId !== selfWebSearchProviderValue,
      dedicatedWebSearchProviderId: normalizedProviderId,
    }));
    setDedicatedProviderMenuOpen(false);
    setDedicatedProviderSearch('');
  };

  const handleWebSearchToggle = () => {
    setDraft((prev) => ({
      ...prev,
      webSearchEnabled: !prev.webSearchEnabled,
    }));
  };

  const handleRefreshModels = async () => {
    const trimmedBaseUrl = draft.baseUrl.trim();
    const trimmedApiKey = draft.apiKey.trim();

    if (!trimmedBaseUrl) {
      setModelRefreshError(t('请先填写基础 URL'));
      return;
    }

    if (!trimmedApiKey) {
      setModelRefreshError(t('请先填写 API 密钥'));
      return;
    }

    await refreshModelsWithCredentials(draft.provider, trimmedBaseUrl, trimmedApiKey, draft.model);
  };

  const handleValidateWebSearch = async () => {
    if (!canValidateWebSearch || validatingWebSearch) {
      return;
    }
    const bridge = getAppBridge();
    if (!bridge?.ValidateAIProviderWebSearch) {
      setWebSearchValidationPassed(false);
      setWebSearchValidationMessage(t('不支持'));
      return;
    }

    setValidatingWebSearch(true);
    setWebSearchValidationPassed(false);
    setWebSearchValidationMessage('');

    try {
      const resolvedWebSearchProviderValue = dedicatedProviderOptions.some((item) => item.value === draft.dedicatedWebSearchProviderId)
        ? draft.dedicatedWebSearchProviderId
        : selfWebSearchProviderValue;
      const useDedicatedWebSearchProvider = resolvedWebSearchProviderValue !== selfWebSearchProviderValue;
      const result = await bridge.ValidateAIProviderWebSearch(JSON.stringify({
        ...draft,
        provider: providerDefinition.value,
        model: draft.model?.trim() || '',
        webSearchEnabled: draft.webSearchEnabled,
        dedicatedWebSearchEnabled: useDedicatedWebSearchProvider,
        dedicatedWebSearchProviderId: useDedicatedWebSearchProvider ? resolvedWebSearchProviderValue : '',
        reasoningEffort: draft.reasoningEffort || 'disable',
        enableReasoningEffort: Boolean(draft.enableReasoningEffort),
        modelMaxTokens: normalizePositiveInteger(draft.modelMaxTokens),
        modelMaxThinkingTokens: normalizePositiveInteger(draft.modelMaxThinkingTokens),
        openAiLegacyReasoningFormatEnabled: draft.openAiLegacyReasoningFormatEnabled === true,
      }));
      const passed = result?.success === true;
      setWebSearchValidationPassed(passed);
      setWebSearchValidationMessage(passed ? t('支持') : t('不支持'));
    } catch {
      setWebSearchValidationPassed(false);
      setWebSearchValidationMessage(t('不支持'));
    } finally {
      setValidatingWebSearch(false);
    }
  };

  const handleSystemPromptPresetsChange = async (nextPresets: AISystemPromptPreset[]) => {
    if (systemPromptPresetsSaving) {
      return;
    }
    setSystemPromptPresetsSaving(true);
    setSystemPromptPresetsError('');
    try {
      const settings = await getAIGlobalSettings();
      const savedSettings = await saveAIGlobalSettings({
        ...settings,
        systemPromptPresets: nextPresets,
      });
      setSystemPromptPresets(savedSettings.systemPromptPresets);
    } catch (error) {
      setSystemPromptPresetsError(error instanceof Error ? error.message : t('系统提示词预设保存失败'));
      throw error;
    } finally {
      setSystemPromptPresetsSaving(false);
    }
  };

  const handleSave = () => {
    let reasoningEffort = draft.reasoningEffort || 'disable';
    let enableReasoningEffort = Boolean(draft.enableReasoningEffort);
    let modelMaxTokens = normalizePositiveInteger(draft.modelMaxTokens);
    let modelMaxThinkingTokens = normalizePositiveInteger(draft.modelMaxThinkingTokens);

    switch (modelCapability.reasoningMode) {
      case 'binary':
        reasoningEffort = 'disable';
        modelMaxTokens = 0;
        modelMaxThinkingTokens = 0;
        break;
      case 'effort': {
        const nextSelection = currentEffortReasoningSelection;
        reasoningEffort = nextSelection;
        enableReasoningEffort = nextSelection !== 'disable';
        modelMaxTokens = 0;
        modelMaxThinkingTokens = 0;
        break;
      }
      case 'budget':
        reasoningEffort = 'disable';
        enableReasoningEffort = modelCapability.requiredReasoningBudget ? true : Boolean(draft.enableReasoningEffort);
        if (!enableReasoningEffort && !modelCapability.requiredReasoningBudget) {
          modelMaxTokens = 0;
          modelMaxThinkingTokens = 0;
        } else {
          modelMaxTokens = resolvedMaxTokens;
          modelMaxThinkingTokens = resolvedThinkingTokens;
        }
        break;
      default:
        reasoningEffort = 'disable';
        enableReasoningEffort = false;
        modelMaxTokens = 0;
        modelMaxThinkingTokens = 0;
        break;
    }

    const resolvedWebSearchProviderValue = dedicatedProviderOptions.some((item) => item.value === draft.dedicatedWebSearchProviderId)
      ? draft.dedicatedWebSearchProviderId
      : selfWebSearchProviderValue;
    const useDedicatedWebSearchProvider = resolvedWebSearchProviderValue !== selfWebSearchProviderValue;

    onSave?.({
      ...draft,
      provider: providerDefinition.value,
      cacheStrategy: selectedPromptCacheStrategy,
      openAiResponsesUsePromptCacheRetention: providerDefinition.value === 'Responses' && draft.openAiResponsesUsePromptCacheRetention === true,
      modelTemperature: normalizeOptionalNumber(draft.modelTemperature),
      modelTopP: normalizeOptionalNumber(draft.modelTopP),
      customHeaders: normalizeAIProviderCustomHeaders(draft.customHeaders),
      systemPromptAppend: draft.systemPromptAppend.replace(/\r\n/g, '\n').trim(),
      systemPromptPresetId: draft.systemPromptPresetId.trim(),
      webSearchEnabled: draft.webSearchEnabled,
      dedicatedWebSearchEnabled: useDedicatedWebSearchProvider,
      dedicatedWebSearchProviderId: useDedicatedWebSearchProvider ? resolvedWebSearchProviderValue : '',
      dedicatedProxyEnabled: draft.dedicatedProxyEnabled,
      dedicatedProxyId: draft.dedicatedProxyEnabled ? draft.dedicatedProxyId : '',
      reasoningEffort,
      enableReasoningEffort,
      openAiLegacyReasoningFormatEnabled: draft.openAiLegacyReasoningFormatEnabled === true,
      modelMaxTokens,
      modelMaxThinkingTokens,
    });
  };

  return {
    draft,
    setDraft,
    systemPromptPresets,
    systemPromptPresetsSaving,
    systemPromptPresetsError,
    handleSystemPromptPresetsChange,
    modelQuery,
    setModelQuery,
    modelOptions,
    setModelOptions,
    modelRefreshError,
    modelRefreshing,
    providerMenuOpen,
    setProviderMenuOpen,
    channelPreset,
    channelPresetOptions,
    channelPresetMenuOpen,
    setChannelPresetMenuOpen,
    channelPresetFieldRef,
    activeChannelPreset,
    handleChannelPresetSelect,
    dedicatedProviderMenuOpen,
    setDedicatedProviderMenuOpen,
    dedicatedProviderSearch,
    setDedicatedProviderSearch,
    validatingWebSearch,
    webSearchValidationMessage,
    webSearchValidationPassed,
    proxyMenuOpen,
    setProxyMenuOpen,
    activeTab,
    setActiveTab,
    providerFieldRef,
    dedicatedProviderFieldRef,
    dedicatedProxyFieldRef,
    providerDefinition,
    providerOptions,
    modelCapability,
    effortReasoningOptions,
    currentEffortReasoningSelection,
    resolvedMaxTokens,
    maxThinkingTokenLimit,
    resolvedThinkingTokens,
    supportsPromptCacheSettings,
    usePromptCacheRetention,
    activeModelPromptCachePolicy,
    responsePromptCacheOptionsReady,
    promptCacheOptions,
    selectedPromptCacheStrategy,
    promptCacheOfficialSupport,
    supportsWebSearch,
    dedicatedProviderOptions,
    filteredDedicatedProviderOptions,
    currentDedicatedProviderOption,
    dedicatedProxyOptions,
    currentDedicatedProxyOption,
    selectedWebSearchProviderValue,
    usingDedicatedWebSearchProvider,
    canValidateWebSearch,
    filteredModels,
    handleProviderSelect,
    handleWebSearchProviderSelect,
    handleWebSearchToggle,
    handleRefreshModels,
    handleValidateWebSearch,
    handleSave,
  };
}
