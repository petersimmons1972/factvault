package agentcomms

import "bytes"

// bytesReader is a tiny indirection to avoid importing bytes everywhere.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
