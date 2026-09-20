const PASSWORD_REASONS = new Set([
  'password_required',
  'password_invalid',
  'password_incorrect',
  'missing_recovery_password',
  'invalid_recovery_password',
  'recovery_password_incorrect',
]);

/** 同步错误对象中与恢复密码相关的字段 */
interface RecoveryPasswordErrorLike {
  category?: string;
  reason?: string;
  message?: string;
}

export function isRecoveryPasswordError(error: unknown): boolean {
  const e = error as RecoveryPasswordErrorLike | null | undefined;
  if (e?.category === 'password' || (e?.reason !== undefined && PASSWORD_REASONS.has(e.reason))) return true;
  const message = String(e?.message ?? error ?? '');
  return /恢复密码|(?:LUMETERM2|LUMIN2).*(?:需要密码|解密失败)|密码(?:错误|不正确)/.test(message);
}

export interface SyncWithRecoveryPasswordOptions<TResult> {
  /** 初始同步（无 initialError 时调用；提供了 initialError 则不需要） */
  sync?: () => Promise<TResult>;
  initialError?: unknown;
  retry: (password: string) => Promise<TResult>;
  /** 弹窗输入密码，取消时返回 null */
  prompt: (
    title: string,
    placeholder: string,
    message: string,
    okLabel?: string,
    options?: Record<string, unknown>,
  ) => Promise<string | null>;
  /** 翻译函数 */
  t: (key: string) => string;
}

export interface SyncWithRecoveryPasswordResult<TResult> {
  result: TResult | null;
  cancelled: boolean;
}

export async function syncWithRecoveryPassword<TResult>({
  sync,
  initialError,
  retry,
  prompt,
  t,
}: SyncWithRecoveryPasswordOptions<TResult>): Promise<SyncWithRecoveryPasswordResult<TResult>> {
  let error: unknown = initialError;
  if (!error) {
    if (!sync) throw new Error('sync is required when initialError is not provided');
    try {
      return { result: await sync(), cancelled: false };
    } catch (caught) {
      error = caught;
    }
  }
  if (!isRecoveryPasswordError(error)) throw error;

  for (let attempt = 0; attempt < 3; attempt += 1) {
    const password = await prompt(
      attempt === 0 ? t('请输入恢复密码以继续同步') : t('恢复密码不正确，请重新输入'),
      '',
      t('同步需要恢复密码'),
      '',
      { inputType: 'password' },
    );
    if (password === null) return { result: null, cancelled: true };
    try {
      return { result: await retry(password), cancelled: false };
    } catch (caught) {
      if (!isRecoveryPasswordError(caught)) throw caught;
      error = caught;
    }
  }
  throw error;
}
