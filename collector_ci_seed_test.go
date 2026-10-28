package collector

import "fmt"

// CI-SEED round A: vet + unused seeds must fail the lint lane.
func seedVetOnly() {
	fmt.Printf("%d", "not-a-number")
}

const seedUnusedOnly = 1
