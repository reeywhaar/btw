// Package app holds what this program is, as distinct from what it was told.
//
// The line between this package and internal/config is whether an operator can change it
// without rebuilding. They can move the port with -p; they cannot make btw listen on
// anything but :80 inside the container, any more than they can make it a different
// project.
//
// It deliberately does not become a home for every constant in the program. The default
// rhythm, the session cookie's name and the staleness cap are domain rules and belong
// beside the code that enforces them — a `constants` package is a package with no subject,
// and the first thing anybody does with one is stop reading it.
package app

// Name is what this program is called, in a log line and in a notification's title.
const Name = "btw"

// ProjectURL is where somebody wondering what btw is can go and read it.
const ProjectURL = "https://github.com/reeywhaar/btw"

// ListenAddr is where serve listens, inside the container. Not configurable — remap it
// with `docker run -p 8080:80`. A port number inside a container is not a thing an
// operator should have to think about twice.
const ListenAddr = ":80"

// Version is the build this is, stamped at link time:
//
//	-ldflags "-X btw/internal/app.Version=$(git rev-parse --short HEAD)"
//
// "dev" is what a local build says, and it is true.
var Version = "dev"
