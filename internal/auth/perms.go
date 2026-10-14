package auth

import "strings"

// HasAction checks if claims allow the given action group.
// Empty AllowedActions = all allowed (backcompat). Match case-insensitive.
func HasAction(c *Claims, action string) bool {
	if c == nil {
		return false
	}
	if c.Role == "superadmin" {
		return true
	}
	if len(c.AllowedActions) == 0 {
		return true
	}
	action = strings.ToLower(action)
	for _, a := range c.AllowedActions {
		if strings.EqualFold(strings.TrimSpace(a), action) {
			return true
		}
	}
	return false
}
