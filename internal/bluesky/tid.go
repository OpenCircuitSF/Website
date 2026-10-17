package bluesky

const tidAlphabet = "234567abcdefghijklmnopqrstuvwxyz"

// TID builds a record key in AT Protocol's timestamp-identifier format: a
// 64-bit value, top bit zero, made of microseconds since the epoch shifted
// left ten bits with a 10-bit clock id in the low bits, written as 13
// characters of base32-sortable, most significant first.
//
// outreach derives micros and clock from the post row, so the same post
// always maps to the same key: that determinism is what lets a retry find
// out whether an earlier attempt already created the record.
func TID(micros int64, clock uint16) string {
	v := uint64(micros)<<10 | uint64(clock&1023)
	v &^= 1 << 63
	var out [13]byte
	for i := 12; i >= 0; i-- {
		out[i] = tidAlphabet[v&31]
		v >>= 5
	}
	return string(out[:])
}
