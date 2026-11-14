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
//
// The "QRChannel" module logs every pairing QR payload at debug
// ("Emitting QR code %s"); its lines keep the format string and drop the
// arguments, so the payload never reaches a log (spec 116 SR-116-06).
type slogWALog struct {
	log  *slog.Logger
	bare bool
}

func (s *slogWALog) line(msg string, args []any) string {
	if s.bare {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// NewSlogLogger wraps a *slog.Logger so it satisfies whatsmeow's
// waLog.Logger interface. The Adapter constructor in commit 4 calls this
// exactly once with the daemon-supplied logger and passes the result to
// whatsmeow.NewClient. Production uses feature 002's slog wiring; tests
// may pass slog.New(slog.DiscardHandler) to silence.
func NewSlogLogger(l *slog.Logger) waLog.Logger { return &slogWALog{log: l} }

func (s *slogWALog) Debugf(msg string, args ...any) { s.log.Debug(s.line(msg, args)) }
func (s *slogWALog) Infof(msg string, args ...any)  { s.log.Info(s.line(msg, args)) }
func (s *slogWALog) Warnf(msg string, args ...any)  { s.log.Warn(s.line(msg, args)) }
func (s *slogWALog) Errorf(msg string, args ...any) { s.log.Error(s.line(msg, args)) }

// Sub returns a child logger tagged with the given module name under the
// "module" slog attribute. whatsmeow uses this to partition internal log
// output by subsystem (e.g. "Client", "Socket", "XMPP").
func (s *slogWALog) Sub(module string) waLog.Logger {
	return &slogWALog{log: s.log.With("module", module), bare: s.bare || module == "QRChannel"}
}
