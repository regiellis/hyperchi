// Command gen writes reference/helpers.json from the framework source. Run it
// from the showcase directory with go generate.
package main

import (
	"log"

	"showcase/internal/apiref"
)

func main() {
	if err := apiref.WriteFile("../..", "reference/helpers.json"); err != nil {
		log.Fatal(err)
	}
}
