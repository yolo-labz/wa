package whatsmeow

import "testing"

// TestSniffExt_AdvertisedWinsWhenSniffIsGeneric: on a host whose mime table
// maps application/octet-stream to "bin" (any host with /etc/mime.types),
// unknown bytes must still take the advertised type's extension, as sniffMime
// takes its MIME; a specific sniff still wins over the advertised type.
func TestSniffExt_AdvertisedWinsWhenSniffIsGeneric(t *testing.T) {
	old := extsByType
	t.Cleanup(func() { extsByType = old })
	table := map[string][]string{
		"application/octet-stream": {".bin"},
		"application/x-wa-fixture": {".wafix"},
		"image/png":                {".png"},
	}
	extsByType = func(typ string) ([]string, error) { return table[typ], nil }

	unknown := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for _, c := range []struct {
		name       string
		payload    []byte
		advertised string
		want       string
	}{
		{"generic sniff, advertised known", unknown, "application/x-wa-fixture", "wafix"},
		{"generic sniff, advertised with params", unknown, "application/x-wa-fixture; v=1", "wafix"},
		{"generic sniff, advertised unknown", unknown, "application/x-nope", "bin"},
		{"generic sniff, nothing advertised", unknown, "", "bin"},
		{"specific sniff beats advertised", png, "application/x-wa-fixture", "png"},
	} {
		if got := sniffExt(c.payload, c.advertised); got != c.want {
			t.Errorf("%s: sniffExt = %q, want %q", c.name, got, c.want)
		}
	}
}
