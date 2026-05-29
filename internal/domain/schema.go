package domain

// LayoutSchemaVersion is the on-disk profile-layout schema version the wad
// daemon writes to <config>/wa/.schema-version and that `wa doctor` asserts.
//
// `1` is the pre-feature-008 implicit single-profile layout; `2` is the
// feature-008 per-profile layout. It is intentionally distinct from the
// per-database SQLite PRAGMA user_version values (the history DB's table
// schema is tracked by a separate, independently-versioned user_version that
// climbs as columns are added) — those track table schemas, this tracks the
// directory layout.
//
// It lives here, not in cmd/wad, so the daemon's migrator (the writer) and the
// CLI doctor (the checker) reference one constant and cannot drift. A
// duplicated literal is exactly what caused #180 item 5: the doctor hardcoded
// "expected v4" while the daemon kept writing v2, so every healthy install got
// a spurious WARN.
const LayoutSchemaVersion = 2
