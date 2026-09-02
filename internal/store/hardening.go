package store

import "strings"

// forceAdminOnlyLibraries: optional, purely runtime-configured set of library
// NAMES (case-insensitive) that are always admin-only, no matter what
// user_library_access rows exist for them. Empty by default — a no-op unless
// explicitly configured via SetForceAdminOnlyLibraries (see cmd/goldfish/
// main.go, populated from an environment variable). Deliberately kept out of
// the database and out of any checked-in config: this is a generic hardening
// knob for self-hosters who want a personal/admin-only library, not tied to
// any specific deployment's library names.

// SetForceAdminOnlyLibraries configures library names that are always
// admin-only, regardless of any ACL grants. Case-insensitive, whitespace-
// trimmed. Call once at startup; safe to call with an empty slice (clears it).
func (s *Store) SetForceAdminOnlyLibraries(names []string) {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			m[n] = true
		}
	}
	s.forceAdminOnlyLibraries = m
}

// forceAdminOnlyLibraryIDs resolves the configured names to their current
// library IDs. Re-resolved per call (cheap — tiny table, no index needed)
// so a library rename doesn't require a server restart to take effect.
func (s *Store) forceAdminOnlyLibraryIDs() []int64 {
	if len(s.forceAdminOnlyLibraries) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, name FROM libraries`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		var name string
		if rows.Scan(&id, &name) == nil && s.forceAdminOnlyLibraries[strings.ToLower(name)] {
			ids = append(ids, id)
		}
	}
	return ids
}

// forceAdminOnlyExclusionSQL returns a SQL boolean fragment (plus its args)
// that excludes any forced-admin-only library from `col` (e.g. "i.library_id").
// Callers must gate this on the non-admin case themselves — admins are never
// affected by this mechanism. Empty configuration returns a no-op "1=1".
func (s *Store) forceAdminOnlyExclusionSQL(col string) (string, []any) {
	ids := s.forceAdminOnlyLibraryIDs()
	if len(ids) == 0 {
		return "1=1", nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return col + " NOT IN (" + strings.Join(placeholders, ",") + ")", args
}

// isForceAdminOnlyLibrary: true if this specific library ID is currently in
// the forced-admin-only set.
func (s *Store) isForceAdminOnlyLibrary(libraryID int64) bool {
	for _, id := range s.forceAdminOnlyLibraryIDs() {
		if id == libraryID {
			return true
		}
	}
	return false
}
