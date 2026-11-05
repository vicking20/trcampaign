package blaze

import "fmt"

// ComponentName returns a readable Blaze 3 component name for logs.
func ComponentName(component uint16) string {
	if n, ok := componentNames[component]; ok {
		return n
	}
	return fmt.Sprintf("0x%04X", component)
}

// CommandName returns "Component.command" for logs (RPC requests/responses).
func CommandName(component, command uint16) string {
	if sc := GameSchema(); sc != nil { // The game's own names first
		if r, ok := sc.Commands[key(component, command)]; ok && r.Name != "" {
			return ComponentName(component) + "." + r.Name
		}
	}
	if n, ok := commandNames[uint32(component)<<16|uint32(command)]; ok {
		return ComponentName(component) + "." + n
	}
	return fmt.Sprintf("%s.0x%04X", ComponentName(component), command)
}

// NotificationName returns "Component.Notification" for logs.
func NotificationName(component, command uint16) string {
	if sc := GameSchema(); sc != nil {
		if r, ok := sc.Notifications[key(component, command)]; ok && r.Name != "" {
			return ComponentName(component) + "." + r.Name
		}
	}
	if n, ok := notificationNames[uint32(component)<<16|uint32(command)]; ok {
		return ComponentName(component) + "." + n
	}
	return fmt.Sprintf("%s.notify(0x%04X)", ComponentName(component), command)
}
