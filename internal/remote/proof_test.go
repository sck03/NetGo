package remote

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestSharedPossessionVectors(t *testing.T) {
	f, e := os.Open("../../tests/contracts/remote/possession.txt")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	v := map[string][]byte{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		parts := strings.Fields(scan.Text())
		if len(parts) != 2 {
			t.Fatal("bad vector")
		}
		data, e := hex.DecodeString(parts[1])
		if e != nil {
			t.Fatal(e)
		}
		v[parts[0]] = data
	}
	if e = scan.Err(); e != nil {
		t.Fatal(e)
	}
	msg, e := ProofMessage(v["ticket"], v["challenge"], v["channel"])
	if e != nil {
		t.Fatal(e)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x31}, 32))
	if !bytes.Equal(key.Public().(ed25519.PublicKey), v["key"]) || !bytes.Equal(ed25519.Sign(key, msg), v["proof"]) {
		t.Fatal("noncanonical possession vector")
	}
}
