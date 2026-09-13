package advise

// Category is one of the labels the companion may put on a reminder.
type Category struct{ Name, Gloss string }

// Categories are the labels, each with the gloss it is given in the question.
//
// Glossed because a bare word loses the distinction that matters: "errands" is bound by opening
// hours and "chores" by nothing but being awake, which is the scheduling fact that makes them
// two words rather than one.
//
// **That is also the bar for adding one.** A category earns its place by implying *when* —
// medication is bound to the clock where a hospital appointment is bound to opening hours, and
// that is why they are not both just "health". A word that says only what a reminder is about
// buys nothing here and costs a line of every request.
//
// Ordered by kin rather than alphabetically, because the list is read as prose by the model and
// neighbours that belong together make the distinctions easier to see.
//
// A file of their own: this is the taxonomy, not part of the prompt's wording, and it is the
// list somebody comes looking for.
var Categories = []Category{
	{"work", "the job and whatever it demands"},
	{"personal", "the person's own business, where nothing more specific fits"},
	{"admin", "paperwork, renewals, bureaucracy, accounts"},
	{"correspondence", "messages and replies that are owed to somebody"},
	{"digital", "backups, updates, passwords, the machines themselves — bound by nothing"},

	{"family", "partner, children, parents, relatives"},
	{"school", "term dates, the run, homework and the forms that come with it"},
	{"social", "friends, meeting people, calls and replies that are not work"},
	{"community", "volunteering, neighbours, clubs and the groups that meet"},
	{"faith", "prayer, services and observances, which happen at set times"},

	{"health", "appointments and anything medical, bound by when a surgery is open"},
	{"medication", "doses and refills, bound to the clock rather than to opening hours"},
	{"selfcare", "grooming, haircuts, winding down"},
	{"fitness", "exercise and sport"},

	{"finance", "bills, payments, budgeting, taxes"},
	{"shopping", "buying things, in a shop or online"},
	{"errands", "short tasks that have to happen out of the house, and so are bound by opening hours"},
	{"vehicle", "the car or the bike: fuel, servicing, tests"},

	{"chores", "housework and maintenance at home, bound by nothing but being awake"},
	{"cooking", "meals and what has to happen before one, bound to the hours people eat"},
	{"repairs", "fixing what is broken, and the tradespeople who come to"},
	{"outdoors", "anything wanting daylight or dry weather, the garden included"},
	{"pets", "feeding, walking, the vet"},

	{"learning", "study, courses, reading to learn something"},
	{"creative", "making things, writing, music, art"},
	{"entertainment", "watching, playing, listening, going out for fun"},
	{"travel", "trips, packing, bookings, getting somewhere"},
}
