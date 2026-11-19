//go:build linux

package metrics

import "testing"

// Lecture réelle de /proc : le processus de test lui-même est en cours d'exécution.
func TestProcStates(t *testing.T) {
	total, running, _, err := processStates()
	if err != nil || total < 1 || running < 1 {
		t.Fatalf("processus : total %d, en exécution %d, err %v", total, running, err)
	}
	states, err := readTCPStates()
	if err != nil {
		t.Fatal(err)
	}
	for st := range states {
		found := false
		for _, known := range tcpStates {
			found = found || st == known
		}
		if !found {
			t.Fatalf("état TCP inattendu : %q", st)
		}
	}
	if _, err := readDiskIO(); err != nil {
		t.Fatal(err)
	}
}
