package workers

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func TestDecompressBombRejected(t *testing.T) {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	chunk := bytes.Repeat([]byte("a"), 1024*1024)
	for range 101 {
		if _, err := zw.Write(chunk); err != nil {
			t.Fatalf("write chunk: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zlib writer: %v", err)
	}

	if _, err := decompress(compressed.Bytes()); err == nil {
		t.Fatal("expected oversized decompression to fail")
	}
}
