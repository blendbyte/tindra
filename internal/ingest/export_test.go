package ingest

import "context"

// DecodeProfile reverses EncodeProfile with the default size budget. Production
// code decodes with DecodeProfileLimited and a per-request budget.
func DecodeProfile(encoding int16, data []byte) (*Profile, error) {
	p, _, err := DecodeProfileLimited(context.Background(), encoding, data, maxDecodedProfileBytes)
	return p, err
}
