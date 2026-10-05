// Command fakeaf stands in for af.exe in the installer's fixture release.
//
// A real executable rather than any bytes, for two of the tests: one runs the
// installed binary to prove what was placed is what was published, and one
// keeps it RUNNING while the installer replaces it, which is the case Windows
// makes hard and the only way to prove the installer handles it is to hold a
// real image open.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hold" {
		// Held until the test kills it, bounded so a test that forgets cannot
		// leave it running for ever.
		fmt.Println("holding")
		time.Sleep(2 * time.Minute)
		return
	}
	fmt.Println("antifailure 9.9.9 (fixture)")
}
