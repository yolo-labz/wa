package sqlitehistory_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

func TestGetRawProtoScopesOrRejectsAmbiguity(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-a", true: "first-b"}[reverse], func(t *testing.T) {
			s := openTempStore(t)
			ctx := context.Background()
			rows := []struct {
				chat string
				raw  []byte
			}{{"11111111@s.whatsapp.net", []byte("payload-a")}, {"22222222@s.whatsapp.net", []byte("payload-b")}}
			if reverse {
				rows[0], rows[1] = rows[1], rows[0]
			}
			for i, row := range rows {
				if err := s.InsertRaw(ctx, domain.HistoryRecord{
					ChatJID: row.chat, SenderJID: row.chat, MessageID: "DUPLICATE",
					Timestamp: int64(i + 1), Body: "", MediaType: "image/jpeg",
					Caption: "", PushName: "", IsFromMe: false,
					RawProto: row.raw, SenderAltJID: "", AddressingMode: "",
				}); err != nil {
					t.Fatalf("InsertRaw: %v", err)
				}
			}

			if _, _, err := s.GetRawProto(ctx, "", "DUPLICATE"); !errors.Is(err, domain.ErrMessageIDAmbiguous) {
				t.Fatalf("unqualified error = %v, want ErrMessageIDAmbiguous", err)
			}
			for _, row := range rows {
				chat, raw, err := s.GetRawProto(ctx, row.chat, "DUPLICATE")
				if err != nil || chat != row.chat || string(raw) != string(row.raw) {
					t.Fatalf("qualified %s = (%q,%q,%v)", row.chat, chat, raw, err)
				}
			}
			if _, _, err := s.GetRawProto(ctx, "33333333@s.whatsapp.net", "DUPLICATE"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cross-chat miss = %v, want os.ErrNotExist", err)
			}
		})
	}
}
