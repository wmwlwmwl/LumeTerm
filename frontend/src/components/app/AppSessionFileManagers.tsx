import type { config } from '../../../wailsjs/go/models.ts';
import FileManager from '../FileManager.tsx';
import type { SessionLike } from '../../utils/sessionWorkspace.ts';

export interface AppSessionFileManagersProps {
  session: SessionLike;
  servers: config.Connection[];
  activeSessionId: string | null;
  activeTerminalId: string | null;
  addToast: (message: string | Error, type?: string, duration?: number) => number;
  removeToast?: (id: number) => void;
  getEffectiveTerminals: (s: SessionLike) => Array<{ id: string; label?: string }>;
}

export default function AppSessionFileManagers({
  session,
  servers,
  activeSessionId,
  activeTerminalId,
  addToast,
  removeToast,
  getEffectiveTerminals,
}: AppSessionFileManagersProps) {
  return (
    <>
      {getEffectiveTerminals(session).map((term) => {
        const isActive = activeSessionId === session.id && activeTerminalId === term.id;
        const serverConfig = servers.find((server) => server.id === session.serverId);
        return (
          <div key={term.id} style={isActive ? { display: 'contents' } : { display: 'none' }}>
            <FileManager
              sessionId={String(term.id ?? '')}
              sessionGroupId={String(session.id ?? '')}
              addToast={addToast}
              removeToast={removeToast}
              isActive={isActive}
              initialPath={serverConfig?.fileManagerInitPath || ''}
            />
          </div>
        );
      })}
    </>
  );
}
