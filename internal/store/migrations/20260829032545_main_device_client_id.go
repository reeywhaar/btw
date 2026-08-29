package migrations

// Which browser a subscription belongs to, so a rotated endpoint replaces its row rather
// than adding one beside it.
//
// A push endpoint identifies a *subscription*, not a browser, and browsers replace
// subscriptions on their own — after a permission is re-granted, after site data is cleared,
// after a pushsubscriptionchange. Registering upserts on the endpoint, so the new
// subscription arrived as a new row and the old one stayed. Both were live at the push
// service, so one press of "send one now" sent two pushes and one browser showed two
// notifications.
//
// Neither the Topic header nor the notification tag can help with that: they collapse
// messages within one subscription, and this is two.
//
// Empty for rows that predate this, and empty is never matched against — an unknown browser
// must not collapse another unknown browser's row.
var mainDeviceClientID = Migration{
	Name: "20260829032545_main_device_client_id",
	Up: exec(`
ALTER TABLE devices ADD COLUMN client_id TEXT NOT NULL DEFAULT '';
CREATE INDEX devices_client ON devices(principal_id, client_id);
`),
}
