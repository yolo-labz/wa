package app

// SetLastEventUnixForTest seeds the bridge's last-event clock so external
// tests (e.g. handleHealth + soft-stale watchdog suites) can drive the
// (now - lastEvent) stale computation without sleeping or mocking
// time.Now. Defined in a *_test.go file so it never ships in production
// binaries — it exists solely to keep `package app_test` tests free of
// the memory↔app import cycle that would arise from a white-box test.
func (b *EventBridge) SetLastEventUnixForTest(ts int64) {
	b.lastEventUnix.Store(ts)
}
