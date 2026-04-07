// Package sqlitestore wraps whatsmeow's sqlstore.Container with a file
// lock and tightened file permissions. The schema is owned by whatsmeow
// upstream; this package adds no migrations.
package sqlitestore
