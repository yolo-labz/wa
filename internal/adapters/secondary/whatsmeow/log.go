package whatsmeow

import (
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// slogWALog adapts a *slog.Logger into whatsmeow's waLog.Logger interface.
// Per research §D10 and data-model.md §"slog → waLog bridge" there is
// exactly ONE such bridge in the project; do not reinvent it per file.
// The whatsmeow library calls the Debugf/Infof/Warnf/Errorf methods with
// printf-style format strings, so we eagerly Sprintf into the slog message
// field (slog does not have a native printf wrapper).
type slogWALog struct {
	log *slog.Logger
}

// NewSlogLogger wraps a *slog.Logger so it satisfies whatsmeow's
// waLog.Logger interface. The Adapter constructor in commit 4 calls this
// exactly once with the daemon-supplied logger and passes the result to
// whatsmeow.NewClient. Production uses feature 002's slog wiring; tests
// may pass slog.New(slog.NewTextHandler(io.Discard, nil)) to silence.
func NewSlogLogger(l *slog.Logger) waLog.Logger { return &slogWALog{log: l} }

func (s *slogWALog) Debugf(msg string, args ...any) { s.log.Debug(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Infof(msg string, args ...any)  { s.log.Info(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Warnf(msg string, args ...any)  { s.log.Warn(fmt.Sprintf(msg, args...)) }
func (s *slogWALog) Errorf(msg string, args ...any) { s.log.Error(fmt.Sprintf(msg, args...)) }

// Sub returns a child logger tagged with the given module name under the
// "module" slog attribute. whatsmeow uses this to partition internal log
// output by subsystem (e.g. "Client", "Socket", "XMPP").
func (s *slogWALog) Sub(module string) waLog.Logger {
	return &slogWALog{log: s.log.With("module", module)}
}
