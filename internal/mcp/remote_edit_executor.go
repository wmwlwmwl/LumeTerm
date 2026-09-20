package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"lumeterm/internal/mcpserver"
)

const remotePatchPythonScript = `#!/usr/bin/env python3
import json
import os
import shutil
import stat
import sys
import tempfile

def ensure_parent(target_path):
    parent = os.path.dirname(target_path)
    if parent:
        os.makedirs(parent, exist_ok=True)

def acquire_lock(target_path):
    ensure_parent(target_path)
    lock_path = target_path + ".lumeterm.lock"
    os.mkdir(lock_path)
    return lock_path

def release_lock(lock_path):
    if not lock_path:
        return
    try:
        os.rmdir(lock_path)
    except OSError:
        shutil.rmtree(lock_path, ignore_errors=True)

def preserve_metadata(target_path):
    if not os.path.exists(target_path):
        return None
    info = os.stat(target_path)
    return {
        "mode": stat.S_IMODE(info.st_mode),
        "uid": info.st_uid,
        "gid": info.st_gid,
    }

def restore_metadata(target_path, metadata):
    if not metadata:
        return
    try:
        os.chmod(target_path, metadata["mode"])
    except OSError:
        pass
    try:
        os.chown(target_path, metadata["uid"], metadata["gid"])
    except OSError:
        pass

def atomic_write(target_path, content):
    ensure_parent(target_path)
    parent = os.path.dirname(target_path) or "."
    metadata = preserve_metadata(target_path)
    temp_path = ""
    fd, temp_path = tempfile.mkstemp(prefix=".lumeterm_patch_", dir=parent, text=True)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        if metadata is not None:
            try:
                os.chmod(temp_path, metadata["mode"])
            except OSError:
                pass
        os.replace(temp_path, target_path)
        restore_metadata(target_path, metadata)
    finally:
        if temp_path and os.path.exists(temp_path):
            os.remove(temp_path)

def read_text(target_path):
    with open(target_path, "r", encoding="utf-8", newline="") as handle:
        return handle.read()

def apply_update_by_expected_content(target_path, expected_content, next_content):
    current_content = read_text(target_path)
    if current_content != expected_content:
        return None, {"reason": "remote content mismatch before atomic write"}
    atomic_write(target_path, next_content)
    return True, None

def apply_delete_by_expected_content(target_path, expected_content):
    current_content = read_text(target_path)
    if current_content != expected_content:
        return None, {"reason": "remote content mismatch before delete"}
    os.remove(target_path)
    return True, None

def apply_update_by_hunks(target_path, hunks):
    content = read_text(target_path)
    for hunk in hunks:
        search = hunk.get("Search") or hunk.get("search") or ""
        replace = hunk.get("Replace") or hunk.get("replace") or ""
        occurrences = content.count(search) if search else 0
        if occurrences != 1:
            return None, occurrences
        content = content.replace(search, replace, 1)
    atomic_write(target_path, content)
    return True, None

def main():
    payload_path = sys.argv[1]
    with open(payload_path, "r", encoding="utf-8") as handle:
        operations = json.load(handle)
    result = {
        "session_id": "",
        "files_changed": 0,
        "applied": False,
        "changes": []
    }
    for operation in operations:
        action = operation.get("Action") or operation.get("action") or ""
        target_path = operation.get("Path") or operation.get("path") or ""
        content = operation.get("Content") or operation.get("content") or ""
        hunks = operation.get("Hunks") or operation.get("hunks") or []
        change = {
            "action": action,
            "path": target_path,
            "hunks": len(hunks),
            "applied": False
        }
        lock_path = ""
        try:
            lock_path = acquire_lock(target_path)
            if action == "add":
                atomic_write(target_path, content)
                change["applied"] = True
                result["files_changed"] += 1
            elif action == "delete":
                if "ExpectedContent" in operation or "expectedContent" in operation or "expected_content" in operation:
                    expected_content = operation.get("ExpectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expected_content")
                    applied, failure = apply_delete_by_expected_content(target_path, expected_content if expected_content is not None else "")
                    if not applied:
                        change["failure"] = failure
                        result["changes"].append(change)
                        result["failure"] = failure
                        print(json.dumps(result, ensure_ascii=False))
                        return 0
                else:
                    os.remove(target_path)
                change["applied"] = True
                result["files_changed"] += 1
            elif action == "update":
                if "ExpectedContent" in operation or "expectedContent" in operation or "expected_content" in operation:
                    expected_content = operation.get("ExpectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expectedContent")
                    if expected_content is None:
                        expected_content = operation.get("expected_content")
                    next_content = operation.get("Content")
                    if next_content is None:
                        next_content = operation.get("content")
                    applied, failure = apply_update_by_expected_content(
                        target_path,
                        expected_content if expected_content is not None else "",
                        next_content if next_content is not None else "",
                    )
                    if not applied:
                        change["failure"] = failure
                        result["changes"].append(change)
                        result["failure"] = failure
                        print(json.dumps(result, ensure_ascii=False))
                        return 0
                else:
                    applied, occurrences = apply_update_by_hunks(target_path, hunks)
                    if not applied:
                        failure = {
                            "reason": "patch hunk matched zero or multiple locations",
                            "occurrences": occurrences
                        }
                        change["failure"] = failure
                        result["changes"].append(change)
                        result["failure"] = failure
                        print(json.dumps(result, ensure_ascii=False))
                        return 0
                change["applied"] = True
                result["files_changed"] += 1
            else:
                failure = {"reason": "unsupported patch action: " + action}
                change["failure"] = failure
                result["changes"].append(change)
                result["failure"] = failure
                print(json.dumps(result, ensure_ascii=False))
                return 0
        except Exception as exc:
            failure = {"reason": str(exc)}
            change["failure"] = failure
            result["changes"].append(change)
            result["failure"] = failure
            print(json.dumps(result, ensure_ascii=False))
            return 0
        finally:
            release_lock(lock_path)
        result["changes"].append(change)
    result["applied"] = True
    print(json.dumps(result, ensure_ascii=False))
    return 0

if __name__ == "__main__":
    sys.exit(main())
`

type RemoteEditExecutor struct {
	host Host
}

func NewRemoteEditExecutor(host Host) RemoteEditExecutor {
	return RemoteEditExecutor{host: host}
}

func (e RemoteEditExecutor) GetCapabilities(sessionID string) (mcpserver.RemoteEditCapabilities, error) {
	return e.GetCapabilitiesContext(context.Background(), sessionID)
}

func (e RemoteEditExecutor) GetCapabilitiesContext(ctx context.Context, sessionID string) (mcpserver.RemoteEditCapabilities, error) {
	capabilities := mcpserver.RemoteEditCapabilities{}
	output, err := e.runCommandContext(ctx, sessionID, "sh -lc 'command -v python3 >/dev/null 2>&1 && echo python3=1 || echo python3=0; command -v perl >/dev/null 2>&1 && echo perl=1 || echo perl=0; command -v patch >/dev/null 2>&1 && echo patch=1 || echo patch=0; command -v flock >/dev/null 2>&1 && echo flock=1 || echo flock=0'")
	if err != nil {
		return capabilities, err
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case "python3=1":
			capabilities.Python3 = true
		case "perl=1":
			capabilities.Perl = true
		case "patch=1":
			capabilities.Patch = true
		case "flock=1":
			capabilities.Flock = true
		}
	}
	return capabilities, nil
}

func (e RemoteEditExecutor) ApplyPatchAtomic(sessionID string, operations []mcpserver.ApplyPatchFileOperation) (mcpserver.ApplyPatchResult, error) {
	return e.ApplyPatchAtomicContext(context.Background(), sessionID, operations)
}

func (e RemoteEditExecutor) ApplyPatchAtomicContext(ctx context.Context, sessionID string, operations []mcpserver.ApplyPatchFileOperation) (mcpserver.ApplyPatchResult, error) {
	result := mcpserver.ApplyPatchResult{SessionID: sessionID}
	capabilities, err := e.GetCapabilitiesContext(ctx, sessionID)
	if err != nil {
		return result, err
	}
	result.Capabilities = capabilities
	if !capabilities.Python3 {
		return result, mcpserver.ErrRemoteEditUnsupported
	}
	result.Handler = mcpserver.EditHandlerPython3AtomicPatch
	payload, err := json.Marshal(operations)
	if err != nil {
		return result, err
	}
	scriptPath, err := e.uploadTempTextContext(ctx, sessionID, ".py", remotePatchPythonScript, 0700)
	if err != nil {
		return result, err
	}
	defer e.removeTempFile(sessionID, scriptPath)
	payloadPath, err := e.uploadTempTextContext(ctx, sessionID, ".json", string(payload), 0600)
	if err != nil {
		return result, err
	}
	defer e.removeTempFile(sessionID, payloadPath)
	output, err := e.runCommandContext(ctx, sessionID, "python3 "+quotePOSIX(scriptPath)+" "+quotePOSIX(payloadPath)+" 2>&1")
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return result, fmt.Errorf("invalid remote patch result: %w", err)
	}
	if result.SessionID == "" {
		result.SessionID = sessionID
	}
	result.Handler = mcpserver.EditHandlerPython3AtomicPatch
	result.Capabilities = capabilities
	return result, nil
}

func (e RemoteEditExecutor) runCommandContext(ctx context.Context, sessionID string, command string) (string, error) {
	if e.host == nil {
		return "", fmt.Errorf("ssh manager unavailable")
	}
	return e.host.RunCommandContext(ctx, sessionID, command)
}

func (e RemoteEditExecutor) uploadTempTextContext(ctx context.Context, sessionID string, suffix string, content string, mode os.FileMode) (string, error) {
	if e.host == nil {
		return "", fmt.Errorf("ssh manager unavailable")
	}
	return e.host.UploadTempTextContext(ctx, sessionID, suffix, content, mode)
}

func (e RemoteEditExecutor) removeTempFile(sessionID string, path string) {
	if e.host == nil || strings.TrimSpace(path) == "" {
		return
	}
	e.host.RemoveFile(sessionID, path)
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}