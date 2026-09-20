import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from '../../../i18n.ts';
import { getAIProviderState, normalizeAIProviderCustomHeaders, normalizeAIProviderState, saveAIProviderState } from '../aiProviderBridge.ts';
import { getAIProviderDefinition } from '../providers/index.ts';
import {
  buildDisplayModelCapability,
  buildProviderCopyName,
  buildProviderModelOptions,
  buildReasoningOptions,
  extractAIProviderBalanceValue,
  formatAIProviderBalanceDeltaLabel,
  formatAIProviderBalanceLabel,
  getApiKeyPreview,
  getAppBridge,
  getCacheStrategyLabel,
  getProviderModelSummary,
  getReasoningEffortLabel,
  isAIProviderBalanceLabelEnabled,
  normalizeOptionalNumber,
  parseAIProviderBalanceNumber,
  ADAPTIVE_MODEL_MIN_WIDTH,
  ADAPTIVE_PROVIDER_MIN_WIDTH,
  resolveAdaptiveLabelLayout,
  resolveAdaptiveSelectorAvailableWidth,
  resolveAIProviderBaseOrigin,
  sortProviders,
  summaryTooltipDelay,
  type AIProviderLike,
  type RectLike,
} from './providerSelectorTypes.ts';

export interface UseAIProviderSelectorOptions {
  providers?: AIProviderLike[];
  currentProviderId?: string;
  onCurrentProviderChange?: (providerId: string) => Promise<void> | void;
  balanceRefreshSignal?: number;
  persistSelectedProviderId?: boolean;
  dismissSignal?: number;
}

const EMPTY_PROVIDERS: AIProviderLike[] = [];

export function useAIProviderSelector({
  providers = EMPTY_PROVIDERS,
  currentProviderId,
  onCurrentProviderChange,
  balanceRefreshSignal = 0,
  persistSelectedProviderId = true,
  dismissSignal = 0,
}: UseAIProviderSelectorOptions) {
  const { t, lang } = useTranslation();
  const providersRef = useRef(providers);
  providersRef.current = providers;
  const containerRef = useRef<HTMLDivElement | null>(null);
  const tooltipTimerRef = useRef<number | null>(null);
  const providerLabelRef = useRef<HTMLSpanElement | null>(null);
  const modelLabelRef = useRef<HTMLSpanElement | null>(null);
  const modelButtonRef = useRef<HTMLDivElement | null>(null);
  const reasoningButtonRef = useRef<HTMLDivElement | null>(null);
  const balanceRequestRef = useRef(0);
  const balanceDeltaTimeoutRef = useRef<number | null>(null);
  const providerBalanceCacheRef = useRef<Record<string, { label: string; numericValue: number | null }>>({});
  const [open, setOpen] = useState(false);
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const [reasoningMenuOpen, setReasoningMenuOpen] = useState(false);
  const [searchValue, setSearchValue] = useState('');
  const [providerList, setProviderList] = useState<AIProviderLike[]>(sortProviders(providers));
  const [persistedCurrentProviderId, setPersistedCurrentProviderId] = useState(providers[0]?.id || '');
  const [panelBounds, setPanelBounds] = useState<{ top: number; left: number; width: number; height: number } | null>(null);
  const [dropdownMetrics, setDropdownMetrics] = useState<{ width: number; maxHeight: number } | null>(null);
  const [triggerRect, setTriggerRect] = useState<RectLike | null>(null);
  const [modelTriggerRect, setModelTriggerRect] = useState<RectLike | null>(null);
  const [tooltipVisible, setTooltipVisible] = useState(false);
  const [tooltipTriggerRect, setTooltipTriggerRect] = useState<RectLike | null>(null);
  const [providerBalanceLabel, setProviderBalanceLabel] = useState('');
  const [providerBalanceDelta, setProviderBalanceDelta] = useState<number | null>(null);
  const [providerBalanceDeltaTick, setProviderBalanceDeltaTick] = useState(0);
  const [quickModelOptions, setQuickModelOptions] = useState<string[]>([]);
  const [quickModelLoading, setQuickModelLoading] = useState(false);
  const [quickModelError, setQuickModelError] = useState('');
  const [quickModelResolved, setQuickModelResolved] = useState(false);
  const [editingState, setEditingState] = useState<{ open: boolean; mode: 'create' | 'edit'; provider: AIProviderLike | null }>({ open: false, mode: 'edit', provider: null });
  const [providerLabelFontSize, setProviderLabelFontSize] = useState(12);
  const [modelLabelFontSize, setModelLabelFontSize] = useState(12);
  const [providerTriggerWidth, setProviderTriggerWidth] = useState(0);
  const [modelTriggerWidth, setModelTriggerWidth] = useState(0);
  const [minSelectorWidth, setMinSelectorWidth] = useState(0);
  const isControlled = typeof currentProviderId === 'string';
  const effectiveSelectedId = isControlled ? currentProviderId : persistedCurrentProviderId;

  const closeTooltip = useCallback(() => {
    if (tooltipTimerRef.current) {
      window.clearTimeout(tooltipTimerRef.current);
      tooltipTimerRef.current = null;
    }
    setTooltipVisible(false);
  }, []);

  const selectedProvider = useMemo(
    () => providerList.find((item) => item.id === effectiveSelectedId) || null,
    [providerList, effectiveSelectedId],
  );

  const quickModelConfig = useMemo(() => {
    if (!selectedProvider) {
      return { visible: false, options: [] as string[], currentValue: '', currentLabel: '' };
    }
    const fallbackOptions = buildProviderModelOptions(selectedProvider);
    const options = quickModelResolved
      ? (quickModelOptions.length > 0 ? quickModelOptions : fallbackOptions)
      : fallbackOptions;
    const selectedModel = typeof selectedProvider.model === 'string' ? selectedProvider.model.trim() : '';
    const currentValue = selectedModel || (options[0] || '');
    return {
      visible: Boolean(currentValue || options.length > 0),
      options,
      currentValue,
      currentLabel: currentValue || t('模型'),
    };
  }, [quickModelOptions, quickModelResolved, selectedProvider, t, lang]);

  const quickReasoningConfig = useMemo(() => {
    if (!selectedProvider) {
      return { visible: false, options: [] as string[], currentValue: 'disable', currentLabel: '' };
    }
    const selectedModel = typeof selectedProvider.model === 'string' ? selectedProvider.model.trim() : '';
    if (!selectedModel) {
      return { visible: false, options: [] as string[], currentValue: 'disable', currentLabel: '' };
    }
    const providerValue = typeof selectedProvider.provider === 'string' && selectedProvider.provider.trim() ? selectedProvider.provider.trim() : 'Compatible';
    const providerDefinition = getAIProviderDefinition(providerValue);
    const capability = buildDisplayModelCapability(providerDefinition.value, providerDefinition.getModelCapability(selectedModel));
    let options = buildReasoningOptions(capability);
    const storedValue = typeof selectedProvider.reasoningEffort === 'string' ? selectedProvider.reasoningEffort.trim().toLowerCase() : '';
    const hasStoredEffort = storedValue !== '' && storedValue !== 'disable';
    const defaultValue = typeof capability?.reasoningEffort === 'string' ? capability.reasoningEffort.trim().toLowerCase() : '';
    if (storedValue && storedValue !== 'disable' && !options.includes(storedValue)) {
      options = [...options, storedValue];
    }
    if (defaultValue && defaultValue !== 'disable' && !options.includes(defaultValue)) {
      options = [...options, defaultValue];
    }
    // 能力表未收录该模型时，若供应商已保存过思考档位，仍展示档位按钮（补一个"关闭"选项供切换），
    // 否则用户已配置的思考强度会连同入口一起消失。
    if (capability?.reasoningMode !== 'effort' && !hasStoredEffort) {
      return { visible: false, options: [] as string[], currentValue: 'disable', currentLabel: '' };
    }
    if (options.length <= 1) {
      if (!hasStoredEffort) {
        return { visible: false, options: [] as string[], currentValue: 'disable', currentLabel: '' };
      }
      options = options.includes('disable') ? options : ['disable', ...options];
    }
    let currentValue = storedValue && options.includes(storedValue) ? storedValue : '';
    if (!currentValue) {
      currentValue = capability?.requiredReasoningEffort
        ? (defaultValue || options[0] || 'disable')
        : (selectedProvider.enableReasoningEffort === true
          ? (defaultValue || options.find((value) => value !== 'disable') || options[0] || 'disable')
          : (options.includes('disable') ? 'disable' : (defaultValue || options[0] || 'disable')));
    }
    if (selectedProvider.enableReasoningEffort !== true && options.includes('disable') && !capability?.requiredReasoningEffort) {
      currentValue = 'disable';
    }
    return {
      visible: true,
      options,
      currentValue,
      currentLabel: getReasoningEffortLabel(t, currentValue) || t('无'),
    };
  }, [selectedProvider, t, lang]);

  const providerBalanceLabelEnabled = isAIProviderBalanceLabelEnabled(selectedProvider);
  const providerTriggerText = providerBalanceLabelEnabled ? (providerBalanceLabel || '¥ --') : (selectedProvider?.name || t('选择供应商'));
  const providerBalanceDeltaLabel = providerBalanceDelta === null ? '' : formatAIProviderBalanceDeltaLabel(providerBalanceDelta);
  const providerBalanceDeltaPositive = Number(providerBalanceDelta) > 0;

  const providerSystemPromptAppendEnabled = typeof selectedProvider?.systemPromptAppend === 'string'
    && selectedProvider.systemPromptAppend.trim() !== '';

  const providerSummaryRows = [
    { label: t('供应商'), value: selectedProvider?.name || t('选择供应商') },
    { label: t('模型'), value: getProviderModelSummary(t, selectedProvider) },
    { label: t('API兼容方式'), value: selectedProvider?.provider || 'Compatible' },
    { label: t('缓存策略'), value: getCacheStrategyLabel(t, selectedProvider?.cacheStrategy) },
    { label: t('追加系统提示词'), value: providerSystemPromptAppendEnabled ? t('是') : t('否') },
    { label: 'Key', value: getApiKeyPreview(selectedProvider?.apiKey) || '-' },
  ];

  const updateAdaptiveLabelFontSizes = useCallback(() => {
    const providerText = providerTriggerText;
    const modelText = quickModelConfig.visible ? quickModelConfig.currentLabel : '';
    const providerFontFamily = providerLabelRef.current
      ? (window.getComputedStyle(providerLabelRef.current).fontFamily || 'sans-serif')
      : 'sans-serif';
    const modelFontFamily = modelLabelRef.current
      ? (window.getComputedStyle(modelLabelRef.current).fontFamily || 'sans-serif')
      : providerFontFamily;
    const reasoningWidth = quickReasoningConfig.visible && reasoningButtonRef.current
      ? Math.ceil(reasoningButtonRef.current.getBoundingClientRect().width)
      : 0;
    const overlapWidth = (quickModelConfig.visible ? 1 : 0) + (quickReasoningConfig.visible ? 1 : 0);
    // 容器自身可被 flex 压缩（flex-basis 0），必须保住内部按钮最小宽度之和，
    // 否则按钮会溢出 overflow-visible 的容器、盖住底栏相邻按钮。
    setMinSelectorWidth(Math.max(
      ADAPTIVE_PROVIDER_MIN_WIDTH,
      ADAPTIVE_PROVIDER_MIN_WIDTH
        + (quickModelConfig.visible ? ADAPTIVE_MODEL_MIN_WIDTH : 0)
        + (quickReasoningConfig.visible && reasoningWidth > 0 ? reasoningWidth : 0)
        - overlapWidth,
    ));
    const layout = resolveAdaptiveLabelLayout({
      providerText,
      modelText,
      availableWidth: resolveAdaptiveSelectorAvailableWidth(containerRef.current),
      providerFontFamily,
      modelFontFamily,
      fixedWidth: reasoningWidth - overlapWidth,
      minFontSize: 11,
    });
    setProviderLabelFontSize(layout.providerFontSize);
    setModelLabelFontSize(layout.modelFontSize);
    setProviderTriggerWidth(layout.providerWidth);
    setModelTriggerWidth(layout.modelWidth);
  }, [providerTriggerText, quickModelConfig.currentLabel, quickModelConfig.visible, quickReasoningConfig.currentLabel, quickReasoningConfig.visible]);

  const filteredProviders = useMemo(() => {
    const keyword = searchValue.trim().toLowerCase();
    const sortedProviders = sortProviders(providerList);
    if (!keyword) {
      return sortedProviders;
    }
    return sortedProviders.filter((item) => {
      const haystack = `${item.name || ''} ${item.model || ''} ${item.provider || ''}`.toLowerCase();
      return haystack.includes(keyword);
    });
  }, [providerList, searchValue]);

  const pinnedProviders = useMemo(() => filteredProviders.filter((item) => item.pinned), [filteredProviders]);
  const normalProviders = useMemo(() => filteredProviders.filter((item) => !item.pinned), [filteredProviders]);

  const persistRegistryState = useCallback(async (nextProviders: AIProviderLike[], nextPersistedId: string) => {
    const savedState = await saveAIProviderState({
      currentProviderId: nextPersistedId,
      providers: nextProviders,
    });
    const sortedProviders = sortProviders(savedState.providers);
    const sortedCurrentProviderId = savedState.currentProviderId || nextPersistedId || sortedProviders[0]?.id || '';
    setProviderList(sortedProviders);
    setPersistedCurrentProviderId(sortedCurrentProviderId);
    return {
      providers: sortedProviders,
      currentProviderId: sortedCurrentProviderId,
    };
  }, []);

  const getPersistedSelectionId = useCallback((nextProviders: AIProviderLike[], preferredId: string) => {
    if (persistSelectedProviderId || !isControlled) {
      return preferredId;
    }
    if (persistedCurrentProviderId && nextProviders.some((item) => item.id === persistedCurrentProviderId)) {
      return persistedCurrentProviderId;
    }
    return nextProviders[0]?.id || '';
  }, [isControlled, persistSelectedProviderId, persistedCurrentProviderId]);

  const resolveProviderRegistryState = useCallback(async () => {
    const state = await getAIProviderState();
    const currentPropsProviders = providersRef.current;
    const hasPersistedProviders = Array.isArray(state.providers) && state.providers.length > 0;
    const nextState = hasPersistedProviders
      ? state
      : normalizeAIProviderState({ currentProviderId: currentPropsProviders[0]?.id || '', providers: currentPropsProviders });
    const nextProviders = sortProviders(nextState.providers);
    const nextSelectedId = nextState.currentProviderId || nextProviders[0]?.id || '';
    return {
      hasPersistedProviders,
      nextProviders,
      nextSelectedId,
    };
  }, []);

  const notifySelectionChange = useCallback(async (providerId: string) => {
    if (typeof onCurrentProviderChange === 'function') {
      await onCurrentProviderChange(providerId);
    }
  }, [onCurrentProviderChange]);

  const handleOpenEditor = (mode: 'create' | 'edit', provider: AIProviderLike | null) => {
    setOpen(false);
    setModelMenuOpen(false);
    setReasoningMenuOpen(false);
    setEditingState({ open: true, mode, provider });
  };

  const handleCopyProvider = (provider: AIProviderLike) => {
    if (!provider) {
      return;
    }
    handleOpenEditor('create', {
      ...provider,
      id: '',
      name: buildProviderCopyName(t, provider),
    });
  };

  const handleSelectProvider = async (providerId: string) => {
    setOpen(false);
    setModelMenuOpen(false);
    setReasoningMenuOpen(false);
    if (!isControlled || persistSelectedProviderId) {
      await persistRegistryState(providerList, providerId);
    } else {
      setPersistedCurrentProviderId(providerId);
    }
    await notifySelectionChange(providerId);
  };

  const handleQuickModelSelect = useCallback(async (nextValue: string) => {
    if (!selectedProvider) {
      return;
    }
    const normalizedValue = typeof nextValue === 'string' ? nextValue.trim() : '';
    if (!normalizedValue) {
      return;
    }
    const providerValue = typeof selectedProvider.provider === 'string' && selectedProvider.provider.trim() ? selectedProvider.provider.trim() : 'Compatible';
    const providerDefinition = getAIProviderDefinition(providerValue);
    const capability = buildDisplayModelCapability(providerDefinition.value, providerDefinition.getModelCapability(normalizedValue));
    const nextProviders = providerList.map((item) => {
      if (item.id !== selectedProvider.id) {
        return item;
      }
      const nextReasoningOptions = buildReasoningOptions(capability);
      let reasoningEffort = typeof item.reasoningEffort === 'string' ? item.reasoningEffort.trim().toLowerCase() : 'disable';
      let enableReasoningEffort = item.enableReasoningEffort === true;
      if (capability?.reasoningMode === 'effort') {
        if (!nextReasoningOptions.includes(reasoningEffort)) {
          reasoningEffort = capability.requiredReasoningEffort
            ? (typeof capability.reasoningEffort === 'string' ? capability.reasoningEffort : nextReasoningOptions[0] || 'disable')
            : (enableReasoningEffort
              ? (typeof capability.reasoningEffort === 'string' ? capability.reasoningEffort : nextReasoningOptions.find((value) => value !== 'disable') || 'disable')
              : (nextReasoningOptions.includes('disable') ? 'disable' : (typeof capability.reasoningEffort === 'string' ? capability.reasoningEffort : nextReasoningOptions[0] || 'disable')));
        }
        enableReasoningEffort = capability.requiredReasoningEffort ? true : reasoningEffort !== 'disable';
      } else {
        reasoningEffort = 'disable';
        enableReasoningEffort = false;
      }
      return {
        ...item,
        model: normalizedValue,
        reasoningEffort,
        enableReasoningEffort,
        modelMaxTokens: 0,
        modelMaxThinkingTokens: 0,
        updatedAt: Date.now(),
      };
    });
    const normalizedState = normalizeAIProviderState({
      currentProviderId: getPersistedSelectionId(nextProviders, persistedCurrentProviderId || effectiveSelectedId || selectedProvider.id || ''),
      providers: nextProviders,
    });
    await persistRegistryState(normalizedState.providers, normalizedState.currentProviderId);
    setModelMenuOpen(false);
  }, [effectiveSelectedId, getPersistedSelectionId, persistRegistryState, persistedCurrentProviderId, providerList, selectedProvider]);

  const handleQuickReasoningSelect = useCallback(async (nextValue: string) => {
    if (!selectedProvider) {
      return;
    }
    const normalizedValue = typeof nextValue === 'string' ? nextValue.trim().toLowerCase() : 'disable';
    const nextProviders = providerList.map((item) => (
      item.id === selectedProvider.id
        ? {
            ...item,
            reasoningEffort: normalizedValue || 'disable',
            enableReasoningEffort: normalizedValue !== 'disable',
            modelMaxTokens: 0,
            modelMaxThinkingTokens: 0,
            updatedAt: Date.now(),
          }
        : item
    ));
    const normalizedState = normalizeAIProviderState({
      currentProviderId: getPersistedSelectionId(nextProviders, persistedCurrentProviderId || effectiveSelectedId || selectedProvider.id || ''),
      providers: nextProviders,
    });
    await persistRegistryState(normalizedState.providers, normalizedState.currentProviderId);
    setReasoningMenuOpen(false);
  }, [effectiveSelectedId, getPersistedSelectionId, persistRegistryState, persistedCurrentProviderId, providerList, selectedProvider]);

  const handleSaveProvider = async (draft: Record<string, unknown>) => {
    const savedProvider: AIProviderLike = {
      id: draft.id ? String(draft.id) : `ai-provider-${Date.now()}`,
      name: (typeof draft.name === 'string' ? draft.name.trim() : '') || t('未命名供应商'),
      provider: (typeof draft.provider === 'string' ? draft.provider.trim() : '') || 'Compatible',
      model: typeof draft.model === 'string' ? draft.model.trim() : '',
      baseUrl: typeof draft.baseUrl === 'string' ? draft.baseUrl.trim() : '',
      apiKey: typeof draft.apiKey === 'string' ? draft.apiKey.trim() : '',
      cacheStrategy: typeof draft.cacheStrategy === 'string' ? draft.cacheStrategy : 'model',
      customHeaders: normalizeAIProviderCustomHeaders(draft.customHeaders),
      openAiResponsesUsePromptCacheRetention: draft.openAiResponsesUsePromptCacheRetention === true,
      openAiResponsesFinishOnCompletedEvent: draft.openAiResponsesFinishOnCompletedEvent === true,
      modelTemperature: normalizeOptionalNumber(draft.modelTemperature),
      modelTopP: normalizeOptionalNumber(draft.modelTopP),
      systemPromptAppend: typeof draft.systemPromptAppend === 'string' ? draft.systemPromptAppend.replace(/\r\n/g, '\n').trim() : '',
      systemPromptPresetId: typeof draft.systemPromptPresetId === 'string' ? draft.systemPromptPresetId.trim() : '',
      webSearchEnabled: Boolean(draft.webSearchEnabled),
      dedicatedWebSearchEnabled: Boolean(draft.dedicatedWebSearchEnabled),
      dedicatedWebSearchProviderId: typeof draft.dedicatedWebSearchProviderId === 'string' ? draft.dedicatedWebSearchProviderId : '',
      dedicatedProxyEnabled: Boolean(draft.dedicatedProxyEnabled),
      dedicatedProxyId: typeof draft.dedicatedProxyId === 'string' ? draft.dedicatedProxyId : '',
      reasoningEffort: typeof draft.reasoningEffort === 'string' ? draft.reasoningEffort : 'disable',
      enableReasoningEffort: Boolean(draft.enableReasoningEffort),
      modelMaxTokens: Number.isFinite(Number(draft.modelMaxTokens)) && Number(draft.modelMaxTokens) > 0
        ? Math.floor(Number(draft.modelMaxTokens))
        : 0,
      modelMaxThinkingTokens: Number.isFinite(Number(draft.modelMaxThinkingTokens)) && Number(draft.modelMaxThinkingTokens) > 0
        ? Math.floor(Number(draft.modelMaxThinkingTokens))
        : 0,
      pinned: Boolean(draft.pinned),
      updatedAt: Date.now(),
    };

    const nextBaseProviders = providerList.some((item) => item.id === savedProvider.id)
      ? providerList.map((item) => (item.id === savedProvider.id ? { ...item, ...savedProvider } : item))
      : [savedProvider, ...providerList];

    const normalizedState = normalizeAIProviderState({
      currentProviderId: getPersistedSelectionId(nextBaseProviders, savedProvider.id || ''),
      providers: nextBaseProviders,
    });
    const nextProviders = sortProviders(normalizedState.providers);

    await persistRegistryState(nextProviders, normalizedState.currentProviderId);
    setOpen(false);
    setEditingState({ open: false, mode: 'edit', provider: null });
    await notifySelectionChange(savedProvider.id || '');
  };

  const handleDeleteProvider = async (provider: AIProviderLike) => {
    if (!provider) {
      return;
    }
    const confirmed = await window.lumeDialog?.confirm(`${t('确定删除供应商')}「${provider.name || provider.provider || provider.id}」？${t('此操作不可撤销')}`);
    if (!confirmed) {
      return;
    }

    const nextBaseProviders = providerList.filter((item) => item.id !== provider.id);
    const fallbackSelectedId = nextBaseProviders[0]?.id || '';
    const normalizedState = normalizeAIProviderState({
      currentProviderId: getPersistedSelectionId(
        nextBaseProviders,
        persistedCurrentProviderId === provider.id ? fallbackSelectedId : persistedCurrentProviderId,
      ),
      providers: nextBaseProviders,
    });
    const nextProviders = sortProviders(normalizedState.providers);

    await persistRegistryState(nextProviders, normalizedState.currentProviderId);
    setOpen(false);
    setEditingState({ open: false, mode: 'edit', provider: null });

    if (effectiveSelectedId === provider.id) {
      await notifySelectionChange(fallbackSelectedId);
    }
  };

  const handleTogglePin = async (item: AIProviderLike) => {
    const nextBaseProviders = providerList.map((entry) => (
      entry.id === item.id ? { ...entry, pinned: !entry.pinned, updatedAt: Date.now() } : entry
    ));
    const normalizedState = normalizeAIProviderState({
      currentProviderId: getPersistedSelectionId(nextBaseProviders, persistedCurrentProviderId || nextBaseProviders[0]?.id || ''),
      providers: nextBaseProviders,
    });
    await persistRegistryState(sortProviders(normalizedState.providers), normalizedState.currentProviderId);
  };

  useEffect(() => {
    let cancelled = false;

    resolveProviderRegistryState()
      .then(async ({ hasPersistedProviders, nextProviders, nextSelectedId }) => {
        if (cancelled) {
          return;
        }
        setProviderList(nextProviders);
        setPersistedCurrentProviderId(nextSelectedId);
        if (!hasPersistedProviders) {
          await persistRegistryState(nextProviders, nextSelectedId);
        }
      })
      .catch(async () => {
        if (cancelled) {
          return;
        }
        const currentPropsProviders = providersRef.current;
        const nextState = normalizeAIProviderState({ currentProviderId: currentPropsProviders[0]?.id || '', providers: currentPropsProviders });
        const nextProviders = sortProviders(nextState.providers);
        const nextSelectedId = nextState.currentProviderId || nextProviders[0]?.id || '';
        setProviderList(nextProviders);
        setPersistedCurrentProviderId(nextSelectedId);
        await persistRegistryState(nextProviders, nextSelectedId);
      });

    return () => {
      cancelled = true;
    };
  }, [persistRegistryState, resolveProviderRegistryState]);

  useLayoutEffect(() => {
    updateAdaptiveLabelFontSizes();
    const observedElements = [containerRef.current, containerRef.current?.parentElement, reasoningButtonRef.current].filter(Boolean) as HTMLElement[];
    if (observedElements.length === 0) {
      return undefined;
    }
    if (typeof window.ResizeObserver === 'function') {
      const observer = new window.ResizeObserver(() => {
        updateAdaptiveLabelFontSizes();
      });
      observedElements.forEach((element) => observer.observe(element));
      window.addEventListener('resize', updateAdaptiveLabelFontSizes);
      return () => {
        observer.disconnect();
        window.removeEventListener('resize', updateAdaptiveLabelFontSizes);
      };
    }
    window.addEventListener('resize', updateAdaptiveLabelFontSizes);
    return () => window.removeEventListener('resize', updateAdaptiveLabelFontSizes);
  }, [quickModelConfig.visible, quickReasoningConfig.visible, updateAdaptiveLabelFontSizes]);

  useLayoutEffect(() => {
    const frameId = window.requestAnimationFrame(() => {
      updateAdaptiveLabelFontSizes();
    });
    return () => window.cancelAnimationFrame(frameId);
  }, [providerTriggerText, quickModelConfig.currentLabel, quickReasoningConfig.currentLabel, updateAdaptiveLabelFontSizes]);

  useLayoutEffect(() => {
    const availableWidth = resolveAdaptiveSelectorAvailableWidth(containerRef.current);
    if (availableWidth <= 0) {
      return;
    }
    const providerLabelElement = providerLabelRef.current;
    const modelLabelElement = modelLabelRef.current;
    const reasoningWidth = quickReasoningConfig.visible && reasoningButtonRef.current
      ? Math.ceil(reasoningButtonRef.current.getBoundingClientRect().width)
      : 0;
    const overlapWidth = (quickModelConfig.visible ? 1 : 0) + (quickReasoningConfig.visible ? 1 : 0);
    const currentProviderWidth = providerTriggerWidth > 0 ? providerTriggerWidth : 0;
    const currentModelWidth = quickModelConfig.visible && modelTriggerWidth > 0 ? modelTriggerWidth : 0;
    const currentTotalWidth = currentProviderWidth + currentModelWidth + reasoningWidth - overlapWidth;
    const spareWidth = Math.max(0, availableWidth - currentTotalWidth);
    const providerOverflow = providerLabelElement
      ? Math.max(0, Math.ceil(providerLabelElement.scrollWidth - providerLabelElement.clientWidth))
      : 0;
    const modelOverflow = quickModelConfig.visible && modelLabelElement
      ? Math.max(0, Math.ceil(modelLabelElement.scrollWidth - modelLabelElement.clientWidth))
      : 0;
    if (spareWidth <= 0 || (providerOverflow <= 0 && modelOverflow <= 0)) {
      return;
    }
    let remainingWidth = spareWidth;
    let nextModelWidth = currentModelWidth;
    let nextProviderWidth = currentProviderWidth;
    if (modelOverflow > 0 && remainingWidth > 0) {
      const extraWidth = Math.min(modelOverflow + 2, remainingWidth);
      nextModelWidth += extraWidth;
      remainingWidth -= extraWidth;
    }
    if (providerOverflow > 0 && remainingWidth > 0) {
      const extraWidth = Math.min(providerOverflow + 2, remainingWidth);
      nextProviderWidth += extraWidth;
    }
    if (nextProviderWidth !== currentProviderWidth) {
      setProviderTriggerWidth(nextProviderWidth);
    }
    if (nextModelWidth !== currentModelWidth) {
      setModelTriggerWidth(nextModelWidth);
    }
  }, [
    modelTriggerWidth,
    providerTriggerWidth,
    quickModelConfig.currentLabel,
    quickModelConfig.visible,
    quickReasoningConfig.visible,
    providerTriggerText,
  ]);

  useEffect(() => {
    if (!selectedProvider) {
      setQuickModelOptions([]);
      setQuickModelLoading(false);
      setQuickModelError('');
      setQuickModelResolved(false);
      return undefined;
    }

    const providerValue = typeof selectedProvider.provider === 'string' ? selectedProvider.provider.trim() : '';
    const baseUrl = typeof selectedProvider.baseUrl === 'string' ? selectedProvider.baseUrl.trim() : '';
    const apiKey = typeof selectedProvider.apiKey === 'string' ? selectedProvider.apiKey.trim() : '';

    if (!baseUrl || !apiKey) {
      setQuickModelOptions(buildProviderModelOptions(selectedProvider));
      setQuickModelLoading(false);
      setQuickModelError('');
      setQuickModelResolved(false);
      return undefined;
    }

    let cancelled = false;
    setQuickModelLoading(true);
    setQuickModelError('');
    setQuickModelResolved(false);

    const bridge = getAppBridge();
    const requestProfile = {
      ...selectedProvider,
      provider: providerValue || 'Compatible',
      baseUrl,
      apiKey,
      model: typeof selectedProvider.model === 'string' ? selectedProvider.model.trim() : '',
      dedicatedProxyEnabled: Boolean(selectedProvider.dedicatedProxyEnabled),
      dedicatedProxyId: typeof selectedProvider.dedicatedProxyId === 'string' ? selectedProvider.dedicatedProxyId.trim() : '',
    };

    const requestModels = async () => {
      try {
        const models = bridge?.RequestAIProviderModelsWithProfile
          ? await bridge.RequestAIProviderModelsWithProfile(JSON.stringify(requestProfile))
          : (bridge?.RequestAIProviderModels
            ? await bridge.RequestAIProviderModels(baseUrl, apiKey)
            : []);
        if (cancelled) {
          return;
        }
        const normalizedModels = Array.isArray(models)
          ? models.filter((item) => typeof item === 'string' && item.trim()).map((item) => item.trim())
          : [];
        setQuickModelOptions(normalizedModels);
        setQuickModelError('');
        setQuickModelResolved(true);
      } catch (error) {
        if (cancelled) {
          return;
        }
        setQuickModelOptions([]);
        setQuickModelError(error instanceof Error ? error.message : '');
        setQuickModelResolved(true);
      } finally {
        if (!cancelled) {
          setQuickModelLoading(false);
        }
      }
    };

    void requestModels();

    return () => {
      cancelled = true;
    };
  }, [
    selectedProvider?.id,
    selectedProvider?.provider,
    selectedProvider?.baseUrl,
    selectedProvider?.apiKey,
    selectedProvider?.customHeaders,
    selectedProvider?.dedicatedProxyEnabled,
    selectedProvider?.dedicatedProxyId,
  ]);

  useEffect(() => {
    if (!selectedProvider || !providerBalanceLabelEnabled) {
      setProviderBalanceLabel('');
      setProviderBalanceDelta(null);
      return undefined;
    }
    const origin = resolveAIProviderBaseOrigin(selectedProvider.baseUrl);
    const apiKey = typeof selectedProvider.apiKey === 'string' ? selectedProvider.apiKey.trim() : '';
    const providerId = typeof selectedProvider.id === 'string' ? selectedProvider.id.trim() : '';
    const cachedBalanceEntry = providerId ? providerBalanceCacheRef.current[providerId] || null : null;
    if (!origin || !apiKey) {
      setProviderBalanceLabel('¥ --');
      setProviderBalanceDelta(null);
      return undefined;
    }
    const requestId = balanceRequestRef.current + 1;
    balanceRequestRef.current = requestId;
    const controller = new AbortController();
    setProviderBalanceLabel(cachedBalanceEntry?.label || '¥ --');
    void fetch(`${origin}/api/usage/token/user-balance?apikey=${encodeURIComponent(apiKey)}`, {
      method: 'GET',
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        const contentType = typeof response.headers?.get === 'function' ? response.headers.get('content-type') || '' : '';
        if (contentType.toLowerCase().includes('application/json')) {
          return response.json();
        }
        return response.text();
      })
      .then((payload) => {
        if (balanceRequestRef.current !== requestId) {
          return;
        }
        const balanceValue = extractAIProviderBalanceValue(payload);
        const nextNumericValue = parseAIProviderBalanceNumber(balanceValue);
        const previousNumericValue = cachedBalanceEntry?.numericValue;
        const nextBalanceLabel = formatAIProviderBalanceLabel(balanceValue);
        setProviderBalanceLabel(nextBalanceLabel);
        if (providerId && Number.isFinite(previousNumericValue) && Number.isFinite(nextNumericValue)) {
          const deltaValue = (nextNumericValue as number) - (previousNumericValue as number);
          if (deltaValue !== 0) {
            setProviderBalanceDelta(deltaValue);
            setProviderBalanceDeltaTick((current) => current + 1);
          }
        }
        if (providerId) {
          providerBalanceCacheRef.current[providerId] = {
            label: nextBalanceLabel,
            numericValue: nextNumericValue,
          };
        }
      })
      .catch(() => {
        if (balanceRequestRef.current !== requestId) {
          return;
        }
        setProviderBalanceLabel(cachedBalanceEntry?.label || '¥ --');
        setProviderBalanceDelta(null);
      });
    return () => {
      controller.abort();
    };
  }, [balanceRefreshSignal, providerBalanceLabelEnabled, selectedProvider?.apiKey, selectedProvider?.baseUrl, selectedProvider?.id]);

  useEffect(() => {
    if (balanceDeltaTimeoutRef.current) {
      window.clearTimeout(balanceDeltaTimeoutRef.current);
      balanceDeltaTimeoutRef.current = null;
    }
    if (!providerBalanceDeltaLabel) {
      return undefined;
    }
    balanceDeltaTimeoutRef.current = window.setTimeout(() => {
      setProviderBalanceDelta(null);
      balanceDeltaTimeoutRef.current = null;
    }, 2400);
    return () => {
      if (balanceDeltaTimeoutRef.current) {
        window.clearTimeout(balanceDeltaTimeoutRef.current);
        balanceDeltaTimeoutRef.current = null;
      }
    };
  }, [providerBalanceDeltaLabel, providerBalanceDeltaTick]);

  useEffect(() => () => closeTooltip(), [closeTooltip]);

  useEffect(() => {
    if (!tooltipVisible) {
      return undefined;
    }

    const updateTooltipRect = () => {
      const rect = containerRef.current?.getBoundingClientRect();
      if (!rect) {
        setTooltipVisible(false);
        return;
      }
      setTooltipTriggerRect({ top: rect.top, left: rect.left, right: rect.right, bottom: rect.bottom });
    };

    updateTooltipRect();
    window.addEventListener('resize', updateTooltipRect);
    window.addEventListener('scroll', updateTooltipRect, true);

    return () => {
      window.removeEventListener('resize', updateTooltipRect);
      window.removeEventListener('scroll', updateTooltipRect, true);
    };
  }, [tooltipVisible]);

  useEffect(() => {
    if (open || editingState.open || modelMenuOpen || reasoningMenuOpen) {
      closeTooltip();
    }
  }, [closeTooltip, editingState.open, modelMenuOpen, open, reasoningMenuOpen]);

  useEffect(() => {
    if ((!open && !modelMenuOpen && !reasoningMenuOpen) || editingState.open) {
      return undefined;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
        setModelMenuOpen(false);
        setReasoningMenuOpen(false);
      }
    };

    window.addEventListener('pointerdown', handlePointerDown);
    return () => window.removeEventListener('pointerdown', handlePointerDown);
  }, [editingState.open, modelMenuOpen, open, reasoningMenuOpen]);

  useEffect(() => {
    if (!editingState.open && !open && !modelMenuOpen && !reasoningMenuOpen) {
      setTriggerRect(null);
      setModelTriggerRect(null);
      setPanelBounds(null);
      setDropdownMetrics(null);
      return undefined;
    }

    const updatePanelBounds = () => {
      const root = containerRef.current?.closest('[data-ai-panel-root="true"]');
      const chatStage = root?.querySelector('[data-ai-chat-stage="true"]');
      const composerInputZone = root?.querySelector('[data-ai-composer-input-zone="true"]');
      const fallbackPanel = root || chatStage || composerInputZone;

      if (!fallbackPanel) {
        setPanelBounds(null);
        setDropdownMetrics(null);
        return;
      }

      const fallbackRect = fallbackPanel.getBoundingClientRect();
      const chatRect = chatStage?.getBoundingClientRect();
      const composerRect = composerInputZone?.getBoundingClientRect();

      const top = Math.min(chatRect?.top ?? fallbackRect.top, composerRect?.top ?? fallbackRect.top);
      const left = Math.min(chatRect?.left ?? fallbackRect.left, composerRect?.left ?? fallbackRect.left);
      const right = Math.max(chatRect?.right ?? fallbackRect.right, composerRect?.right ?? fallbackRect.right);
      const bottom = Math.max(chatRect?.bottom ?? fallbackRect.bottom, composerRect?.bottom ?? fallbackRect.bottom);

      setPanelBounds({
        top,
        left,
        width: right - left,
        height: bottom - top,
      });

      const triggerRectData = containerRef.current?.getBoundingClientRect();
      if (triggerRectData) {
        const panelWidth = Math.max(triggerRectData.width, Math.min(right - left, window.innerWidth - 32));
        setDropdownMetrics({
          width: panelWidth,
          maxHeight: Math.max(120, triggerRectData.top - top - 8),
        });
        setTriggerRect({ top: triggerRectData.top, left: triggerRectData.left, right: triggerRectData.right, bottom: triggerRectData.bottom });
      }

      const modelRectData = modelButtonRef.current?.getBoundingClientRect();
      if (modelRectData) {
        setModelTriggerRect({ top: modelRectData.top, left: modelRectData.left, right: modelRectData.right, bottom: modelRectData.bottom });
      } else {
        setModelTriggerRect(null);
      }
    };

    updatePanelBounds();
    window.addEventListener('resize', updatePanelBounds);
    window.addEventListener('scroll', updatePanelBounds, true);

    return () => {
      window.removeEventListener('resize', updatePanelBounds);
      window.removeEventListener('scroll', updatePanelBounds, true);
    };
  }, [editingState.open, modelMenuOpen, open, reasoningMenuOpen]);

  useEffect(() => {
    if (dismissSignal <= 0) {
      return undefined;
    }
    let cancelled = false;
    closeTooltip();
    setOpen(false);
    setModelMenuOpen(false);
    setReasoningMenuOpen(false);
    setSearchValue('');
    setQuickModelOptions([]);
    setQuickModelLoading(false);
    setQuickModelError('');
    setQuickModelResolved(false);
    setTriggerRect(null);
    setModelTriggerRect(null);
    setTooltipTriggerRect(null);
    setDropdownMetrics(null);
    setPanelBounds(null);
    setEditingState({ open: false, mode: 'edit', provider: null });

    resolveProviderRegistryState()
      .then(({ nextProviders, nextSelectedId }) => {
        if (cancelled) {
          return;
        }
        setProviderList(nextProviders);
        setPersistedCurrentProviderId(nextSelectedId);
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        const currentPropsProviders = providersRef.current;
        const nextState = normalizeAIProviderState({ currentProviderId: currentPropsProviders[0]?.id || '', providers: currentPropsProviders });
        const nextProviders = sortProviders(nextState.providers);
        const nextSelectedId = nextState.currentProviderId || nextProviders[0]?.id || '';
        setProviderList(nextProviders);
        setPersistedCurrentProviderId(nextSelectedId);
      });

    return () => {
      cancelled = true;
    };
  }, [closeTooltip, dismissSignal, resolveProviderRegistryState]);

  const handleTriggerMouseEnter = useCallback(() => {
    if (open || editingState.open || modelMenuOpen || reasoningMenuOpen) {
      return;
    }
    const rect = containerRef.current?.getBoundingClientRect();
    if (rect) {
      setTooltipTriggerRect({ top: rect.top, left: rect.left, right: rect.right, bottom: rect.bottom });
    }
    if (tooltipTimerRef.current) {
      window.clearTimeout(tooltipTimerRef.current);
    }
    tooltipTimerRef.current = window.setTimeout(() => {
      setTooltipVisible(true);
      tooltipTimerRef.current = null;
    }, summaryTooltipDelay);
  }, [editingState.open, modelMenuOpen, open, reasoningMenuOpen]);

  return {
    containerRef,
    providerLabelRef,
    modelLabelRef,
    modelButtonRef,
    reasoningButtonRef,
    open,
    setOpen,
    modelMenuOpen,
    setModelMenuOpen,
    reasoningMenuOpen,
    setReasoningMenuOpen,
    searchValue,
    setSearchValue,
    providerList,
    panelBounds,
    dropdownMetrics,
    triggerRect,
    modelTriggerRect,
    tooltipVisible,
    tooltipTriggerRect,
    providerBalanceLabelEnabled,
    providerTriggerText,
    providerBalanceDeltaLabel,
    providerBalanceDeltaPositive,
    providerBalanceDeltaTick,
    providerSystemPromptAppendEnabled,
    quickModelOptions,
    quickModelLoading,
    quickModelError,
    quickModelConfig,
    quickReasoningConfig,
    providerSummaryRows,
    editingState,
    setEditingState,
    providerLabelFontSize,
    modelLabelFontSize,
    providerTriggerWidth,
    modelTriggerWidth,
    minSelectorWidth,
    filteredProviders,
    pinnedProviders,
    normalProviders,
    effectiveSelectedId,
    closeTooltip,
    handleOpenEditor,
    handleCopyProvider,
    handleSelectProvider,
    handleQuickModelSelect,
    handleQuickReasoningSelect,
    handleSaveProvider,
    handleDeleteProvider,
    handleTogglePin,
    handleTriggerMouseEnter,
  };
}
