// Command btw serves a place to put a thought down and stop carrying it.
package main

import (
	"os"
	"time"

	"btw/internal/cli"
)

func main() {
	// Every time this stores or prints is UTC, whatever TZ says.
	//
	// btw does care what time it is where a person is — quiet hours are meaningless
	// otherwise — but that conversion happens at one boundary, in internal/rhythm, against
	// an IANA name stored per account and a zone database imported into the binary. Storage
	// and logs stay UTC, so there is exactly one place where local time exists.
	time.Local = time.UTC
	os.Exit(cli.Execute())
}
