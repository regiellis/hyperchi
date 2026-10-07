package main

import (
	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
)

// auditPage lists who did what, newest first.
func (s *Server) auditPage(c *hyperchi.Context) error {
	total, err := s.db.Count("audit_log", "")
	if err != nil {
		return err
	}
	p := database.NewPaginator(c.QueryInt("page", 1), auditPerPage, total)
	query, args := database.Select("id", "user_email", "action", "detail", "created_at").
		From("audit_log").OrderBy("id", "DESC").Limit(p.PerPage).Offset(p.Offset()).Build()
	entries, err := database.ListStruct[AuditEntry](s.db, query, args...)
	if err != nil {
		return err
	}
	return s.page(c, "Audit log", "audit", auditView(p, entries))
}
