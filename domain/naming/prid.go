package naming

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// GeneratePRID returns a unique run identifier in the form YYYYMMDD-xxxxxx
// where xxxxxx is 6 random hex characters. Callers are expected to generate
// at most one PRID per (onboard file, group name) and reuse it for every
// component/tag that maps into the same pull request.
func GeneratePRID() string {
	date := time.Now().UTC().Format("20060102")
	randomBytes := make([]byte, 3)
	if _, err := rand.Read(randomBytes); err != nil {
		nanos := time.Now().UnixNano()
		randomBytes = []byte{byte(nanos), byte(nanos >> 8), byte(nanos >> 16)}
	}
	return date + "-" + hex.EncodeToString(randomBytes)
}
