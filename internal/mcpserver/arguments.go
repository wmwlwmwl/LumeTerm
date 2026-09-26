package mcpserver

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func validateAllowedArguments(arguments map[string]any, allowedKeys ...string) error {
	if len(arguments) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	var unknownKeys []string
	for key := range arguments {
		if _, ok := allowed[key]; ok {
			continue
		}
		unknownKeys = append(unknownKeys, key)
	}
	if len(unknownKeys) == 0 {
		return nil
	}
	sort.Strings(unknownKeys)
	return fmt.Errorf("unknown arguments: %s", strings.Join(unknownKeys, ", "))
}

func requireStringArgument(arguments map[string]any, name string) (string, error) {
	rawValue, ok := arguments[name]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", name)
	}
	value, ok := rawValue.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", name)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("argument %s must not be empty", name)
	}
	return value, nil
}

func requireStringArgumentAllowEmpty(arguments map[string]any, name string) (string, error) {
	rawValue, ok := arguments[name]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", name)
	}
	value, ok := rawValue.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", name)
	}
	return value, nil
}

func optionalStringArgument(arguments map[string]any, name string) (string, bool, error) {
	rawValue, ok := arguments[name]
	if !ok {
		return "", false, nil
	}
	value, ok := rawValue.(string)
	if !ok {
		return "", false, fmt.Errorf("argument %s must be a string", name)
	}
	return strings.TrimSpace(value), true, nil
}

func optionalBoolArgument(arguments map[string]any, name string) (bool, error) {
	rawValue, ok := arguments[name]
	if !ok {
		return false, nil
	}
	value, ok := rawValue.(bool)
	if !ok {
		return false, fmt.Errorf("argument %s must be a boolean", name)
	}
	return value, nil
}

func optionalIntArgument(arguments map[string]any, name string) (int, bool, error) {
	rawValue, ok := arguments[name]
	if !ok {
		return 0, false, nil
	}
	switch value := rawValue.(type) {
	case int:
		return value, true, nil
	case int32:
		return int(value), true, nil
	case int64:
		return int(value), true, nil
	case float64:
		if value != float64(int(value)) {
			return 0, false, fmt.Errorf("argument %s must be an integer", name)
		}
		return int(value), true, nil
	default:
		return 0, false, fmt.Errorf("argument %s must be an integer", name)
	}
}

func requireSessionArgument(service *Service, arguments map[string]any) (ConnectedSession, error) {
	sessionID, err := requireStringArgument(arguments, "session_id")
	if err != nil {
		return ConnectedSession{}, err
	}
	session, err := service.GetConnectedSession(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ConnectedSession{}, fmt.Errorf("session %q not found among connected sessions; call list_connected_sessions to get valid session ids (prefer the entry marked is_latest_terminal), or reconnect_server if the server was disconnected", sessionID)
		}
		return ConnectedSession{}, err
	}
	return session, nil
}