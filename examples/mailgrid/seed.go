package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/database"
)

const seededKey = "demo_seeded"

var (
	demoFirst = []string{"Ada", "Bram", "Chen", "Dalia", "Emeka", "Freya", "Gus", "Hana", "Ivo", "Jun",
		"Kofi", "Lena", "Mateo", "Nia", "Omar", "Priya", "Quinn", "Rosa", "Sven", "Tara"}
	demoLast   = []string{"Okafor", "Lindqvist", "Moreau", "Tanaka", "Silva", "Novak"}
	demoOrgs   = []string{"Harbor Books", "Northwind Tea", "Ridge Cycles", "Oak & Iron", "Lumen Labs"}
	demoCities = []string{"Lisbon", "Osaka", "Nairobi", "Leeds", "Austin", "Tallinn", "Recife"}
)

// seedDemo adds one list and one letter the first time a development
// database starts, as real rows. It records that it ran, so deleting the
// demo data does not bring it back.
func seedDemo(db *database.DB) error {
	done, err := getSetting(db, seededKey)
	if err != nil || done != "" {
		return err
	}
	return db.Tx(func(tx *database.Tx) error {
		now := time.Now().UTC()
		listID, err := tx.Create("lists", map[string]any{"name": "Spring customers (demo)", "created_at": now})
		if err != nil {
			return err
		}
		for i := range 60 {
			first, last := demoFirst[i%len(demoFirst)], demoLast[i%len(demoLast)]
			fields := map[string]string{"city": demoCities[i%len(demoCities)]}
			// Every seventh customer has no company, so the letter's
			// {{company}} warning has something to report.
			if i%7 != 3 {
				fields["company"] = demoOrgs[i%len(demoOrgs)]
			}
			encoded, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			domain := []string{"example.com", "example.org", "example.net"}[i%3]
			email := fmt.Sprintf("%s.%s%d@%s", strings.ToLower(first), strings.ToLower(last), i+1, domain)
			if _, err := tx.Create("recipients", map[string]any{
				"list_id": listID, "email": email, "name": first + " " + last, "fields": string(encoded), "created_at": now,
			}); err != nil {
				return err
			}
		}
		if _, err := tx.Create("letters", map[string]any{
			"title":   "Spring opening hours (demo)",
			"subject": "{{name}}, our spring hours for {{city}}",
			"body": "Dear {{name}},\n\nFrom April our {{city}} counter opens at 8:00 on weekdays.\n" +
				"Your account manager at {{company}} has the new pickup times.\n\nThe Mailgrid team",
			"format":     formatText,
			"created_at": now,
			"updated_at": now,
		}); err != nil {
			return err
		}
		return setSetting(tx, seededKey, now.Format(time.RFC3339))
	})
}
