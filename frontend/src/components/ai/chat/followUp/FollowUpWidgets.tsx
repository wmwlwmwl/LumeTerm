import ReactMarkdown, { type Components } from 'react-markdown';
import rehypeSanitize from 'rehype-sanitize';
import remarkGfm from 'remark-gfm';
import { cn } from '../../../../utils/cn.ts';
import { remarkCleanAutolinks } from '../AIChatMarkdown.tsx';

export const suggestionMarkdownComponents: Components = {
  p: ({ children }) => <span>{children}</span>,
  ul: ({ children }) => <span className="grid gap-1 pl-[18px]">{children}</span>,
  ol: ({ children }) => <span className="grid gap-1 pl-[18px]">{children}</span>,
  li: ({ children }) => <span className="leading-[1.6] [display:list-item]">{children}</span>,
  a: ({ children }) => <span className="text-accent underline">{children}</span>,
  code: ({ children }) => (
    <code className="rounded-md bg-[color-mix(in_srgb,var(--accent)_10%,var(--surface-overlay))] px-1.5 py-0.5 font-mono text-sm text-primary">
      {children}
    </code>
  ),
  pre: ({ children }) => (
    <span className="block whitespace-pre-wrap font-mono [word-break:break-word]">
      {children}
    </span>
  ),
  blockquote: ({ children }) => (
    <span className="block border-l-[3px] border-l-[color-mix(in_srgb,var(--accent)_40%,var(--border))] pl-3 text-secondary">
      {children}
    </span>
  ),
  h1: ({ children }) => <span className="block text-[16px] font-bold leading-[1.4]">{children}</span>,
  h2: ({ children }) => <span className="block text-[15px] font-bold leading-[1.45]">{children}</span>,
  h3: ({ children }) => <span className="block text-md font-bold leading-[1.5]">{children}</span>,
};

export interface FollowUpSuggestionMarkdownProps {
  text: string;
  inline?: boolean;
}

export function FollowUpSuggestionMarkdown({ text, inline = false }: FollowUpSuggestionMarkdownProps) {
  return (
    <span className={cn('leading-[1.6] [word-break:break-word]', inline ? 'inline w-auto' : 'block w-full')}>
      <ReactMarkdown remarkPlugins={[remarkGfm, remarkCleanAutolinks]} rehypePlugins={[rehypeSanitize]} components={suggestionMarkdownComponents}>
        {text || ''}
      </ReactMarkdown>
    </span>
  );
}

export interface OptionIndicatorProps {
  type: string;
  checked: boolean;
}

export function OptionIndicator({ type, checked }: OptionIndicatorProps) {
  if (type === 'multiple') {
    return (
      <span
        className={cn(
          'inline-flex h-[18px] w-[18px] box-border items-center justify-center rounded-sm border-[1.5px]',
          checked ? 'border-accent bg-[rgba(var(--accent-rgb),0.18)]' : 'border-tertiary bg-transparent',
        )}
      >
        <span
          style={{ background: checked ? 'var(--accent)' : 'transparent' }}
          className="block h-[9px] w-[9px] rounded-xs"
        />
      </span>
    );
  }

  return (
    <span
      className={cn(
        'inline-flex h-[18px] w-[18px] box-border items-center justify-center rounded-full border-[1.5px]',
        checked ? 'border-accent bg-[rgba(var(--accent-rgb),0.12)]' : 'border-tertiary bg-transparent',
      )}
    >
      <span
        style={{ background: checked ? 'var(--accent)' : 'transparent' }}
        className="block h-2 w-2 rounded-full"
      />
    </span>
  );
}
