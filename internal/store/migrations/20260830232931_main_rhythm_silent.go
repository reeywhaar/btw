package migrations

// Whether a nudge arrives without a sound.
//
// A reminder that has to be noticed is the default, but a phone on a desk beside somebody
// working is a phone whose every chime is an interruption they did not ask for — and the
// thing btw is for is putting something down, not being startled by it.
var mainRhythmSilent = Migration{
	Name: "20260830232931_main_rhythm_silent",
	Up: exec(`
ALTER TABLE rhythm ADD COLUMN silent INTEGER NOT NULL DEFAULT 0;
`),
}
